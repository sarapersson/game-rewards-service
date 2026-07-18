package outbox

import "time"

type ClaimOutcome string

const (
	ClaimOutcomeClaimed ClaimOutcome = "claimed"
	ClaimOutcomeEmpty   ClaimOutcome = "empty"
	ClaimOutcomeError   ClaimOutcome = "error"
)

type PublishOutcome string

const (
	PublishOutcomeSuccess  PublishOutcome = "success"
	PublishOutcomeFailed   PublishOutcome = "failed"
	PublishOutcomeTimeout  PublishOutcome = "timeout"
	PublishOutcomeCanceled PublishOutcome = "canceled"
)

type Operation string

const (
	OperationClaim            Operation = "claim"
	OperationMarkPublished    Operation = "mark_published"
	OperationScheduleRetry    Operation = "schedule_retry"
	OperationMarkDeadLetter   Operation = "mark_dead_letter"
	OperationCalculateBackoff Operation = "calculate_backoff"
)

// Observer receives bounded worker outcomes without event identifiers or raw errors.
type Observer interface {
	ObserveClaim(ClaimOutcome)
	ObservePublish(eventType string, outcome PublishOutcome, duration time.Duration)
	ObservePublished(eventType string)
	ObserveRetry(eventType, failureReason string)
	ObserveDeadLetter(eventType, failureReason string)
	ObserveLeaseLoss(eventType string, operation Operation)
	ObserveOperationError(operation Operation)
}

type noopObserver struct{}

func (noopObserver) ObserveClaim(ClaimOutcome)                            {}
func (noopObserver) ObservePublish(string, PublishOutcome, time.Duration) {}
func (noopObserver) ObservePublished(string)                              {}
func (noopObserver) ObserveRetry(string, string)                          {}
func (noopObserver) ObserveDeadLetter(string, string)                     {}
func (noopObserver) ObserveLeaseLoss(string, Operation)                   {}
func (noopObserver) ObserveOperationError(Operation)                      {}
