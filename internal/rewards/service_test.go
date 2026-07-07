package rewards

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	inserted Claim
	result   Claim
	err      error
}

func (s *fakeStore) InsertClaim(_ context.Context, claim Claim) (Claim, error) {
	s.inserted = claim

	if s.err != nil {
		return Claim{}, s.err
	}

	if s.result.ID == "" {
		return claim, nil
	}

	return s.result, nil
}

func TestServiceCreateClaim(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	store := &fakeStore{
		result: Claim{
			ID:         "claim-123",
			PlayerID:   "player-123",
			CampaignID: "campaign-123",
			RewardID:   "reward-123",
			Status:     ClaimStatusClaimed,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}

	service := NewServiceWithIDGenerator(store, func() (string, error) {
		return "claim-123", nil
	})

	claim, err := service.CreateClaim(context.Background(), CreateClaimCommand{
		PlayerID:   " player-123 ",
		CampaignID: " campaign-123 ",
		RewardID:   " reward-123 ",
	})
	if err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}

	if claim.ID != "claim-123" {
		t.Fatalf("claim.ID = %q, want %q", claim.ID, "claim-123")
	}

	if claim.PlayerID != "player-123" {
		t.Fatalf("claim.PlayerID = %q, want %q", claim.PlayerID, "player-123")
	}

	if claim.CampaignID != "campaign-123" {
		t.Fatalf("claim.CampaignID = %q, want %q", claim.CampaignID, "campaign-123")
	}

	if claim.RewardID != "reward-123" {
		t.Fatalf("claim.RewardID = %q, want %q", claim.RewardID, "reward-123")
	}

	if claim.Status != ClaimStatusClaimed {
		t.Fatalf("claim.Status = %q, want %q", claim.Status, ClaimStatusClaimed)
	}

	if store.inserted.ID != "claim-123" {
		t.Fatalf("inserted.ID = %q, want %q", store.inserted.ID, "claim-123")
	}

	if store.inserted.PlayerID != "player-123" {
		t.Fatalf("inserted.PlayerID = %q, want %q", store.inserted.PlayerID, "player-123")
	}

	if store.inserted.CampaignID != "campaign-123" {
		t.Fatalf("inserted.CampaignID = %q, want %q", store.inserted.CampaignID, "campaign-123")
	}

	if store.inserted.RewardID != "reward-123" {
		t.Fatalf("inserted.RewardID = %q, want %q", store.inserted.RewardID, "reward-123")
	}

	if store.inserted.Status != ClaimStatusClaimed {
		t.Fatalf("inserted.Status = %q, want %q", store.inserted.Status, ClaimStatusClaimed)
	}
}

func TestServiceCreateClaimValidation(t *testing.T) {
	tests := []struct {
		name      string
		cmd       CreateClaimCommand
		wantField string
	}{
		{
			name: "missing player_id",
			cmd: CreateClaimCommand{
				CampaignID: "campaign-123",
				RewardID:   "reward-123",
			},
			wantField: "player_id",
		},
		{
			name: "missing campaign_id",
			cmd: CreateClaimCommand{
				PlayerID: "player-123",
				RewardID: "reward-123",
			},
			wantField: "campaign_id",
		},
		{
			name: "missing reward_id",
			cmd: CreateClaimCommand{
				PlayerID:   "player-123",
				CampaignID: "campaign-123",
			},
			wantField: "reward_id",
		},
		{
			name: "player_id too long",
			cmd: CreateClaimCommand{
				PlayerID:   stringOfLength(MaxIDLength + 1),
				CampaignID: "campaign-123",
				RewardID:   "reward-123",
			},
			wantField: "player_id",
		},
		{
			name: "campaign_id too long",
			cmd: CreateClaimCommand{
				PlayerID:   "player-123",
				CampaignID: stringOfLength(MaxIDLength + 1),
				RewardID:   "reward-123",
			},
			wantField: "campaign_id",
		},
		{
			name: "reward_id too long",
			cmd: CreateClaimCommand{
				PlayerID:   "player-123",
				CampaignID: "campaign-123",
				RewardID:   stringOfLength(MaxIDLength + 1),
			},
			wantField: "reward_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			service := NewServiceWithIDGenerator(store, func() (string, error) {
				return "claim-123", nil
			})

			_, err := service.CreateClaim(context.Background(), tt.cmd)
			if err == nil {
				t.Fatal("CreateClaim returned nil error, want validation error")
			}

			if !IsValidationError(err) {
				t.Fatalf("CreateClaim error = %v, want validation error", err)
			}

			var validationErr ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("CreateClaim error = %v, want ValidationError", err)
			}

			if validationErr.Field != tt.wantField {
				t.Fatalf("ValidationError.Field = %q, want %q", validationErr.Field, tt.wantField)
			}

			if store.inserted.ID != "" {
				t.Fatalf("store was called for invalid command: %+v", store.inserted)
			}
		})
	}
}

func TestServiceCreateClaimPropagatesDuplicateClaim(t *testing.T) {
	store := &fakeStore{
		err: ErrDuplicateClaim,
	}

	service := NewServiceWithIDGenerator(store, func() (string, error) {
		return "claim-123", nil
	})

	_, err := service.CreateClaim(context.Background(), CreateClaimCommand{
		PlayerID:   "player-123",
		CampaignID: "campaign-123",
		RewardID:   "reward-123",
	})
	if err == nil {
		t.Fatal("CreateClaim returned nil error, want duplicate claim error")
	}

	if !errors.Is(err, ErrDuplicateClaim) {
		t.Fatalf("CreateClaim error = %v, want ErrDuplicateClaim", err)
	}
}

func TestServiceCreateClaimReturnsUnavailableWithoutStore(t *testing.T) {
	service := NewServiceWithIDGenerator(nil, func() (string, error) {
		return "claim-123", nil
	})

	_, err := service.CreateClaim(context.Background(), CreateClaimCommand{
		PlayerID:   "player-123",
		CampaignID: "campaign-123",
		RewardID:   "reward-123",
	})
	if err == nil {
		t.Fatal("CreateClaim returned nil error, want unavailable error")
	}

	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("CreateClaim error = %v, want ErrUnavailable", err)
	}
}

func TestServiceCreateClaimReturnsIDGenerationError(t *testing.T) {
	idErr := errors.New("random source failed")

	service := NewServiceWithIDGenerator(&fakeStore{}, func() (string, error) {
		return "", idErr
	})

	_, err := service.CreateClaim(context.Background(), CreateClaimCommand{
		PlayerID:   "player-123",
		CampaignID: "campaign-123",
		RewardID:   "reward-123",
	})
	if err == nil {
		t.Fatal("CreateClaim returned nil error, want ID generation error")
	}

	if !errors.Is(err, idErr) {
		t.Fatalf("CreateClaim error = %v, want wrapped ID error", err)
	}
}

func stringOfLength(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}

	return string(b)
}
