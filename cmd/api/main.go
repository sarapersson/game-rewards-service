package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sarapersson/game-rewards-service/internal/config"
	"github.com/sarapersson/game-rewards-service/internal/httpapi"
	"github.com/sarapersson/game-rewards-service/internal/observability"
	"github.com/sarapersson/game-rewards-service/internal/postgres"
	"github.com/sarapersson/game-rewards-service/internal/rewards"
)

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

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", slog.Any("error", err))
		return 1
	}

	logger := newLogger(cfg).With(slog.String("component", "api"))

	dbPool, err := postgres.OpenPool(ctx, cfg.Database)
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

	rewardMetrics, err := observability.NewRewardMetrics(registry)
	if err != nil {
		logger.Error("register reward metrics", slog.Any("error", err))
		return 1
	}

	rewardStore := rewards.NewPostgresStore(dbPool, cfg.Database.QueryTimeout)
	rewardService := rewards.NewService(rewardStore)

	server := httpapi.NewServerWithObservability(
		cfg,
		logger,
		rewardService,
		httpapi.ServerObservability{
			MetricsHandler:      observability.Handler(registry),
			RequestObserver:     httpMetrics,
			RewardClaimObserver: rewardMetrics,
		},
		httpapi.ReadinessCheck{
			Name: "postgres",
			Check: func(ctx context.Context) error {
				return postgres.Ping(ctx, dbPool, cfg.Database.PingTimeout)
			},
		},
	)

	if ctx.Err() != nil {
		return 0
	}

	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		logger.Error(
			"listen on http address",
			slog.String("addr", server.Addr),
			slog.Any("error", err),
		)
		return 1
	}
	defer listener.Close()

	if ctx.Err() != nil {
		return 0
	}

	serverErrCh := make(chan error, 1)

	logger.Info("starting http server", slog.String("addr", server.Addr))

	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}

		serverErrCh <- err
	}()

	select {
	case err := <-serverErrCh:
		if err != nil {
			logger.Error("http server failed", slog.Any("error", err))
			return 1
		}

		return 0

	case <-ctx.Done():
		logger.Info(
			"shutdown requested",
			slog.String("shutdown_cause", context.Cause(ctx).Error()),
		)

		if err := stopHTTPServer(cfg.ShutdownTimeout, server, serverErrCh); err != nil {
			logger.Error("http server shutdown failed", slog.Any("error", err))
			return 1
		}

		logger.Info("shutdown complete")
		return 0
	}
}

func stopHTTPServer(
	shutdownTimeout time.Duration,
	server *http.Server,
	serveResult <-chan error,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	shutdownErr := server.Shutdown(ctx)
	var closeErr error
	if shutdownErr != nil {
		closeErr = server.Close()
	}

	serverErr := <-serveResult

	if shutdownErr != nil {
		shutdownErr = fmt.Errorf("graceful http server shutdown: %w", shutdownErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("force close http server: %w", closeErr)
	}
	if serverErr != nil {
		serverErr = fmt.Errorf("http server stopped: %w", serverErr)
	}

	return errors.Join(shutdownErr, closeErr, serverErr)
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
