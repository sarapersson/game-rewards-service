package rewards

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/sarapersson/game-rewards-service/internal/idempotency"
	"github.com/sarapersson/game-rewards-service/internal/postgres"
)

const (
	idempotencyStateProcessing = "processing"
	idempotencyStateCompleted  = "completed"
)

var errPostgresQueryTimeout = errors.New("postgres reward claim query timeout")

func (s *Service) createClaim(ctx context.Context, cmd createClaimParams) (CreateClaimResult, error) {
	queryCtx, cancel := s.queryContext(ctx)
	defer cancel()

	tx, err := s.pool.BeginTx(queryCtx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return CreateClaimResult{}, mapPostgresError(queryCtx, err)
	}
	defer s.rollbackTransaction(tx)

	reserved, err := tryReserveIdempotencyKey(queryCtx, tx, cmd)
	if err != nil {
		return CreateClaimResult{}, err
	}

	if !reserved {
		result, err := replayIdempotentClaim(queryCtx, tx, cmd)
		if err != nil {
			return CreateClaimResult{}, err
		}

		if err := tx.Commit(queryCtx); err != nil {
			return CreateClaimResult{}, mapPostgresError(queryCtx, err)
		}

		return result, nil
	}

	created, inserted, err := tryInsertClaimForIdempotentCreate(queryCtx, tx, cmd.Claim)
	if err != nil {
		return CreateClaimResult{}, err
	}
	if !inserted {
		result, err := completeDuplicateClaim(queryCtx, tx, cmd)
		if err != nil {
			return CreateClaimResult{}, err
		}

		if err := tx.Commit(queryCtx); err != nil {
			return CreateClaimResult{}, mapPostgresError(queryCtx, err)
		}

		return result, nil
	}

	result, err := completeCreatedClaim(queryCtx, tx, cmd, created)
	if err != nil {
		return CreateClaimResult{}, err
	}

	if err := tx.Commit(queryCtx); err != nil {
		return CreateClaimResult{}, mapPostgresError(queryCtx, err)
	}

	return result, nil
}

func (s *Service) rollbackTransaction(tx pgx.Tx) {
	// Rollback must outlive a canceled request while remaining bounded.
	rollbackCtx, cancel := context.WithTimeout(context.Background(), s.queryTimeout)
	defer cancel()

	_ = tx.Rollback(rollbackCtx)
}

func (s *Service) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, s.queryTimeout, errPostgresQueryTimeout)
}

func tryReserveIdempotencyKey(ctx context.Context, tx pgx.Tx, cmd createClaimParams) (bool, error) {
	const query = `
INSERT INTO idempotency_keys (operation, key_hash, request_hash, state)
VALUES ($1, $2, $3, $4)
ON CONFLICT (operation, key_hash) DO NOTHING`

	tag, err := tx.Exec(
		ctx,
		query,
		idempotency.RewardClaimOperation,
		cmd.KeyHash[:],
		cmd.RequestHash[:],
		idempotencyStateProcessing,
	)
	if err != nil {
		return false, mapPostgresError(ctx, err)
	}

	return tag.RowsAffected() == 1, nil
}

func replayIdempotentClaim(ctx context.Context, tx pgx.Tx, cmd createClaimParams) (CreateClaimResult, error) {
	const query = `
SELECT request_hash, state, response_status, response_body, reward_claim_id::text
FROM idempotency_keys
WHERE operation = $1 AND key_hash = $2`

	var (
		requestHash    []byte
		state          string
		responseStatus sql.NullInt64
		responseBody   []byte
		rewardClaimID  sql.NullString
	)

	err := tx.QueryRow(ctx, query, idempotency.RewardClaimOperation, cmd.KeyHash[:]).Scan(
		&requestHash,
		&state,
		&responseStatus,
		&responseBody,
		&rewardClaimID,
	)
	if err != nil {
		return CreateClaimResult{}, mapPostgresError(ctx, err)
	}

	if state != idempotencyStateCompleted {
		return CreateClaimResult{}, fmt.Errorf("unexpected committed idempotency state %q: %w", state, ErrInternal)
	}

	if !bytes.Equal(requestHash, cmd.RequestHash[:]) {
		return CreateClaimResult{}, ErrIdempotencyKeyReused
	}

	if !responseStatus.Valid || len(responseBody) == 0 {
		return CreateClaimResult{}, fmt.Errorf("completed idempotency key missing stored response: %w", ErrInternal)
	}

	result := CreateClaimResult{
		StatusCode:   int(responseStatus.Int64),
		ResponseBody: responseBody,
		Replayed:     true,
	}
	if err := validateStoredCreateClaimResponse(
		result.StatusCode,
		result.ResponseBody,
		cmd.Claim,
		rewardClaimID.String,
	); err != nil {
		return CreateClaimResult{}, err
	}

	return result, nil
}

