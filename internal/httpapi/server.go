package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/sarapersson/game-rewards-service/internal/config"
)

// NewServer builds the HTTP server with routes, middleware, and production-safe timeouts.
func NewServer(cfg config.Config, logger *slog.Logger, readinessChecks ...ReadinessCheck) *http.Server {
	return &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           newRouter(logger, readinessChecks...),
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}
}
