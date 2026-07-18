package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sarapersson/game-rewards-service/internal/outbox"
)

// WorkerMetrics implements outbox.Observer with bounded Prometheus labels.
type WorkerMetrics struct {
	claims          *prometheus.CounterVec
	publishAttempts *prometheus.CounterVec
	publishDuration *prometheus.HistogramVec
	published       *prometheus.CounterVec
	retries         *prometheus.CounterVec
	deadLetters     *prometheus.CounterVec
	leaseLosses     *prometheus.CounterVec
	operationErrors *prometheus.CounterVec
}

func NewWorkerMetrics(registerer prometheus.Registerer) (*WorkerMetrics, error) {
	metrics := &WorkerMetrics{
		claims: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "outbox", Name: "claim_attempts_total",
			Help: "Total outbox claim attempts by outcome.",
		}, []string{"outcome"}),
		publishAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "outbox", Name: "publish_attempts_total",
			Help: "Total outbox publish attempts by event type and outcome.",
		}, []string{"event_type", "outcome"}),
		publishDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: "outbox", Name: "publish_duration_seconds",
			Help: "Outbox publisher duration in seconds.", Buckets: durationBuckets(),
		}, []string{"event_type", "outcome"}),
		published: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "outbox", Name: "events_published_total",
			Help: "Total outbox events successfully published and finalized.",
		}, []string{"event_type"}),
		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "outbox", Name: "retries_scheduled_total",
			Help: "Total outbox retries successfully scheduled.",
		}, []string{"event_type", "failure_reason"}),
		deadLetters: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "outbox", Name: "events_dead_lettered_total",
			Help: "Total outbox events successfully moved to dead letter.",
		}, []string{"event_type", "failure_reason"}),
		leaseLosses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "outbox", Name: "lease_losses_total",
			Help: "Total outbox lease ownership losses by operation.",
		}, []string{"event_type", "operation"}),
		operationErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "outbox", Name: "operation_errors_total",
			Help: "Total outbox worker operation errors.",
		}, []string{"operation"}),
	}

	if err := register(registerer,
		metrics.claims,
		metrics.publishAttempts,
		metrics.publishDuration,
		metrics.published,
		metrics.retries,
		metrics.deadLetters,
		metrics.leaseLosses,
		metrics.operationErrors,
	); err != nil {
		return nil, err
	}
	return metrics, nil
}

func (m *WorkerMetrics) ObserveClaim(outcome outbox.ClaimOutcome) {
	if m != nil {
		m.claims.WithLabelValues(normalizeClaimOutcome(outcome)).Inc()
	}
}

func (m *WorkerMetrics) ObservePublish(eventType string, outcome outbox.PublishOutcome, duration time.Duration) {
	if m == nil {
		return
	}
	eventType = normalizeEventType(eventType)
	publishOutcome := normalizePublishOutcome(outcome)
	m.publishAttempts.WithLabelValues(eventType, publishOutcome).Inc()
	m.publishDuration.WithLabelValues(eventType, publishOutcome).Observe(duration.Seconds())
}

func (m *WorkerMetrics) ObservePublished(eventType string) {
	if m != nil {
		m.published.WithLabelValues(normalizeEventType(eventType)).Inc()
	}
}

func (m *WorkerMetrics) ObserveRetry(eventType, failureReason string) {
	if m != nil {
		m.retries.WithLabelValues(normalizeEventType(eventType), normalizeFailureReason(failureReason)).Inc()
	}
}

func (m *WorkerMetrics) ObserveDeadLetter(eventType, failureReason string) {
	if m != nil {
		m.deadLetters.WithLabelValues(normalizeEventType(eventType), normalizeFailureReason(failureReason)).Inc()
	}
}

func (m *WorkerMetrics) ObserveLeaseLoss(eventType string, operation outbox.Operation) {
	if m != nil {
		m.leaseLosses.WithLabelValues(normalizeEventType(eventType), normalizeOperation(operation)).Inc()
	}
}

func (m *WorkerMetrics) ObserveOperationError(operation outbox.Operation) {
	if m != nil {
		m.operationErrors.WithLabelValues(normalizeOperation(operation)).Inc()
	}
}

func normalizeEventType(eventType string) string {
	if eventType == "RewardClaimed" {
		return "reward_claimed"
	}
	return "unknown"
}

func normalizeClaimOutcome(outcome outbox.ClaimOutcome) string {
	switch outcome {
	case outbox.ClaimOutcomeClaimed, outbox.ClaimOutcomeEmpty, outbox.ClaimOutcomeError:
		return string(outcome)
	default:
		return "error"
	}
}

func normalizePublishOutcome(outcome outbox.PublishOutcome) string {
	switch outcome {
	case outbox.PublishOutcomeSuccess, outbox.PublishOutcomeFailed, outbox.PublishOutcomeTimeout, outbox.PublishOutcomeCanceled:
		return string(outcome)
	default:
		return "failed"
	}
}

func normalizeFailureReason(reason string) string {
	switch reason {
	case "publish_failed", "publish_timeout", "publish_canceled":
		return reason
	default:
		return "unknown"
	}
}

func normalizeOperation(operation outbox.Operation) string {
	switch operation {
	case outbox.OperationClaim,
		outbox.OperationMarkPublished,
		outbox.OperationScheduleRetry,
		outbox.OperationMarkDeadLetter,
		outbox.OperationCalculateBackoff:
		return string(operation)
	default:
		return "unknown"
	}
}
