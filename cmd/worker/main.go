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

	"github.com/sarapersson/game-rewards-service/internal/adminhttp"
	"github.com/sarapersson/game-rewards-service/internal/config"
	"github.com/sarapersson/game-rewards-service/internal/health"
	"github.com/sarapersson/game-rewards-service/internal/observability"
	"github.com/sarapersson/game-rewards-service/internal/outbox"
	"github.com/sarapersson/game-rewards-service/internal/postgres"
)

const workerInstanceIDBytes = 16

type componentResult struct {
	name string
	err  error
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	exitCode := run(ctx)
	stop()
	os.Exit(exitCode)
}

func run(ctx context.Context) int {
	if ctx.Err() != nil {
		return 0
	}

	cfg, err := config.LoadWorker()
	if err != nil {
		slog.Error("load config", slog.Any("error", err))
		return 1
	}

	logger := newLogger(cfg).With(slog.String("component", "worker"))

	dbPool, err := postgres.OpenPool(ctx, cfg.Database.URL)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return 0
		}

		logger.Error("open postgres pool", slog.Any("error", err))
		return 1
	}
	defer dbPool.Close()

	if err := postgres.Ping(ctx, dbPool, cfg.Database.PingTimeout); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return 0
		}

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

	workerID := newWorkerID(cfg.ServiceName)

	store, err := outbox.NewPostgresStore(dbPool, cfg.Database.QueryTimeout)
	if err != nil {
		logger.Error("create outbox store", slog.Any("error", err))
		return 1
	}

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
			PollInterval:   cfg.Outbox.PollInterval,
			LockTTL:        cfg.Outbox.LockTTL,
			PublishTimeout: cfg.Outbox.PublishTimeout,
			MaxAttempts:    cfg.Outbox.MaxAttempts,
			BaseBackoff:    cfg.Outbox.BaseBackoff,
			MaxBackoff:     cfg.Outbox.MaxBackoff,
			Observer:       workerMetrics,
		},
	)
	if err != nil {
		logger.Error("create outbox worker", slog.Any("error", err))
		return 1
	}

	var workerReady atomic.Bool
	adminServer := adminhttp.NewServer(
		cfg.AdminHTTP,
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

	if ctx.Err() != nil {
		return 0
	}

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

	if ctx.Err() != nil {
		return 0
	}

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()

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
	case <-ctx.Done():
		logger.Info(
			"shutdown requested",
			slog.String("shutdown_cause", context.Cause(ctx).Error()),
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

func newWorkerID(serviceName string) string {
	var instanceID [workerInstanceIDBytes]byte
	rand.Read(instanceID[:])

	return fmt.Sprintf(
		"%s-worker-%s",
		serviceName,
		hex.EncodeToString(instanceID[:]),
	)
}

func newLogger(cfg config.WorkerConfig) *slog.Logger {
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
