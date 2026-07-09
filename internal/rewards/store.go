package rewards

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	rewardClaimsPlayerCampaignRewardConstraint = "reward_claims_player_campaign_reward_uniq"

	idempotencyStateProcessing = "processing"
	idempotencyStateCompleted  = "completed"
)

type PostgresStore struct {
	pool         *pgxpool.Pool
	queryTimeout time.Duration
}

func NewPostgresStore(pool *pgxpool.Pool, queryTimeout time.Duration) *PostgresStore {
	return &PostgresStore{
		pool:         pool,
		queryTimeout: queryTimeout,
	}
}

func (s *PostgresStore) CreateClaim(ctx context.Context, cmd CreateClaimStoreCommand) (CreateClaimResult, error) {
	if s == nil || s.pool == nil {
		return CreateClaimResult{}, ErrUnavailable
	}

	queryCtx := ctx
	cancel := func() {}
	if s.queryTimeout > 0 {
		queryCtx, cancel = context.WithTimeout(ctx, s.queryTimeout)
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

func (s *PostgresStore) InsertClaim(ctx context.Context, claim Claim) (Claim, error) {
	if s == nil || s.pool == nil {
		return Claim{}, ErrUnavailable
	}

	queryCtx := ctx
	cancel := func() {}
	if s.queryTimeout > 0 {
		queryCtx, cancel = context.WithTimeout(ctx, s.queryTimeout)
	}
	defer cancel()

	created, err := insertClaim(queryCtx, s.pool, claim)
	if err != nil {
		return Claim{}, err
	}

	return created, nil
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
	if err := tx.QueryRow(ctx, query, cmd.Operation, cmd.KeyHash).Scan(
		&requestHash,
		&state,
		&responseStatus,
		&responseBody,
	); err != nil {
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
		return fmt.Errorf("create reward claimed outbox event id: %v: %w", err, ErrInternal)
	}

	payload, err := json.Marshal(NewRewardClaimedEvent(eventID, claim))
	if err != nil {
		return fmt.Errorf("marshal reward claimed outbox payload: %v: %w", err, ErrInternal)
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

func completeIdempotencyKey(ctx context.Context, tx pgx.Tx, cmd CreateClaimStoreCommand, statusCode int, responseBody []byte, claimID string) error {
	const query = `
UPDATE idempotency_keys
SET state = $3,
    response_status = $4,
    response_body = $5,
    reward_claim_id = NULLIF($6, '')::uuid,
    completed_at = now(),
    updated_at = now()
WHERE operation = $1 AND key_hash = $2`

	tag, err := tx.Exec(
		ctx,
		query,
		cmd.Operation,
		cmd.KeyHash,
		idempotencyStateCompleted,
		statusCode,
		responseBody,
		claimID,
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

func insertClaim(ctx context.Context, db claimInserter, claim Claim) (Claim, error) {
	const query = `
INSERT INTO reward_claims (id, player_id, campaign_id, reward_id, status)
VALUES ($1, $2, $3, $4, $5)
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
		return Claim{}, mapPostgresError(err)
	}

	return created, nil
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

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" && pgErr.ConstraintName == rewardClaimsPlayerCampaignRewardConstraint {
			return ErrDuplicateClaim
		}

		return fmt.Errorf("postgres reward claim operation: %w", ErrInternal)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres reward claim operation returned no row: %w", ErrInternal)
	}

	return fmt.Errorf("postgres reward claim operation: %w", ErrInternal)
}
