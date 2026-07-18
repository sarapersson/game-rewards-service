package observability

import (
	"errors"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sarapersson/game-rewards-service/internal/rewards"
)

// RewardMetrics records reward claim and idempotency outcomes.
type RewardMetrics struct {
	claims      *prometheus.CounterVec
	idempotency *prometheus.CounterVec
}

func NewRewardMetrics(registerer prometheus.Registerer) (*RewardMetrics, error) {
	metrics := &RewardMetrics{
		claims: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "reward_claim",
			Name:      "operations_total",
			Help:      "Total reward claim operations by outcome.",
		}, []string{"outcome"}),
		idempotency: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "idempotency",
			Name:      "operations_total",
			Help:      "Total idempotency operations by outcome.",
		}, []string{"outcome"}),
	}

	if err := register(registerer, metrics.claims, metrics.idempotency); err != nil {
		return nil, err
	}
	return metrics, nil
}

func (m *RewardMetrics) ObserveRewardClaim(result rewards.CreateClaimResult, err error) {
	if m == nil {
		return
	}

	if err == nil {
		m.idempotency.WithLabelValues(mapIdempotencySuccess(result)).Inc()
		m.claims.WithLabelValues(mapClaimSuccess(result)).Inc()
		return
	}

	switch {
	case errors.Is(err, rewards.ErrIdempotencyKeyReused):
		m.claims.WithLabelValues("idempotency_key_reused").Inc()
		m.idempotency.WithLabelValues("key_reused").Inc()
	case errors.Is(err, rewards.ErrIdempotencyInProgress):
		m.claims.WithLabelValues("idempotency_in_progress").Inc()
		m.idempotency.WithLabelValues("in_progress").Inc()
	case rewards.IsValidationError(err):
		m.claims.WithLabelValues("invalid").Inc()
	case errors.Is(err, rewards.ErrUnavailable):
		m.claims.WithLabelValues("unavailable").Inc()
		m.idempotency.WithLabelValues("failed").Inc()
	case errors.Is(err, rewards.ErrDuplicateClaim):
		m.claims.WithLabelValues("already_claimed").Inc()
		m.idempotency.WithLabelValues("failed").Inc()
	default:
		m.claims.WithLabelValues("internal_error").Inc()
		m.idempotency.WithLabelValues("failed").Inc()
	}
}

func mapClaimSuccess(result rewards.CreateClaimResult) string {
	if result.Replayed {
		switch result.StatusCode {
		case http.StatusCreated:
			return "replayed_created"
		case http.StatusConflict:
			return "replayed_already_claimed"
		default:
			return "internal_error"
		}
	}

	switch result.StatusCode {
	case http.StatusCreated:
		return "created"
	case http.StatusConflict:
		return "already_claimed"
	default:
		return "internal_error"
	}
}

func mapIdempotencySuccess(result rewards.CreateClaimResult) string {
	if result.Replayed {
		return "replayed"
	}
	return "new"
}
