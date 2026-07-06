package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/sarapersson/game-rewards-service/internal/config"
	"github.com/sarapersson/game-rewards-service/internal/httpapi"
	"github.com/sarapersson/game-rewards-service/internal/postgres"
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

	if err := postgres.Ping(context.Background(), dbPool, cfg.Database.PingTimeout); err != nil {
		logger.Error("ping postgres", slog.Any("error", err))
		return 1
	}

	server := httpapi.NewServer(cfg, logger, httpapi.ReadinessCheck{
		Name: "postgres",
		Check: func(ctx context.Context) error {
			return postgres.Ping(ctx, dbPool, cfg.Database.PingTimeout)
		},
	})

	errCh := make(chan error, 1)

	go func() {
		logger.Info(
			"starting http server",
			slog.String("addr", server.Addr),
			slog.String("app_env", cfg.AppEnv),
			slog.String("service_name", cfg.ServiceName),
		)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}

		errCh <- nil
	}()

	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(shutdownCh)

	select {
	case err := <-errCh:
		if err != nil {
			logger.Error("http server failed", slog.Any("error", err))
			return 1
		}

		return 0

	case sig := <-shutdownCh:
		logger.Info("shutdown signal received", slog.String("signal", sig.String()))

		ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown failed", slog.Any("error", err))
			return 1
		}

		logger.Info("shutdown complete")
		return 0
	}
}

func newLogger(cfg config.Config) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.Log.Level,
	})

	return slog.New(handler).With(
		slog.String("service", cfg.ServiceName),
		slog.String("env", cfg.AppEnv),
	)
}
