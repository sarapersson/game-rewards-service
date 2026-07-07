package rewards

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

type Store interface {
	InsertClaim(ctx context.Context, claim Claim) (Claim, error)
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

func (s *Service) CreateClaim(ctx context.Context, cmd CreateClaimCommand) (Claim, error) {
	cmd.PlayerID = strings.TrimSpace(cmd.PlayerID)
	cmd.CampaignID = strings.TrimSpace(cmd.CampaignID)
	cmd.RewardID = strings.TrimSpace(cmd.RewardID)

	if err := validateCreateClaimCommand(cmd); err != nil {
		return Claim{}, err
	}

	if s == nil || s.store == nil {
		return Claim{}, fmt.Errorf("insert reward claim: %w", ErrUnavailable)
	}

	newID := s.newID
	if newID == nil {
		newID = NewUUIDV4
	}

	id, err := newID()
	if err != nil {
		return Claim{}, fmt.Errorf("create reward claim id: %w", err)
	}

	claim := Claim{
		ID:         id,
		PlayerID:   cmd.PlayerID,
		CampaignID: cmd.CampaignID,
		RewardID:   cmd.RewardID,
		Status:     ClaimStatusClaimed,
	}

	created, err := s.store.InsertClaim(ctx, claim)
	if err != nil {
		return Claim{}, fmt.Errorf("insert reward claim: %w", err)
	}

	return created, nil
}

func validateCreateClaimCommand(cmd CreateClaimCommand) error {
	if cmd.PlayerID == "" {
		return ValidationError{Field: "player_id", Message: "player_id is required"}
	}

	if utf8.RuneCountInString(cmd.PlayerID) > MaxIDLength {
		return ValidationError{Field: "player_id", Message: "player_id must be at most 128 characters"}
	}

	if cmd.CampaignID == "" {
		return ValidationError{Field: "campaign_id", Message: "campaign_id is required"}
	}

	if utf8.RuneCountInString(cmd.CampaignID) > MaxIDLength {
		return ValidationError{Field: "campaign_id", Message: "campaign_id must be at most 128 characters"}
	}

	if cmd.RewardID == "" {
		return ValidationError{Field: "reward_id", Message: "reward_id is required"}
	}

	if utf8.RuneCountInString(cmd.RewardID) > MaxIDLength {
		return ValidationError{Field: "reward_id", Message: "reward_id must be at most 128 characters"}
	}

	return nil
}
