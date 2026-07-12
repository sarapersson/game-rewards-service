package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"
)

const (
	maxWorkerIDLength = 128

	publishFailureFailed   = "publish_failed"
	publishFailureTimeout  = "publish_timeout"
	publishFailureCanceled = "publish_canceled"
)

type Worker struct {
	store          Store
	publisher      Publisher
	logger         *slog.Logger
	workerID       string
	pollInterval   time.Duration
	lockTTL        time.Duration
	publishTimeout time.Duration
	maxAttempts    int
	backoff        BackoffPolicy
	now            func() time.Time
}

type WorkerConfig struct {
	WorkerID       string
	PollInterval   time.Duration
	LockTTL        time.Duration
	PublishTimeout time.Duration
	MaxAttempts    int
	Backoff        BackoffPolicy
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

	if cfg.MaxAttempts <= 0 {
		return nil, fmt.Errorf("max attempts must be greater than zero")
	}

	if _, err := cfg.Backoff.Duration(0); err != nil {
		return nil, fmt.Errorf("invalid backoff policy: %w", err)
	}

	return &Worker{
		store:          store,
		publisher:      publisher,
		logger:         logger,
		workerID:       cfg.WorkerID,
		pollInterval:   cfg.PollInterval,
		lockTTL:        cfg.LockTTL,
		publishTimeout: cfg.PublishTimeout,
		maxAttempts:    cfg.MaxAttempts,
		backoff:        cfg.Backoff,
		now:            time.Now,
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
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

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-timer.C:
			processed, err := w.RunOnce(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}

				w.logger.ErrorContext(
					ctx,
					"outbox_worker_iteration_failed",
					slog.String("worker_id", w.workerID),
					slog.String("error", err.Error()),
				)
			}

			wait := w.pollInterval
			if processed > 0 {
				wait = 0
			}

			timer.Reset(wait)
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	event, claimed, err := w.store.ClaimNext(ctx, w.workerID, w.lockTTL)
	if err != nil {
		return 0, err
	}

	if !claimed {
		return 0, nil
	}

	w.logger.InfoContext(
		ctx,
		"outbox_event_claimed",
		slog.String("worker_id", w.workerID),
		slog.String("event_id", event.ID),
		slog.String("event_type", event.EventType),
	)

	if err := w.processEvent(ctx, event); err != nil {
		return 0, err
	}

	return 1, nil
}

func (w *Worker) processEvent(ctx context.Context, event Event) error {
	publishCtx, cancel := context.WithTimeout(ctx, w.publishTimeout)
	defer cancel()

	started := w.now()
	attemptNumber := event.Attempts + 1

	w.logger.InfoContext(
		ctx,
		"outbox_event_publish_started",
		slog.String("worker_id", w.workerID),
		slog.String("event_id", event.ID),
		slog.String("event_type", event.EventType),
		slog.String("aggregate_type", event.AggregateType),
		slog.String("aggregate_id", event.AggregateID),
		slog.Int("attempt_number", attemptNumber),
		slog.Int("failed_attempts", event.Attempts),
	)

	publishErr := w.publisher.Publish(publishCtx, event)
	duration := w.now().Sub(started)

	if publishErr == nil {
		if err := w.store.MarkPublished(ctx, w.workerID, event.ID); err != nil {
			return fmt.Errorf("mark published event %q: %w", event.ID, err)
		}

		w.logger.InfoContext(
			ctx,
			"outbox_event_published",
			slog.String("worker_id", w.workerID),
			slog.String("event_id", event.ID),
			slog.String("event_type", event.EventType),
			slog.Int("attempt_number", attemptNumber),
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

	failureReason := classifyPublishFailure(publishCtx, publishErr)
	failedAttempts := event.Attempts + 1

	if failedAttempts >= w.maxAttempts {
		if err := w.store.MarkDeadLetter(ctx, w.workerID, event.ID, failureReason); err != nil {
			return fmt.Errorf("mark event %q dead letter: %w", event.ID, err)
		}

		w.logger.WarnContext(
			ctx,
			"outbox_event_dead_lettered",
			slog.String("worker_id", w.workerID),
			slog.String("event_id", event.ID),
			slog.String("event_type", event.EventType),
			slog.Int("failed_attempts", failedAttempts),
			slog.String("failure_reason", failureReason),
			slog.Int64("duration_ms", duration.Milliseconds()),
		)

		return nil
	}

	retryDelay, err := w.backoff.Duration(event.Attempts)
	if err != nil {
		return fmt.Errorf("calculate retry delay for event %q: %w", event.ID, err)
	}

	nextAvailableAt, err := w.store.ScheduleRetry(
		ctx,
		w.workerID,
		event.ID,
		retryDelay,
		failureReason,
	)
	if err != nil {
		return fmt.Errorf("schedule retry for event %q: %w", event.ID, err)
	}

	w.logger.WarnContext(
		ctx,
		"outbox_event_retry_scheduled",
		slog.String("worker_id", w.workerID),
		slog.String("event_id", event.ID),
		slog.String("event_type", event.EventType),
		slog.Int("failed_attempts", failedAttempts),
		slog.Duration("retry_delay", retryDelay),
		slog.Time("next_available_at", nextAvailableAt),
		slog.String("failure_reason", failureReason),
		slog.Int64("duration_ms", duration.Milliseconds()),
	)

	return nil
}

func classifyPublishFailure(publishCtx context.Context, err error) string {
	switch {
	case errors.Is(publishCtx.Err(), context.DeadlineExceeded):
		return publishFailureTimeout
	case errors.Is(err, context.DeadlineExceeded):
		return publishFailureTimeout
	case errors.Is(publishCtx.Err(), context.Canceled):
		return publishFailureCanceled
	case errors.Is(err, context.Canceled):
		return publishFailureCanceled
	default:
		return publishFailureFailed
	}
}
