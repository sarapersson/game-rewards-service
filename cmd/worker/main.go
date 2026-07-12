package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"unicode/utf8"

	"github.com/sarapersson/game-rewards-service/internal/config"
	"github.com/sarapersson/game-rewards-service/internal/outbox"
	"github.com/sarapersson/game-rewards-service/internal/postgres"
)

const (
	maxWorkerIDLength     = 128
	workerInstanceIDBytes = 16
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", slog.Any("error", err))
		return 1
	}

	logger := newLogger(cfg)

	dbPool, err := postgres.OpenPool(context.Background(), cfg.Database)
	if err != nil {
		logger.Error("open postgres pool", slog.Any("error", err))
		return 1
	}
	defer dbPool.Close()

	if err := postgres.Ping(
		context.Background(),
		dbPool,
		cfg.Database.PingTimeout,
	); err != nil {
		logger.Error("ping postgres", slog.Any("error", err))
		return 1
	}

	workerID, err := newWorkerID(cfg.ServiceName)
	if err != nil {
		logger.Error("create worker id", slog.Any("error", err))
		return 1
	}

	store := outbox.NewPostgresStore(
		dbPool,
		cfg.Database.QueryTimeout,
	)

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
		},
	)
	if err != nil {
		logger.Error("create outbox worker", slog.Any("error", err))
		return 1
	}

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()

	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(shutdownCh)

	errCh := make(chan error, 1)

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

	go func() {
		errCh <- worker.Run(workerCtx)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			logger.Error("outbox worker failed", slog.Any("error", err))
			return 1
		}

		logger.Info("outbox worker stopped")
		return 0

	case sig := <-shutdownCh:
		logger.Info(
			"shutdown signal received",
			slog.String("signal", sig.String()),
		)

		cancelWorker()

		shutdownCtx, cancelShutdown := context.WithTimeout(
			context.Background(),
			cfg.ShutdownTimeout,
		)
		defer cancelShutdown()

		select {
		case err := <-errCh:
			if err != nil {
				logger.Error(
					"outbox worker shutdown failed",
					slog.Any("error", err),
				)
				return 1
			}

			logger.Info("shutdown complete")
			return 0

		case <-shutdownCtx.Done():
			logger.Error(
				"outbox worker shutdown timed out",
				slog.String("error", shutdownCtx.Err().Error()),
			)
			return 1
		}
	}
}

func newWorkerID(serviceName string) (string, error) {
	instanceID := make([]byte, workerInstanceIDBytes)
	if _, err := rand.Read(instanceID); err != nil {
		return "", fmt.Errorf("generate worker instance id: %w", err)
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
