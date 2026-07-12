package rewards

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sarapersson/game-rewards-service/internal/idempotency"
)

type Store interface {
	CreateClaim(ctx context.Context, cmd CreateClaimStoreCommand) (CreateClaimResult, error)
}

type Service struct {
	store Store
	newID IDGenerator
}

func NewService(store Store) *Service {
	return &Service{
		store: store,
		newID: NewUUIDV4,
	}
}

func NewServiceWithIDGenerator(store Store, newID IDGenerator) *Service {
	return &Service{
		store: store,
		newID: newID,
	}
}

func (s *Service) CreateClaim(ctx context.Context, cmd CreateClaimCommand) (CreateClaimResult, error) {
	cmd.PlayerID = strings.TrimSpace(cmd.PlayerID)
	cmd.CampaignID = strings.TrimSpace(cmd.CampaignID)
	cmd.RewardID = strings.TrimSpace(cmd.RewardID)
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)

	if err := validateCreateClaimCommand(cmd); err != nil {
		return CreateClaimResult{}, err
	}

	if s == nil || s.store == nil {
		return CreateClaimResult{}, fmt.Errorf("create reward claim: %w", ErrUnavailable)
	}

	keyHash, err := idempotency.HashKey(cmd.IdempotencyKey)
	if err != nil {
		return CreateClaimResult{}, ValidationError{
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
		return CreateClaimResult{}, fmt.Errorf("hash reward claim request: %w", ErrInternal)
	}

	newID := s.newID
	if newID == nil {
		newID = NewUUIDV4
	}

	id, err := newID()
	if err != nil {
		return CreateClaimResult{}, fmt.Errorf("create reward claim id: %w", err)
	}

	claim := Claim{
		ID:         id,
		PlayerID:   cmd.PlayerID,
		CampaignID: cmd.CampaignID,
		RewardID:   cmd.RewardID,
		Status:     ClaimStatusClaimed,
	}

	result, err := s.store.CreateClaim(ctx, CreateClaimStoreCommand{
		Claim:       claim,
		Operation:   idempotency.RewardClaimOperation,
		KeyHash:     keyHash[:],
		RequestHash: requestHash[:],
	})
	if err != nil {
		return CreateClaimResult{}, fmt.Errorf("create reward claim: %w", err)
	}

	return result, nil
}

func validateCreateClaimCommand(cmd CreateClaimCommand) error {
	if cmd.PlayerID == "" {
		return ValidationError{Field: "player_id", Message: "player_id is required"}
	}

	if utf8.RuneCountInString(cmd.PlayerID) > MaxIDLength {
		return ValidationError{
			Field:   "player_id",
			Message: fmt.Sprintf("player_id must be at most %d characters", MaxIDLength),
		}
	}

	if cmd.CampaignID == "" {
		return ValidationError{Field: "campaign_id", Message: "campaign_id is required"}
	}

	if utf8.RuneCountInString(cmd.CampaignID) > MaxIDLength {
		return ValidationError{
			Field:   "campaign_id",
			Message: fmt.Sprintf("campaign_id must be at most %d characters", MaxIDLength),
		}
	}

	if cmd.RewardID == "" {
		return ValidationError{Field: "reward_id", Message: "reward_id is required"}
	}

	if utf8.RuneCountInString(cmd.RewardID) > MaxIDLength {
		return ValidationError{
			Field:   "reward_id",
			Message: fmt.Sprintf("reward_id must be at most %d characters", MaxIDLength),
		}
	}

	if cmd.IdempotencyKey == "" {
		return ValidationError{Field: "idempotency_key", Message: "idempotency key is required"}
	}

	return nil
}
