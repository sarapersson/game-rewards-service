package rewards

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sarapersson/game-rewards-service/internal/idempotency"
)

type Service struct {
	pool         *pgxpool.Pool
	queryTimeout time.Duration
}

func NewService(pool *pgxpool.Pool, queryTimeout time.Duration) (*Service, error) {
	if pool == nil {
		return nil, errors.New("reward claim pool is required")
	}
	if queryTimeout <= 0 {
		return nil, errors.New("reward claim query timeout must be greater than zero")
	}

	return &Service{pool: pool, queryTimeout: queryTimeout}, nil
}

func (s *Service) CreateClaim(ctx context.Context, cmd CreateClaimCommand) (CreateClaimResult, error) {
	params, err := prepareCreateClaim(cmd)
	if err != nil {
		return CreateClaimResult{}, err
	}

	result, err := s.createClaim(ctx, params)
	if err != nil {
		return CreateClaimResult{}, fmt.Errorf("create reward claim: %w", err)
	}

	return result, nil
}

func prepareCreateClaim(cmd CreateClaimCommand) (createClaimParams, error) {
	cmd.PlayerID = strings.TrimSpace(cmd.PlayerID)
	cmd.CampaignID = strings.TrimSpace(cmd.CampaignID)
	cmd.RewardID = strings.TrimSpace(cmd.RewardID)
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)

	if err := validateCreateClaimCommand(cmd); err != nil {
		return createClaimParams{}, err
	}

	keyHash, err := idempotency.HashKey(cmd.IdempotencyKey)
	if err != nil {
		return createClaimParams{}, ValidationError{
			Field:   "idempotency_key",
			Message: "idempotency key is invalid",
		}
	}

	requestHash, err := idempotency.HashRewardClaimRequest(idempotency.RewardClaimRequest{
		PlayerID:   cmd.PlayerID,
		CampaignID: cmd.CampaignID,
		RewardID:   cmd.RewardID,
	})
	if err != nil {
		return createClaimParams{}, fmt.Errorf("hash reward claim request: %w", ErrInternal)
	}

	return createClaimParams{
		Claim: claimToCreate{
			ID:         newUUIDV4(),
			PlayerID:   cmd.PlayerID,
			CampaignID: cmd.CampaignID,
			RewardID:   cmd.RewardID,
		},
		KeyHash:     keyHash,
		RequestHash: requestHash,
	}, nil
}

func validateCreateClaimCommand(cmd CreateClaimCommand) error {
	if err := validateClaimID("player_id", cmd.PlayerID); err != nil {
		return err
	}

	if err := validateClaimID("campaign_id", cmd.CampaignID); err != nil {
		return err
	}

	if err := validateClaimID("reward_id", cmd.RewardID); err != nil {
		return err
	}

	if cmd.IdempotencyKey == "" {
		return ValidationError{Field: "idempotency_key", Message: "idempotency key is required"}
	}

	return nil
}

func validateClaimID(field, value string) error {
	if value == "" {
		return ValidationError{Field: field, Message: fmt.Sprintf("%s is required", field)}
	}

	if strings.ContainsRune(value, '\x00') {
		return ValidationError{
			Field:   field,
			Message: fmt.Sprintf("%s must not contain NUL characters", field),
		}
	}

	if utf8.RuneCountInString(value) > maxIDLength {
		return ValidationError{
			Field:   field,
			Message: fmt.Sprintf("%s must be at most %d characters", field, maxIDLength),
		}
	}

	return nil
}
