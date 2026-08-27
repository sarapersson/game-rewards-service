package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"
)

// maxWorkerIDLength must match the outbox_events.locked_by length constraint.
const maxWorkerIDLength = 128

var errWorkerFatal = errors.New("outbox worker internal failure")

type workerOperationError struct {
	operation Operation
	err       error
}

func (e *workerOperationError) Error() string {
	return fmt.Sprintf("outbox %s operation failed", e.operation)
}

func (e *workerOperationError) Unwrap() error {
	return e.err
}

func wrapWorkerOperationError(operation Operation, err error) error {
	return &workerOperationError{operation: operation, err: err}
}

func workerErrorOperation(err error) string {
	if operationErr, ok := errors.AsType[*workerOperationError](err); ok {
		return string(operationErr.operation)
	}

	return "unknown"
}

type Worker struct {
	store          Store
	publisher      Publisher
	logger         *slog.Logger
	workerID       string
	pollInterval   time.Duration
	lockTTL        time.Duration
	publishTimeout time.Duration
	maxFailures    int
	backoff        backoffPolicy
	observer       Observer
}

type WorkerConfig struct {
	WorkerID       string
	PollInterval   time.Duration
	LockTTL        time.Duration
	PublishTimeout time.Duration
	MaxFailures    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	Observer       Observer
}

func NewWorker(store Store, publisher Publisher, logger *slog.Logger, cfg WorkerConfig) (*Worker, error) {
	if store == nil {
		return nil, fmt.Errorf("store must not be nil")
	}

	if publisher == nil {
		return nil, fmt.Errorf("publisher must not be nil")
	}

	if logger == nil {
		return nil, fmt.Errorf("logger must not be nil")
	}

	if cfg.WorkerID == "" {
		return nil, fmt.Errorf("worker id must not be empty")
	}

	if utf8.RuneCountInString(cfg.WorkerID) > maxWorkerIDLength {
		return nil, fmt.Errorf("worker id must be at most %d characters", maxWorkerIDLength)
	}

	if cfg.PollInterval <= 0 {
		return nil, fmt.Errorf("poll interval must be greater than zero")
	}

	if cfg.LockTTL <= 0 {
		return nil, fmt.Errorf("lock ttl must be greater than zero")
	}

	if cfg.PublishTimeout <= 0 {
		return nil, fmt.Errorf("publish timeout must be greater than zero")
	}

	if cfg.LockTTL <= cfg.PublishTimeout {
		return nil, fmt.Errorf("lock ttl must be greater than publish timeout")
	}

	if cfg.MaxFailures <= 0 {
		return nil, fmt.Errorf("max failures must be greater than zero")
	}

	backoff, err := newBackoffPolicy(cfg.BaseBackoff, cfg.MaxBackoff)
	if err != nil {
		return nil, fmt.Errorf("invalid backoff policy: %w", err)
	}

	observer := cfg.Observer
	if observer == nil {
		observer = noopObserver{}
	}

	return &Worker{
		store:          store,
		publisher:      publisher,
		logger:         logger,
		workerID:       cfg.WorkerID,
		pollInterval:   cfg.PollInterval,
		lockTTL:        cfg.LockTTL,
		publishTimeout: cfg.PublishTimeout,
		maxFailures:    cfg.MaxFailures,
		backoff:        backoff,
		observer:       observer,
	}, nil
}

