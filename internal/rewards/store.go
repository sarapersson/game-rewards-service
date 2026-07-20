package rewards

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
	rewardClaimsPlayerCampaignRewardConstraint = "reward_claims_player_campaign_reward_uniq"

	postgresSQLStateUniqueViolation              = "23505"
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

	idempotencyStateProcessing = "processing"
	idempotencyStateCompleted  = "completed"
)

type PostgresStore struct {
	pool         *pgxpool.Pool
	queryTimeout time.Duration
}

func NewPostgresStore(pool *pgxpool.Pool, queryTimeout time.Duration) *PostgresStore {
	return &PostgresStore{pool: pool, queryTimeout: queryTimeout}
}

func (s *PostgresStore) CreateClaim(ctx context.Context, cmd CreateClaimStoreCommand) (CreateClaimResult, error) {
	queryCtx, cancel, err := s.queryContext(ctx)
	if err != nil {
		return CreateClaimResult{}, err
	}
	defer cancel()

	tx, err := s.pool.BeginTx(queryCtx, pgx.TxOptions{})
	if err != nil {
		return CreateClaimResult{}, mapPostgresError(err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	inserted, err := insertProcessingIdempotencyKey(queryCtx, tx, cmd)
	if err != nil {
		return CreateClaimResult{}, err
	}

	if !inserted {
		result, err := replayIdempotentClaim(queryCtx, tx, cmd)
		if err != nil {
			return CreateClaimResult{}, err
		}

		if err := tx.Commit(queryCtx); err != nil {
			return CreateClaimResult{}, mapPostgresError(err)
		}

		return result, nil
	}

	created, err := insertClaimForIdempotentCreate(queryCtx, tx, cmd.Claim)
	if err != nil {
		if errors.Is(err, ErrDuplicateClaim) {
			result, completeErr := completeDuplicateClaim(queryCtx, tx, cmd)
			if completeErr != nil {
				return CreateClaimResult{}, completeErr
			}

			if err := tx.Commit(queryCtx); err != nil {
				return CreateClaimResult{}, mapPostgresError(err)
			}

			return result, nil
		}

		return CreateClaimResult{}, err
	}

	result, err := completeCreatedClaim(queryCtx, tx, cmd, created)
	if err != nil {
		return CreateClaimResult{}, err
	}

	if err := tx.Commit(queryCtx); err != nil {
		return CreateClaimResult{}, mapPostgresError(err)
	}

	return result, nil
}

func (s *PostgresStore) queryContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if s == nil || s.pool == nil {
		return nil, nil, ErrUnavailable
	}

	if s.queryTimeout <= 0 {
		return nil, nil, fmt.Errorf("reward claims store query timeout must be greater than zero: %w", ErrInternal)
	}

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	return queryCtx, cancel, nil
}

func insertProcessingIdempotencyKey(ctx context.Context, tx pgx.Tx, cmd CreateClaimStoreCommand) (bool, error) {
	const query = `
INSERT INTO idempotency_keys (operation, key_hash, request_hash, state)
VALUES ($1, $2, $3, $4)
ON CONFLICT (operation, key_hash) DO NOTHING
RETURNING state`

	var state string

	err := tx.QueryRow(
		ctx,
		query,
		cmd.Operation,
		cmd.KeyHash,
		cmd.RequestHash,
		idempotencyStateProcessing,
	).Scan(&state)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}

		return false, mapPostgresError(err)
	}

	return true, nil
}

func replayIdempotentClaim(ctx context.Context, tx pgx.Tx, cmd CreateClaimStoreCommand) (CreateClaimResult, error) {
	const query = `
SELECT request_hash, state, response_status, response_body
FROM idempotency_keys
WHERE operation = $1 AND key_hash = $2
FOR UPDATE`

	var (
		requestHash    []byte
		state          string
		responseStatus sql.NullInt64
		responseBody   []byte
	)

	err := tx.QueryRow(ctx, query, cmd.Operation, cmd.KeyHash).Scan(
		&requestHash,
		&state,
		&responseStatus,
		&responseBody,
	)
	if err != nil {
		return CreateClaimResult{}, mapPostgresError(err)
	}

	if !bytes.Equal(requestHash, cmd.RequestHash) {
		return CreateClaimResult{}, ErrIdempotencyKeyReused
	}

	if state == idempotencyStateProcessing {
		return CreateClaimResult{}, ErrIdempotencyInProgress
	}

	if state != idempotencyStateCompleted {
		return CreateClaimResult{}, fmt.Errorf("unexpected idempotency state %q: %w", state, ErrInternal)
	}

	if !responseStatus.Valid || len(responseBody) == 0 {
		return CreateClaimResult{}, fmt.Errorf("completed idempotency key missing stored response: %w", ErrInternal)
	}

	return CreateClaimResult{
		StatusCode:   int(responseStatus.Int64),
		ResponseBody: responseBody,
		Replayed:     true,
	}, nil
}

