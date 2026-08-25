package rewards

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
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

	return createClaimParams{
		PlayerID:   cmd.PlayerID,
		CampaignID: cmd.CampaignID,
		RewardID:   cmd.RewardID,
		KeyHash:    sha256.Sum256([]byte(cmd.IdempotencyKey)),
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

	return validateIdempotencyKey(cmd.IdempotencyKey)
}

func validateClaimID(field, value string) error {
	if value == "" {
		return &InvalidInputError{Message: fmt.Sprintf("%s is required", field)}
	}

	if !utf8.ValidString(value) {
		return &InvalidInputError{Message: fmt.Sprintf("%s must be valid UTF-8", field)}
	}

	if strings.ContainsRune(value, '\x00') {
		return &InvalidInputError{Message: fmt.Sprintf("%s must not contain NUL characters", field)}
	}

	if utf8.RuneCountInString(value) > maxIDLength {
		return &InvalidInputError{Message: fmt.Sprintf("%s must be at most %d characters", field, maxIDLength)}
	}

	return nil
}

func validateIdempotencyKey(key string) error {
	if key == "" {
		return &InvalidInputError{Message: "idempotency key is required"}
	}

	if len(key) > maxIdempotencyKeyLength {
		return &InvalidInputError{Message: "idempotency key is invalid"}
	}

	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return &InvalidInputError{Message: "idempotency key is invalid"}
		}
	}

	return nil
}