// Run processes outbox events until ctx is canceled.
//
// onStarted is called once, after the polling loop has been initialized and
// immediately before the worker begins waiting for work. It may be nil.
func (w *Worker) Run(ctx context.Context, onStarted func()) error {
	if ctx.Err() != nil {
		return nil
	}

	w.logger.InfoContext(
		ctx,
		"outbox_worker_started",
		slog.String("worker_id", w.workerID),
	)

	defer w.logger.InfoContext(
		context.Background(),
		"outbox_worker_stopped",
		slog.String("worker_id", w.workerID),
	)

	timer := time.NewTimer(0)
	defer timer.Stop()

	if ctx.Err() != nil {
		return nil
	}

	if onStarted != nil {
		onStarted()
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-timer.C:
			processed, err := w.runOnce(ctx)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
					return nil
				}

				operation := workerErrorOperation(err)
				if errors.Is(err, errStoreUnavailable) {
					w.logger.ErrorContext(
						ctx,
						"outbox_worker_iteration_failed",
						slog.String("worker_id", w.workerID),
						slog.String("operation", operation),
						slog.String("error_class", "store_unavailable"),
						slog.String("action", "retry"),
					)
				} else {
					w.logger.ErrorContext(
						ctx,
						"outbox_worker_iteration_failed",
						slog.String("worker_id", w.workerID),
						slog.String("operation", operation),
						slog.String("error_class", "store_internal"),
						slog.String("action", "stop"),
					)

					return fmt.Errorf(
						"outbox worker %s operation failed: %w",
						operation,
						errWorkerFatal,
					)
				}
			}

			wait := w.pollInterval
			if processed {
				wait = 0
			}

			timer.Reset(wait)
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) (bool, error) {
	event, claimed, err := w.store.ClaimNext(ctx, w.workerID, w.lockTTL)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return false, ctxErr
		}

		w.observer.ObserveClaim(ClaimOutcomeError)
		w.observer.ObserveOperationError(OperationClaim)
		return false, wrapWorkerOperationError(OperationClaim, err)
	}

	if !claimed {
		w.observer.ObserveClaim(ClaimOutcomeEmpty)
		return false, nil
	}

	w.observer.ObserveClaim(ClaimOutcomeClaimed)

	w.logger.InfoContext(
		ctx,
		"outbox_event_claimed",
		slog.String("worker_id", w.workerID),
		slog.String("event_id", event.ID),
		slog.String("event_type", event.EventType),
	)

	if err := w.processEvent(ctx, event); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func (w *Worker) processEvent(ctx context.Context, event Event) error {
	publishCtx, cancel := context.WithTimeout(ctx, w.publishTimeout)
	defer cancel()

	started := time.Now()

	w.logger.InfoContext(
		ctx,
		"outbox_event_publish_started",
		slog.String("worker_id", w.workerID),
		slog.String("event_id", event.ID),
		slog.String("event_type", event.EventType),
		slog.String("aggregate_type", event.AggregateType),
		slog.String("aggregate_id", event.AggregateID),
		slog.Int("failed_attempts", event.FailedAttempts),
	)

	publishErr := w.publisher.Publish(publishCtx, event)
	duration := time.Since(started)
	publishOutcome := classifyPublishOutcome(publishCtx, publishErr)
	w.observer.ObservePublish(event.EventType, publishOutcome, duration)

	if publishErr == nil {
		if err := w.store.MarkPublished(ctx, w.workerID, event.ID); err != nil {
			err = w.handleTransitionError(ctx, event, OperationMarkPublished, err)
			return fmt.Errorf("mark published event %q: %w", event.ID, err)
		}

		w.observer.ObservePublished(event.EventType)

		w.logger.InfoContext(
			ctx,
			"outbox_event_published",
			slog.String("worker_id", w.workerID),
			slog.String("event_id", event.ID),
			slog.String("event_type", event.EventType),
			slog.Int("failed_attempts", event.FailedAttempts),
			slog.Int64("duration_ms", duration.Milliseconds()),
		)

		return nil
	}

	// A process shutdown should not be recorded as a publisher failure.
	// The event remains leased and becomes eligible for recovery after the
	// lease expires.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	failedAttempts := event.FailedAttempts + 1

	if failedAttempts >= w.maxFailures {
		if err := w.store.MarkDeadLetter(ctx, w.workerID, event.ID, publishOutcome); err != nil {
			err = w.handleTransitionError(ctx, event, OperationMarkDeadLetter, err)
			return fmt.Errorf("mark event %q dead letter: %w", event.ID, err)
		}

		w.observer.ObserveDeadLetter(event.EventType, publishOutcome)

		w.logger.WarnContext(
			ctx,
			"outbox_event_dead_lettered",
			slog.String("worker_id", w.workerID),
			slog.String("event_id", event.ID),
			slog.String("event_type", event.EventType),
			slog.Int("failed_attempts", failedAttempts),
			slog.String("failure_reason", string(publishOutcome)),
			slog.Int64("duration_ms", duration.Milliseconds()),
		)

		return nil
	}

	retryDelay := w.backoff.retryDelay(failedAttempts)

	err := w.store.ScheduleRetry(
		ctx,
		w.workerID,
		event.ID,
		retryDelay,
		publishOutcome,
	)
	if err != nil {
		err = w.handleTransitionError(ctx, event, OperationScheduleRetry, err)
		return fmt.Errorf("schedule retry for event %q: %w", event.ID, err)
	}

	w.observer.ObserveRetry(event.EventType, publishOutcome)

	w.logger.WarnContext(
		ctx,
		"outbox_event_retry_scheduled",
		slog.String("worker_id", w.workerID),
		slog.String("event_id", event.ID),
		slog.String("event_type", event.EventType),
		slog.Int("failed_attempts", failedAttempts),
		slog.Duration("retry_delay", retryDelay),
		slog.String("failure_reason", string(publishOutcome)),
		slog.Int64("duration_ms", duration.Milliseconds()),
	)

	return nil
}

func classifyPublishOutcome(publishCtx context.Context, err error) PublishOutcome {
	if err == nil {
		return PublishOutcomeSuccess
	}

	switch {
	case errors.Is(publishCtx.Err(), context.DeadlineExceeded):
		return PublishOutcomeTimeout
	case errors.Is(err, context.DeadlineExceeded):
		return PublishOutcomeTimeout
	case errors.Is(publishCtx.Err(), context.Canceled):
		return PublishOutcomeCanceled
	case errors.Is(err, context.Canceled):
		return PublishOutcomeCanceled
	default:
		return PublishOutcomeFailed
	}
}

func (w *Worker) handleTransitionError(
	ctx context.Context,
	event Event,
	operation Operation,
	err error,
) error {
	if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
		return ctxErr
	}

	if errors.Is(err, ErrLeaseLost) {
		w.observer.ObserveLeaseLoss(event.EventType, operation)

		w.logger.WarnContext(
			ctx,
			"outbox_event_lease_lost",
			slog.String("worker_id", w.workerID),
			slog.String("event_id", event.ID),
			slog.String("event_type", event.EventType),
			slog.String("operation", string(operation)),
		)

		return ErrLeaseLost
	}

	w.observer.ObserveOperationError(operation)

	return wrapWorkerOperationError(operation, err)
}
