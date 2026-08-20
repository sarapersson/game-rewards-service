package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sarapersson/game-rewards-service/internal/postgres"
)

var (
	// ErrLeaseLost indicates that the worker no longer owns the event's processing
	// lease for the requested state transition.
	ErrLeaseLost = errors.New("outbox event lease lost")

	errStoreUnavailable     = errors.New("outbox store unavailable")
	errStoreInternal        = errors.New("outbox store internal error")
	errPostgresQueryTimeout = errors.New("postgres outbox query timeout")
)

// Store persists outbox leasing and ownership-fenced final state transitions.
type Store interface {
	ClaimNext(ctx context.Context, workerID string, lockTTL time.Duration) (Event, bool, error)
	MarkPublished(ctx context.Context, workerID, eventID string) error
	ScheduleRetry(
		ctx context.Context,
		workerID, eventID string,
		retryDelay time.Duration,
		outcome PublishOutcome,
	) error
	MarkDeadLetter(ctx context.Context, workerID, eventID string, outcome PublishOutcome) error
}

type postgresStore struct {
	pool         *pgxpool.Pool
	queryTimeout time.Duration
}

func NewPostgresStore(pool *pgxpool.Pool, queryTimeout time.Duration) (Store, error) {
	if pool == nil {
		return nil, errors.New("postgres outbox pool is required")
	}
	if queryTimeout <= 0 {
		return nil, errors.New("postgres outbox query timeout must be greater than zero")
	}

	return &postgresStore{pool: pool, queryTimeout: queryTimeout}, nil
}

func (s *postgresStore) ClaimNext(
	ctx context.Context,
	workerID string,
	lockTTL time.Duration,
) (Event, bool, error) {
	if workerID == "" {
		return Event{}, false, fmt.Errorf("worker id must not be empty")
	}

	if lockTTL <= 0 {
		return Event{}, false, fmt.Errorf("lock ttl must be greater than zero")
	}

	queryCtx, cancel := s.queryContext(ctx)
	defer cancel()

	const query = `
WITH due_event AS (
    SELECT id
    FROM outbox_events
    WHERE (
        status = 'pending'
        AND available_at <= now()
    )
    OR (
        status = 'processing'
        AND locked_until <= now()
    )
    ORDER BY available_at, id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox_events AS o
SET status = 'processing',
    locked_by = $1,
    locked_until = now() + make_interval(secs => $2::double precision),
    updated_at = now()
FROM due_event
WHERE o.id = due_event.id
RETURNING
    o.id::text,
    o.aggregate_type,
    o.aggregate_id::text,
    o.event_type,
    o.payload,
    o.failed_attempts;
`

	var (
		event   Event
		payload []byte
	)

	err := s.pool.QueryRow(
		queryCtx,
		query,
		workerID,
		lockTTL.Seconds(),
	).Scan(
		&event.ID,
		&event.AggregateType,
		&event.AggregateID,
		&event.EventType,
		&payload,
		&event.FailedAttempts,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Event{}, false, nil
		}

		return Event{}, false, fmt.Errorf("claim next outbox event: %w", mapPostgresError(queryCtx, err))
	}

	event.Payload = payload

	return event, true, nil
}

func (s *postgresStore) MarkPublished(ctx context.Context, workerID, eventID string) error {
	if workerID == "" {
		return fmt.Errorf("worker id must not be empty")
	}

	if eventID == "" {
		return fmt.Errorf("event id must not be empty")
	}

	queryCtx, cancel := s.queryContext(ctx)
	defer cancel()

	const query = `
UPDATE outbox_events
SET status = 'published',
    published_at = now(),
    locked_by = NULL,
    locked_until = NULL,
    updated_at = now()
WHERE id = $1
  AND status = 'processing'
  AND locked_by = $2;
`

	result, err := s.pool.Exec(queryCtx, query, eventID, workerID)
	if err != nil {
		return fmt.Errorf("mark outbox event published: %w", mapPostgresError(queryCtx, err))
	}

	if result.RowsAffected() != 1 {
		return fmt.Errorf("mark outbox event published: %w", ErrLeaseLost)
	}

	return nil
}

func (s *postgresStore) ScheduleRetry(
	ctx context.Context,
	workerID, eventID string,
	retryDelay time.Duration,
	outcome PublishOutcome,
) error {
	if workerID == "" {
		return fmt.Errorf("worker id must not be empty")
	}

	if eventID == "" {
		return fmt.Errorf("event id must not be empty")
	}

	if retryDelay <= 0 {
		return fmt.Errorf("retry delay must be greater than zero")
	}

	queryCtx, cancel := s.queryContext(ctx)
	defer cancel()

	const query = `
UPDATE outbox_events
SET status = 'pending',
    failed_attempts = failed_attempts + 1,
    available_at = now() + make_interval(secs => $3::double precision),
    locked_by = NULL,
    locked_until = NULL,
    last_failure_reason = $4,
    updated_at = now()
WHERE id = $1
  AND status = 'processing'
  AND locked_by = $2;
`

	result, err := s.pool.Exec(
		queryCtx,
		query,
		eventID,
		workerID,
		retryDelay.Seconds(),
		string(outcome),
	)
	if err != nil {
		return fmt.Errorf("schedule outbox event retry: %w", mapPostgresError(queryCtx, err))
	}

	if result.RowsAffected() != 1 {
		return fmt.Errorf("schedule outbox event retry: %w", ErrLeaseLost)
	}

	return nil
}

func (s *postgresStore) MarkDeadLetter(
	ctx context.Context,
	workerID, eventID string,
	outcome PublishOutcome,
) error {
	if workerID == "" {
		return fmt.Errorf("worker id must not be empty")
	}

	if eventID == "" {
		return fmt.Errorf("event id must not be empty")
	}

	queryCtx, cancel := s.queryContext(ctx)
	defer cancel()

	const query = `
UPDATE outbox_events
SET status = 'dead_letter',
    failed_attempts = failed_attempts + 1,
    dead_lettered_at = now(),
    locked_by = NULL,
    locked_until = NULL,
    last_failure_reason = $3,
    updated_at = now()
WHERE id = $1
  AND status = 'processing'
  AND locked_by = $2;
`

	result, err := s.pool.Exec(queryCtx, query, eventID, workerID, string(outcome))
	if err != nil {
		return fmt.Errorf("mark outbox event dead letter: %w", mapPostgresError(queryCtx, err))
	}

	if result.RowsAffected() != 1 {
		return fmt.Errorf("mark outbox event dead letter: %w", ErrLeaseLost)
	}

	return nil
}

func (s *postgresStore) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, s.queryTimeout, errPostgresQueryTimeout)
}

func mapPostgresError(ctx context.Context, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres outbox operation returned no row: %w", errStoreInternal)
	}

	if ctx != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		if errors.Is(context.Cause(ctx), errPostgresQueryTimeout) {
			return fmt.Errorf("postgres outbox operation: %w", errStoreUnavailable)
		}

		return ctx.Err()
	}

	if postgres.IsUnavailable(err) {
		return fmt.Errorf("postgres outbox operation: %w", errStoreUnavailable)
	}

	return fmt.Errorf("postgres outbox operation: %w", errStoreInternal)
}
