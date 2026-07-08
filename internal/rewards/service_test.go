package rewards

import (
	"context"
	"errors"
	"testing"
)

type fakeStore struct {
	cmd CreateClaimStoreCommand
	err error
}

func (s *fakeStore) CreateClaim(_ context.Context, cmd CreateClaimStoreCommand) (CreateClaimResult, error) {
	s.cmd = cmd

	if s.err != nil {
		return CreateClaimResult{}, s.err
	}

	body, err := MarshalCreatedClaimResponse(cmd.Claim)
	if err != nil {
		return CreateClaimResult{}, err
	}

	return CreateClaimResult{
		StatusCode:   CreateClaimStatusCreated,
		ResponseBody: body,
	}, nil
}

func TestServiceCreateClaim(t *testing.T) {
	store := &fakeStore{}

	service := NewServiceWithIDGenerator(store, func() (string, error) {
		return "claim-123", nil
	})

	result, err := service.CreateClaim(context.Background(), CreateClaimCommand{
		PlayerID:       " player-123 ",
		CampaignID:     " campaign-123 ",
		RewardID:       " reward-123 ",
		IdempotencyKey: " claim-key-123 ",
	})
	if err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}

	if result.StatusCode != CreateClaimStatusCreated {
		t.Fatalf("CreateClaim status = %d, want %d", result.StatusCode, CreateClaimStatusCreated)
	}

	if result.Replayed {
		t.Fatal("CreateClaim should not return replayed result")
	}

	if len(result.ResponseBody) == 0 {
		t.Fatal("CreateClaim response body is empty")
	}

	if store.cmd.Claim.ID != "claim-123" {
		t.Fatalf("stored claim ID = %q, want %q", store.cmd.Claim.ID, "claim-123")
	}

	if store.cmd.Claim.PlayerID != "player-123" {
		t.Fatalf("stored player ID = %q, want %q", store.cmd.Claim.PlayerID, "player-123")
	}

	if store.cmd.Claim.CampaignID != "campaign-123" {
		t.Fatalf("stored campaign ID = %q, want %q", store.cmd.Claim.CampaignID, "campaign-123")
	}

	if store.cmd.Claim.RewardID != "reward-123" {
		t.Fatalf("stored reward ID = %q, want %q", store.cmd.Claim.RewardID, "reward-123")
	}

	if store.cmd.Claim.Status != ClaimStatusClaimed {
		t.Fatalf("stored claim status = %q, want %q", store.cmd.Claim.Status, ClaimStatusClaimed)
	}

	if store.cmd.Operation == "" {
		t.Fatal("store command operation is empty")
	}

	if len(store.cmd.KeyHash) != 32 {
		t.Fatalf("store command key hash length = %d, want 32", len(store.cmd.KeyHash))
	}

	if len(store.cmd.RequestHash) != 32 {
		t.Fatalf("store command request hash length = %d, want 32", len(store.cmd.RequestHash))
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
		{
			name: "missing idempotency key",
			cmd: CreateClaimCommand{
				PlayerID:   "player-123",
				CampaignID: "campaign-123",
				RewardID:   "reward-123",
			},
			wantField: "idempotency_key",
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

			if store.cmd.Claim.ID != "" {
				t.Fatalf("store was called for invalid command: %+v", store.cmd)
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
		PlayerID:       "player-123",
		CampaignID:     "campaign-123",
		RewardID:       "reward-123",
		IdempotencyKey: "claim-key-123",
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
		PlayerID:       "player-123",
		CampaignID:     "campaign-123",
		RewardID:       "reward-123",
		IdempotencyKey: "claim-key-123",
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
		PlayerID:       "player-123",
		CampaignID:     "campaign-123",
		RewardID:       "reward-123",
		IdempotencyKey: "claim-key-123",
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
