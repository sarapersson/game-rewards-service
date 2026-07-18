package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/sarapersson/game-rewards-service/internal/adminhttp"
	"github.com/sarapersson/game-rewards-service/internal/config"
	"github.com/sarapersson/game-rewards-service/internal/health"
	"github.com/sarapersson/game-rewards-service/internal/observability"
	"github.com/sarapersson/game-rewards-service/internal/outbox"
	"github.com/sarapersson/game-rewards-service/internal/postgres"
)

const (
	maxWorkerIDLength     = 128
	workerInstanceIDBytes = 16
)

type componentResult struct {
	name string
	err  error
}

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", slog.Any("error", err))
		return 1
	}

	logger := newLogger(cfg).With(slog.String("component", "worker"))

	dbPool, err := postgres.OpenPool(context.Background(), cfg.Database)
	if err != nil {
		logger.Error("open postgres pool", slog.Any("error", err))
		return 1
	}
	defer dbPool.Close()

	if err := postgres.Ping(context.Background(), dbPool, cfg.Database.PingTimeout); err != nil {
		logger.Error("ping postgres", slog.Any("error", err))
		return 1
	}

	registry, err := observability.NewRegistry()
	if err != nil {
		logger.Error("create metrics registry", slog.Any("error", err))
		return 1
	}

	httpMetrics, err := observability.NewHTTPMetrics(registry)
	if err != nil {
		logger.Error("register HTTP metrics", slog.Any("error", err))
		return 1
	}

	workerMetrics, err := observability.NewWorkerMetrics(registry)
	if err != nil {
		logger.Error("register worker metrics", slog.Any("error", err))
		return 1
	}

	workerID, err := newWorkerID(cfg.ServiceName)
	if err != nil {
		logger.Error("create worker id", slog.Any("error", err))
		return 1
	}

	store := outbox.NewPostgresStore(dbPool, cfg.Database.QueryTimeout)

	publisher, err := outbox.NewLoggingPublisher(logger)
	if err != nil {
		logger.Error("create outbox publisher", slog.Any("error", err))
		return 1
	}

	worker, err := outbox.NewWorker(
		store,
		publisher,
		logger,
		outbox.WorkerConfig{
			WorkerID:       workerID,
			PollInterval:   cfg.Worker.PollInterval,
			LockTTL:        cfg.Worker.OutboxLockTTL,
			PublishTimeout: cfg.Worker.PublishTimeout,
			MaxAttempts:    cfg.Worker.MaxAttempts,
			Backoff: outbox.BackoffPolicy{
				Base: cfg.Worker.BaseBackoff,
				Max:  cfg.Worker.MaxBackoff,
			},
			Observer: workerMetrics,
		},
	)
	if err != nil {
		logger.Error("create outbox worker", slog.Any("error", err))
		return 1
	}

	var workerReady atomic.Bool
	adminServer := adminhttp.NewServer(
		cfg,
		logger,
		observability.Handler(registry),
		httpMetrics,
		health.Check{
			Name: "postgres",
			Check: func(ctx context.Context) error {
				return postgres.Ping(ctx, dbPool, cfg.Database.PingTimeout)
			},
		},
		health.Check{
			Name: "worker",
			Check: func(context.Context) error {
				if !workerReady.Load() {
					return errors.New("worker loop is not running")
				}
				return nil
			},
		},
	)

	listener, err := net.Listen("tcp", adminServer.Addr)
	if err != nil {
		logger.Error(
			"listen on worker admin address",
			slog.String("addr", adminServer.Addr),
			slog.Any("error", err),
		)
		return 1
	}
	defer listener.Close()

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()

	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(shutdownCh)

	results := make(chan componentResult, 2)

	hostname, hostnameErr := os.Hostname()
	if hostnameErr != nil {
		hostname = "unknown"
	}

	logger.Info(
		"starting outbox worker",
		slog.String("worker_id", workerID),
		slog.String("hostname", hostname),
		slog.Int("process_id", os.Getpid()),
	)
	logger.Info(
		"starting worker admin server",
		slog.String("addr", adminServer.Addr),
	)

	go func() {
		defer workerReady.Store(false)

		err := worker.Run(workerCtx, func() {
			workerReady.Store(true)
		})

		results <- componentResult{
			name: "worker",
			err:  err,
		}
	}()

	go func() {
		err := adminServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}

		results <- componentResult{
			name: "admin_server",
			err:  err,
		}
	}()

	select {
	case sig := <-shutdownCh:
		logger.Info(
			"shutdown signal received",
			slog.String("signal", sig.String()),
		)

		if err := stopComponents(
			cfg.ShutdownTimeout,
			adminServer,
			&workerReady,
			cancelWorker,
			results,
			2,
		); err != nil {
			logger.Error(
				"worker shutdown failed",
				slog.Any("error", err),
			)
			return 1
		}

		logger.Info("shutdown complete")
		return 0

	case result := <-results:
		workerReady.Store(false)

		componentErr := result.err
		if componentErr == nil {
			componentErr = fmt.Errorf(
				"%s stopped unexpectedly",
				result.name,
			)
		}

		logger.Error(
			"worker component failed",
			slog.String("component_name", result.name),
			slog.Any("error", componentErr),
		)

		if err := stopComponents(
			cfg.ShutdownTimeout,
			adminServer,
			&workerReady,
			cancelWorker,
			results,
			1,
		); err != nil {
			logger.Error(
				"worker component cleanup failed",
				slog.Any("error", err),
			)
		}

		return 1
	}
}

func stopComponents(
	shutdownTimeout time.Duration,
	adminServer *http.Server,
	workerReady *atomic.Bool,
	cancelWorker context.CancelFunc,
	results <-chan componentResult,
	remaining int,
) error {
	workerReady.Store(false)
	cancelWorker()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		shutdownTimeout,
	)
	defer cancel()

	shutdownErr := adminServer.Shutdown(ctx)
	var componentErr error

	for remaining > 0 {
		select {
		case result := <-results:
			remaining--

			if result.err != nil {
				componentErr = errors.Join(
					componentErr,
					fmt.Errorf(
						"%s stopped: %w",
						result.name,
						result.err,
					),
				)
			}

		case <-ctx.Done():
			_ = adminServer.Close()
			return fmt.Errorf(
				"shutdown timed out: %w",
				ctx.Err(),
			)
		}
	}

	if shutdownErr != nil {
		shutdownErr = fmt.Errorf(
			"shutdown worker admin server: %w",
			shutdownErr,
		)
	}

	return errors.Join(shutdownErr, componentErr)
}

func newWorkerID(serviceName string) (string, error) {
	instanceID := make([]byte, workerInstanceIDBytes)
	if _, err := rand.Read(instanceID); err != nil {
		return "", fmt.Errorf(
			"generate worker instance id: %w",
			err,
		)
	}

	workerID := fmt.Sprintf(
		"%s-worker-%s",
		serviceName,
		hex.EncodeToString(instanceID),
	)

	if utf8.RuneCountInString(workerID) > maxWorkerIDLength {
		return "", fmt.Errorf(
			"worker id exceeds %d characters",
			maxWorkerIDLength,
		)
	}

	return workerID, nil
}

func newLogger(cfg config.Config) *slog.Logger {
	handler := slog.NewJSONHandler(
		os.Stdout,
		&slog.HandlerOptions{
			Level: cfg.Log.Level,
		},
	)

	return slog.New(handler).With(
		slog.String("service", cfg.ServiceName),
		slog.String("env", cfg.AppEnv),
	)
}
