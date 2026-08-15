package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/sarapersson/game-rewards-service/internal/config"
	"github.com/sarapersson/game-rewards-service/internal/rewards"
)

// RequestObserver records bounded HTTP request outcomes.
type RequestObserver interface {
	ObserveRequest(route, method string, status int, duration time.Duration)
}

// RewardClaimObserver records reward and idempotency outcomes after a service call.
type RewardClaimObserver interface {
	ObserveRewardClaim(result rewards.CreateClaimResult, err error)
}

// ServerObservability contains optional process-local observability dependencies.
type ServerObservability struct {
	MetricsHandler      http.Handler
	RequestObserver     RequestObserver
	RewardClaimObserver RewardClaimObserver
}

// NewServer builds the HTTP server with routes, middleware, metrics, and production-safe timeouts.
func NewServer(
	cfg config.Config,
	logger *slog.Logger,
	rewardClaims rewardClaimCreator,
	observability ServerObservability,
	readinessChecks ...ReadinessCheck,
) (*http.Server, error) {
	if logger == nil {
		return nil, errors.New("http logger is required")
	}
	if rewardClaims == nil {
		return nil, errors.New("reward claim service is required")
	}

	return &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           newRouter(logger, rewardClaims, observability, readinessChecks...),
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}, nil
}