func completeCreatedClaim(ctx context.Context, tx pgx.Tx, cmd createClaimParams, claim claim) (CreateClaimResult, error) {
	body, err := marshalCreatedClaimResponse(claim)
	if err != nil {
		return CreateClaimResult{}, err
	}

	if err := insertRewardClaimedOutboxEvent(ctx, tx, claim); err != nil {
		return CreateClaimResult{}, err
	}

	if err := completeIdempotencyKey(ctx, tx, cmd, createClaimStatusCreated, body, claim.ID); err != nil {
		return CreateClaimResult{}, err
	}

	return CreateClaimResult{
		StatusCode:   createClaimStatusCreated,
		ResponseBody: body,
	}, nil
}

func insertRewardClaimedOutboxEvent(ctx context.Context, tx pgx.Tx, claim claim) error {
	eventID := newUUIDV4()

	payload, err := json.Marshal(newRewardClaimedEvent(eventID, claim))
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
		return mapPostgresError(ctx, err)
	}

	return nil
}

func completeDuplicateClaim(ctx context.Context, tx pgx.Tx, cmd createClaimParams) (CreateClaimResult, error) {
	body, err := marshalDuplicateClaimResponse()
	if err != nil {
		return CreateClaimResult{}, err
	}

	if err := completeIdempotencyKey(ctx, tx, cmd, createClaimStatusConflict, body, ""); err != nil {
		return CreateClaimResult{}, err
	}

	return CreateClaimResult{
		StatusCode:   createClaimStatusConflict,
		ResponseBody: body,
	}, nil
}

func completeIdempotencyKey(
	ctx context.Context,
	tx pgx.Tx,
	cmd createClaimParams,
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
		idempotency.RewardClaimOperation,
		cmd.KeyHash[:],
		idempotencyStateCompleted,
		statusCode,
		responseBody,
		claimID,
		cmd.RequestHash[:],
		idempotencyStateProcessing,
	)
	if err != nil {
		return mapPostgresError(ctx, err)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf("complete idempotency key affected %d rows: %w", tag.RowsAffected(), ErrInternal)
	}

	return nil
}

func tryInsertClaimForIdempotentCreate(ctx context.Context, tx pgx.Tx, candidate claimToCreate) (claim, bool, error) {
	const query = `
INSERT INTO reward_claims (id, player_id, campaign_id, reward_id, status)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (player_id, campaign_id, reward_id) DO NOTHING
RETURNING id::text, player_id, campaign_id, reward_id, created_at`

	var created claim

	err := tx.QueryRow(
		ctx,
		query,
		candidate.ID,
		candidate.PlayerID,
		candidate.CampaignID,
		candidate.RewardID,
		claimStatusClaimed,
	).Scan(
		&created.ID,
		&created.PlayerID,
		&created.CampaignID,
		&created.RewardID,
		&created.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return claim{}, false, nil
		}

		return claim{}, false, mapPostgresError(ctx, err)
	}

	return created, true, nil
}

func mapPostgresError(ctx context.Context, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres reward claim operation returned no row: %w", ErrInternal)
	}

	if ctx != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		if errors.Is(context.Cause(ctx), errPostgresQueryTimeout) {
			return fmt.Errorf("postgres reward claim operation: %w", ErrUnavailable)
		}

		return ctx.Err()
	}

	if postgres.IsUnavailable(err) {
		return fmt.Errorf("postgres reward claim operation: %w", ErrUnavailable)
	}

	return fmt.Errorf("postgres reward claim operation: %w", ErrInternal)
}
