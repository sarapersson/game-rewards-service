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