func completeCreatedClaim(ctx context.Context, tx pgx.Tx, cmd CreateClaimStoreCommand, claim Claim) (CreateClaimResult, error) {
	body, err := MarshalCreatedClaimResponse(claim)
	if err != nil {
		return CreateClaimResult{}, err
	}

	if err := insertRewardClaimedOutboxEvent(ctx, tx, claim); err != nil {
		return CreateClaimResult{}, err
	}

	if err := completeIdempotencyKey(ctx, tx, cmd, CreateClaimStatusCreated, body, claim.ID); err != nil {
		return CreateClaimResult{}, err
	}

	return CreateClaimResult{
		StatusCode:   CreateClaimStatusCreated,
		ResponseBody: body,
	}, nil
}

func insertRewardClaimedOutboxEvent(ctx context.Context, tx pgx.Tx, claim Claim) error {
	eventID, err := NewUUIDV4()
	if err != nil {
		return fmt.Errorf("create reward claimed outbox event id: %w", ErrInternal)
	}

	payload, err := json.Marshal(NewRewardClaimedEvent(eventID, claim))
	if err != nil {
		return fmt.Errorf("marshal reward claimed outbox payload: %w", ErrInternal)
	}

	const query = `
INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, status)
VALUES ($1, $2, $3, $4, $5::jsonb, $6)`

	_, err = tx.Exec(
		ctx,
		query,
		eventID,
		outboxAggregateTypeRewardClaim,
		claim.ID,
		outboxEventTypeRewardClaimed,
		string(payload),
		outboxStatusPending,
	)
	if err != nil {
		return mapPostgresError(err)
	}

	return nil
}

func completeDuplicateClaim(ctx context.Context, tx pgx.Tx, cmd CreateClaimStoreCommand) (CreateClaimResult, error) {
	body, err := MarshalDuplicateClaimResponse()
	if err != nil {
		return CreateClaimResult{}, err
	}

	if err := completeIdempotencyKey(ctx, tx, cmd, CreateClaimStatusConflict, body, ""); err != nil {
		return CreateClaimResult{}, err
	}

	return CreateClaimResult{
		StatusCode:   CreateClaimStatusConflict,
		ResponseBody: body,
	}, nil
}

func completeIdempotencyKey(
	ctx context.Context,
	tx pgx.Tx,
	cmd CreateClaimStoreCommand,
	statusCode int,
	responseBody []byte,
	claimID string,
) error {
	const query = `
UPDATE idempotency_keys
SET state = $3,
    response_status = $4,
    response_body = $5,
    reward_claim_id = NULLIF($6, '')::uuid,
    completed_at = now(),
    updated_at = now()
WHERE operation = $1
  AND key_hash = $2
  AND request_hash = $7
  AND state = $8`

	tag, err := tx.Exec(
		ctx,
		query,
		cmd.Operation,
		cmd.KeyHash,
		idempotencyStateCompleted,
		statusCode,
		responseBody,
		claimID,
		cmd.RequestHash,
		idempotencyStateProcessing,
	)
	if err != nil {
		return mapPostgresError(err)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf("complete idempotency key affected %d rows: %w", tag.RowsAffected(), ErrInternal)
	}

	return nil
}

type claimInserter interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func insertClaimForIdempotentCreate(ctx context.Context, db claimInserter, claim Claim) (Claim, error) {
	const query = `
INSERT INTO reward_claims (id, player_id, campaign_id, reward_id, status)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (player_id, campaign_id, reward_id) DO NOTHING
RETURNING id::text, player_id, campaign_id, reward_id, status, created_at, updated_at`

	var created Claim

	err := db.QueryRow(
		ctx,
		query,
		claim.ID,
		claim.PlayerID,
		claim.CampaignID,
		claim.RewardID,
		claim.Status,
	).Scan(
		&created.ID,
		&created.PlayerID,
		&created.CampaignID,
		&created.RewardID,
		&created.Status,
		&created.CreatedAt,
		&created.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Claim{}, ErrDuplicateClaim
		}

		return Claim{}, mapPostgresError(err)
	}

	return created, nil
}

func mapPostgresError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("postgres reward claim operation: %w", ErrUnavailable)
	}

	if errors.Is(err, pgconn.ErrConnClosed) || pgconn.Timeout(err) || isNetworkUnavailableError(err) {
		return fmt.Errorf("postgres reward claim operation: %w", ErrUnavailable)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == postgresSQLStateUniqueViolation && pgErr.ConstraintName == rewardClaimsPlayerCampaignRewardConstraint {
			return ErrDuplicateClaim
		}

		if isPostgresUnavailableSQLState(pgErr.Code) {
			return fmt.Errorf("postgres reward claim operation: %w", ErrUnavailable)
		}

		return fmt.Errorf("postgres reward claim operation: %w", ErrInternal)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres reward claim operation returned no row: %w", ErrInternal)
	}

	return fmt.Errorf("postgres reward claim operation: %w", ErrInternal)
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
