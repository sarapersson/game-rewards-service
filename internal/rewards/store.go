package rewards

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const rewardClaimsPlayerCampaignRewardConstraint = "reward_claims_player_campaign_reward_uniq"

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

	const query = `
INSERT INTO reward_claims (id, player_id, campaign_id, reward_id, status)
VALUES ($1, $2, $3, $4, $5)
RETURNING id::text, player_id, campaign_id, reward_id, status, created_at, updated_at`

	var created Claim
	err := s.pool.QueryRow(
		queryCtx,
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

func mapPostgresError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("postgres insert reward claim: %w", ErrUnavailable)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" && pgErr.ConstraintName == rewardClaimsPlayerCampaignRewardConstraint {
			return ErrDuplicateClaim
		}

		return fmt.Errorf("postgres insert reward claim: %w", ErrInternal)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres insert reward claim returned no row: %w", ErrInternal)
	}

	return fmt.Errorf("postgres insert reward claim: %w", ErrInternal)
}
