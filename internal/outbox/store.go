package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrLeaseLost = errors.New("outbox event lease lost")

type Store interface {
	ClaimNext(ctx context.Context, workerID string, lockTTL time.Duration) (Event, bool, error)
	MarkPublished(ctx context.Context, workerID, eventID string) error
	ScheduleRetry(
		ctx context.Context,
		workerID, eventID string,
		retryDelay time.Duration,
		lastError string,
	) (time.Time, error)
	MarkDeadLetter(ctx context.Context, workerID, eventID, lastError string) error
}

type PostgresStore struct {
	pool         *pgxpool.Pool
	queryTimeout time.Duration
}

func NewPostgresStore(pool *pgxpool.Pool, queryTimeout time.Duration) *PostgresStore {
	return &PostgresStore{pool: pool, queryTimeout: queryTimeout}
}

func (s *PostgresStore) ClaimNext(
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

	queryCtx, cancel, err := s.queryContext(ctx)
	if err != nil {
		return Event{}, false, err
	}
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
    o.status,
    o.attempts,
    o.available_at,
    o.created_at;
`

	var (
		event   Event
		payload []byte
	)

	err = s.pool.QueryRow(
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
		&event.Status,
		&event.Attempts,
		&event.AvailableAt,
		&event.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Event{}, false, nil
		}

		return Event{}, false, fmt.Errorf("claim next outbox event: %w", err)
	}

	event.Payload = payload

	return event, true, nil
}

func (s *PostgresStore) MarkPublished(ctx context.Context, workerID, eventID string) error {
	if workerID == "" {
		return fmt.Errorf("worker id must not be empty")
	}

	if eventID == "" {
		return fmt.Errorf("event id must not be empty")
	}

	queryCtx, cancel, err := s.queryContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	const query = `
UPDATE outbox_events
SET status = 'published',
    published_at = now(),
    locked_by = NULL,
    locked_until = NULL,
    last_error = NULL,
    updated_at = now()
WHERE id = $1
  AND status = 'processing'
  AND locked_by = $2;
`

	result, err := s.pool.Exec(queryCtx, query, eventID, workerID)
	if err != nil {
		return fmt.Errorf("mark outbox event published: %w", err)
	}

	if result.RowsAffected() != 1 {
		return fmt.Errorf("mark outbox event published: %w", ErrLeaseLost)
	}

	return nil
}

func (s *PostgresStore) ScheduleRetry(
	ctx context.Context,
	workerID, eventID string,
	retryDelay time.Duration,
	lastError string,
) (time.Time, error) {
	if workerID == "" {
		return time.Time{}, fmt.Errorf("worker id must not be empty")
	}

	if eventID == "" {
		return time.Time{}, fmt.Errorf("event id must not be empty")
	}

	if retryDelay <= 0 {
		return time.Time{}, fmt.Errorf("retry delay must be greater than zero")
	}

	queryCtx, cancel, err := s.queryContext(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer cancel()

	const query = `
UPDATE outbox_events
SET status = 'pending',
    attempts = attempts + 1,
    available_at = now() + make_interval(secs => $3::double precision),
    locked_by = NULL,
    locked_until = NULL,
    last_error = $4,
    updated_at = now()
WHERE id = $1
  AND status = 'processing'
  AND locked_by = $2
RETURNING available_at;
`

	var nextAvailableAt time.Time

	err = s.pool.QueryRow(
		queryCtx,
		query,
		eventID,
		workerID,
		retryDelay.Seconds(),
		lastError,
	).Scan(&nextAvailableAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, fmt.Errorf("schedule outbox event retry: %w", ErrLeaseLost)
		}

		return time.Time{}, fmt.Errorf("schedule outbox event retry: %w", err)
	}

	return nextAvailableAt, nil
}

func (s *PostgresStore) MarkDeadLetter(
	ctx context.Context,
	workerID, eventID, lastError string,
) error {
	if workerID == "" {
		return fmt.Errorf("worker id must not be empty")
	}

	if eventID == "" {
		return fmt.Errorf("event id must not be empty")
	}

	queryCtx, cancel, err := s.queryContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	const query = `
UPDATE outbox_events
SET status = 'dead_letter',
    attempts = attempts + 1,
    dead_lettered_at = now(),
    locked_by = NULL,
    locked_until = NULL,
    last_error = $3,
    updated_at = now()
WHERE id = $1
  AND status = 'processing'
  AND locked_by = $2;
`

	result, err := s.pool.Exec(queryCtx, query, eventID, workerID, lastError)
	if err != nil {
		return fmt.Errorf("mark outbox event dead letter: %w", err)
	}

	if result.RowsAffected() != 1 {
		return fmt.Errorf("mark outbox event dead letter: %w", ErrLeaseLost)
	}

	return nil
}

func (s *PostgresStore) queryContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if s == nil || s.pool == nil {
		return nil, nil, fmt.Errorf("outbox store is not initialized")
	}

	if s.queryTimeout <= 0 {
		return nil, nil, fmt.Errorf("outbox store query timeout must be greater than zero")
	}

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)

	return queryCtx, cancel, nil
}
