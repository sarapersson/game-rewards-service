package rewards

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/sarapersson/game-rewards-service/internal/postgres"
)

var errPostgresQueryTimeout = errors.New("postgres reward claim query timeout")

func (s *Service) createClaim(ctx context.Context, params createClaimParams) (CreateClaimResult, error) {
	queryCtx, cancel := s.queryContext(ctx)
	defer cancel()

	tx, err := s.pool.BeginTx(queryCtx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return CreateClaimResult{}, mapPostgresError(queryCtx, err)
	}
	defer s.rollbackTransaction(tx)

	for {
		reserved, err := tryReserveIdempotencyKey(queryCtx, tx, params)
		if err != nil {
			return CreateClaimResult{}, err
		}
		if reserved {
			break
		}

		result, found, err := tryLoadIdempotentClaimResult(queryCtx, tx, params)
		if err != nil {
			return CreateClaimResult{}, err
		}
		if !found {
			// Routine retention can delete a completed row after the reservation
			// insert observes a conflict but before replay reads it. Retry the
			// reservation so the request is evaluated against current state.
			continue
		}

		if err := tx.Commit(queryCtx); err != nil {
			return CreateClaimResult{}, mapPostgresError(queryCtx, err)
		}

		return result, nil
	}

	created, inserted, err := tryInsertClaim(queryCtx, tx, params)
	if err != nil {
		return CreateClaimResult{}, err
	}
	if !inserted {
		result, err := completeDuplicateClaim(queryCtx, tx, params)
		if err != nil {
			return CreateClaimResult{}, err
		}

		if err := tx.Commit(queryCtx); err != nil {
			return CreateClaimResult{}, mapPostgresError(queryCtx, err)
		}

		return result, nil
	}

	result, err := completeCreatedClaim(queryCtx, tx, params, created)
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

func tryReserveIdempotencyKey(ctx context.Context, tx pgx.Tx, params createClaimParams) (bool, error) {
	const query = `
INSERT INTO reward_claim_idempotency_keys (key_hash, player_id, campaign_id, reward_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (key_hash) DO NOTHING`

	tag, err := tx.Exec(
		ctx,
		query,
		params.KeyHash[:],
		params.PlayerID,
		params.CampaignID,
		params.RewardID,
	)
	if err != nil {
		return false, mapPostgresError(ctx, err)
	}

	return tag.RowsAffected() == 1, nil
}

func tryLoadIdempotentClaimResult(ctx context.Context, tx pgx.Tx, params createClaimParams) (CreateClaimResult, bool, error) {
	const query = `
SELECT player_id, campaign_id, reward_id, response_status, response_body
FROM reward_claim_idempotency_keys
WHERE key_hash = $1`

	var (
		playerID       string
		campaignID     string
		rewardID       string
		responseStatus sql.NullInt64
		responseBody   []byte
	)

	err := tx.QueryRow(ctx, query, params.KeyHash[:]).Scan(
		&playerID,
		&campaignID,
		&rewardID,
		&responseStatus,
		&responseBody,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreateClaimResult{}, false, nil
		}

		return CreateClaimResult{}, false, mapPostgresError(ctx, err)
	}

	if !responseStatus.Valid || len(responseBody) == 0 {
		return CreateClaimResult{}, false, fmt.Errorf("committed idempotency key missing stored response: %w", ErrInternal)
	}

	if playerID != params.PlayerID || campaignID != params.CampaignID || rewardID != params.RewardID {
		return CreateClaimResult{}, false, ErrIdempotencyKeyReused
	}

	statusCode := int(responseStatus.Int64)
	if statusCode != createClaimStatusCreated && statusCode != createClaimStatusConflict {
		return CreateClaimResult{}, false, fmt.Errorf("unexpected stored reward claim response status %d: %w", statusCode, ErrInternal)
	}
	if !utf8.Valid(responseBody) || !json.Valid(responseBody) {
		return CreateClaimResult{}, false, fmt.Errorf("invalid stored reward claim response body: %w", ErrInternal)
	}

	return CreateClaimResult{
		StatusCode:   statusCode,
		ResponseBody: responseBody,
		Replayed:     true,
	}, true, nil
}

func completeCreatedClaim(ctx context.Context, tx pgx.Tx, params createClaimParams, claim claim) (CreateClaimResult, error) {
	body, err := marshalCreatedClaimResponse(claim)
	if err != nil {
		return CreateClaimResult{}, err
	}

	if err := insertRewardClaimedOutboxEvent(ctx, tx, claim); err != nil {
		return CreateClaimResult{}, err
	}

	if err := completeIdempotencyKey(ctx, tx, params, createClaimStatusCreated, body, claim.ID); err != nil {
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
INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload)
VALUES ($1, $2, $3, $4, $5::jsonb)`

	_, err = tx.Exec(
		ctx,
		query,
		eventID,
		outboxAggregateTypeRewardClaim,
		claim.ID,
		outboxEventTypeRewardClaimed,
		string(payload),
	)
	if err != nil {
		return mapPostgresError(ctx, err)
	}

	return nil
}

func completeDuplicateClaim(ctx context.Context, tx pgx.Tx, params createClaimParams) (CreateClaimResult, error) {
	body, err := marshalDuplicateClaimResponse()
	if err != nil {
		return CreateClaimResult{}, err
	}

	if err := completeIdempotencyKey(ctx, tx, params, createClaimStatusConflict, body, ""); err != nil {
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
	params createClaimParams,
	statusCode int,
	responseBody []byte,
	claimID string,
) error {
	const query = `
UPDATE reward_claim_idempotency_keys
SET response_status = $2,
    response_body = $3,
    reward_claim_id = NULLIF($4, '')::uuid
WHERE key_hash = $1
  AND player_id = $5
  AND campaign_id = $6
  AND reward_id = $7
  AND response_status IS NULL
  AND response_body IS NULL
  AND reward_claim_id IS NULL`

	tag, err := tx.Exec(
		ctx,
		query,
		params.KeyHash[:],
		statusCode,
		responseBody,
		claimID,
		params.PlayerID,
		params.CampaignID,
		params.RewardID,
	)
	if err != nil {
		return mapPostgresError(ctx, err)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf("complete idempotency key affected %d rows: %w", tag.RowsAffected(), ErrInternal)
	}

	return nil
}

func tryInsertClaim(ctx context.Context, tx pgx.Tx, params createClaimParams) (claim, bool, error) {
	const query = `
INSERT INTO reward_claims (id, player_id, campaign_id, reward_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (player_id, campaign_id, reward_id) DO NOTHING
RETURNING id::text, player_id, campaign_id, reward_id, created_at`

	var created claim

	err := tx.QueryRow(
		ctx,
		query,
		newUUIDV4(),
		params.PlayerID,
		params.CampaignID,
		params.RewardID,
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
