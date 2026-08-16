package outbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	postgresSQLStateConnectionException          = "08000"
	postgresSQLStateUnableToEstablishConnection  = "08001"
	postgresSQLStateConnectionDoesNotExist       = "08003"
	postgresSQLStateServerRejectedConnection     = "08004"
	postgresSQLStateConnectionFailure            = "08006"
	postgresSQLStateTransactionResolutionUnknown = "08007"
	postgresSQLStateTooManyConnections           = "53300"
	postgresSQLStateAdminShutdown                = "57P01"
	postgresSQLStateCrashShutdown                = "57P02"
	postgresSQLStateCannotConnectNow             = "57P03"
)

var (
	ErrLeaseLost = errors.New("outbox event lease lost")

	errStoreUnavailable     = errors.New("outbox store unavailable")
	errStoreInternal        = errors.New("outbox store internal error")
	errPostgresQueryTimeout = errors.New("postgres outbox query timeout")
)

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
    o.status,
    o.attempts,
    o.available_at,
    o.created_at;
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
		&event.Status,
		&event.Attempts,
		&event.AvailableAt,
		&event.CreatedAt,
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
    last_error = NULL,
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

	queryCtx, cancel := s.queryContext(ctx)
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

	err := s.pool.QueryRow(
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

		return time.Time{}, fmt.Errorf("schedule outbox event retry: %w", mapPostgresError(queryCtx, err))
	}

	return nextAvailableAt, nil
}

func (s *postgresStore) MarkDeadLetter(
	ctx context.Context,
	workerID, eventID, lastError string,
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
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if isPostgresUnavailableSQLState(pgErr.Code) {
			return fmt.Errorf("postgres outbox operation: %w", errStoreUnavailable)
		}

		return fmt.Errorf("postgres outbox operation: %w", errStoreInternal)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres outbox operation returned no row: %w", errStoreInternal)
	}

	if ctx != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		if errors.Is(context.Cause(ctx), errPostgresQueryTimeout) {
			return fmt.Errorf("postgres outbox operation: %w", errStoreUnavailable)
		}

		return ctx.Err()
	}

	if errors.Is(err, pgconn.ErrConnClosed) || pgconn.Timeout(err) || isNetworkUnavailableError(err) {
		return fmt.Errorf("postgres outbox operation: %w", errStoreUnavailable)
	}

	return fmt.Errorf("postgres outbox operation: %w", errStoreInternal)
}

func isNetworkUnavailableError(err error) bool {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return true
	}

	var operationErr *net.OpError
	return errors.As(err, &operationErr)
}

func isPostgresUnavailableSQLState(code string) bool {
	switch code {
	case postgresSQLStateConnectionException,
		postgresSQLStateConnectionDoesNotExist,
		postgresSQLStateConnectionFailure,
		postgresSQLStateUnableToEstablishConnection,
		postgresSQLStateServerRejectedConnection,
		postgresSQLStateTransactionResolutionUnknown,
		postgresSQLStateTooManyConnections,
		postgresSQLStateAdminShutdown,
		postgresSQLStateCrashShutdown,
		postgresSQLStateCannotConnectNow:
		return true
	default:
		return false
	}
}
