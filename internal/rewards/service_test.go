package rewards

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sarapersson/game-rewards-service/internal/idempotency"
)

type fakeStore struct {
	cmd    CreateClaimStoreCommand
	result CreateClaimResult
	err    error
	called bool
}

func (s *fakeStore) CreateClaim(_ context.Context, cmd CreateClaimStoreCommand) (CreateClaimResult, error) {
	s.called = true
	s.cmd = cmd

	if s.err != nil {
		return CreateClaimResult{}, s.err
	}

	if s.result.ResponseBody != nil || s.result.StatusCode != 0 || s.result.Replayed {
		return s.result, nil
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

	if !store.called {
		t.Fatal("CreateClaim did not call store")
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

	if store.cmd.Operation != idempotency.RewardClaimOperation {
		t.Fatalf("store command operation = %q, want %q", store.cmd.Operation, idempotency.RewardClaimOperation)
	}

	wantKeyHash, err := idempotency.HashKey("claim-key-123")
	if err != nil {
		t.Fatalf("HashKey returned error: %v", err)
	}

	if !bytes.Equal(store.cmd.KeyHash, wantKeyHash[:]) {
		t.Fatalf("store command key hash = %x, want %x", store.cmd.KeyHash, wantKeyHash)
	}

	wantRequestHash, err := idempotency.HashRewardClaimRequest(idempotency.RewardClaimRequest{
		PlayerID:   "player-123",
		CampaignID: "campaign-123",
		RewardID:   "reward-123",
	})
	if err != nil {
		t.Fatalf("HashRewardClaimRequest returned error: %v", err)
	}

	if !bytes.Equal(store.cmd.RequestHash, wantRequestHash[:]) {
		t.Fatalf("store command request hash = %x, want %x", store.cmd.RequestHash, wantRequestHash)
	}
}

func TestServiceCreateClaimReturnsStoreResultUnchanged(t *testing.T) {
	want := CreateClaimResult{
		StatusCode:   CreateClaimStatusCreated,
		ResponseBody: []byte(`{"claim_id":"claim-123"}`),
		Replayed:     true,
	}

	store := &fakeStore{result: want}
	service := NewServiceWithIDGenerator(store, func() (string, error) {
		return "unused-claim-id", nil
	})

	got, err := service.CreateClaim(context.Background(), validCreateClaimCommand())
	if err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}

	if got.StatusCode != want.StatusCode {
		t.Fatalf("status code = %d, want %d", got.StatusCode, want.StatusCode)
	}

	if got.Replayed != want.Replayed {
		t.Fatalf("replayed = %t, want %t", got.Replayed, want.Replayed)
	}

	if !bytes.Equal(got.ResponseBody, want.ResponseBody) {
		t.Fatalf("response body = %s, want %s", got.ResponseBody, want.ResponseBody)
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
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField: "player_id",
		},
		{
			name: "missing campaign_id",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField: "campaign_id",
		},
		{
			name: "missing reward_id",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField: "reward_id",
		},
		{
			name: "player_id too long",
			cmd: CreateClaimCommand{
				PlayerID:       stringOfLength(MaxIDLength + 1),
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField: "player_id",
		},
		{
			name: "campaign_id too long",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     stringOfLength(MaxIDLength + 1),
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField: "campaign_id",
		},
		{
			name: "reward_id too long",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				RewardID:       stringOfLength(MaxIDLength + 1),
				IdempotencyKey: "claim-key-123",
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
		{
			name: "invalid idempotency key",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim\nkey",
			},
			wantField: "idempotency_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			idCalled := false

			service := NewServiceWithIDGenerator(store, func() (string, error) {
				idCalled = true
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

			if idCalled {
				t.Fatal("ID generator was called for invalid command")
			}

			if store.called {
				t.Fatal("store was called for invalid command")
			}
		})
	}
}

func TestServiceCreateClaimAcceptsMaximumMultibyteIDLength(t *testing.T) {
	store := &fakeStore{}
	service := NewServiceWithIDGenerator(store, func() (string, error) {
		return "claim-123", nil
	})

	playerID := strings.Repeat("å", MaxIDLength)

	_, err := service.CreateClaim(context.Background(), CreateClaimCommand{
		PlayerID:       playerID,
		CampaignID:     "campaign-123",
		RewardID:       "reward-123",
		IdempotencyKey: "claim-key-123",
	})
	if err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}

	if store.cmd.Claim.PlayerID != playerID {
		t.Fatalf("stored player ID length = %d runes, want %d", len([]rune(store.cmd.Claim.PlayerID)), MaxIDLength)
	}
}

func TestServiceCreateClaimPropagatesDuplicateClaim(t *testing.T) {
	store := &fakeStore{err: ErrDuplicateClaim}

	service := NewServiceWithIDGenerator(store, func() (string, error) {
		return "claim-123", nil
	})

	_, err := service.CreateClaim(context.Background(), validCreateClaimCommand())
	if err == nil {
		t.Fatal("CreateClaim returned nil error, want duplicate claim error")
	}

	if !errors.Is(err, ErrDuplicateClaim) {
		t.Fatalf("CreateClaim error = %v, want ErrDuplicateClaim", err)
	}
}

func TestServiceCreateClaimReturnsUnavailableWithoutStore(t *testing.T) {
	idCalled := false

	service := NewServiceWithIDGenerator(nil, func() (string, error) {
		idCalled = true
		return "claim-123", nil
	})

	_, err := service.CreateClaim(context.Background(), validCreateClaimCommand())
	if err == nil {
		t.Fatal("CreateClaim returned nil error, want unavailable error")
	}

	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("CreateClaim error = %v, want ErrUnavailable", err)
	}

	if idCalled {
		t.Fatal("ID generator was called without an available store")
	}
}

func TestServiceCreateClaimReturnsIDGenerationError(t *testing.T) {
	idErr := errors.New("random source failed")
	store := &fakeStore{}

	service := NewServiceWithIDGenerator(store, func() (string, error) {
		return "", idErr
	})

	_, err := service.CreateClaim(context.Background(), validCreateClaimCommand())
	if err == nil {
		t.Fatal("CreateClaim returned nil error, want ID generation error")
	}

	if !errors.Is(err, idErr) {
		t.Fatalf("CreateClaim error = %v, want wrapped ID error", err)
	}

	if store.called {
		t.Fatal("store was called after ID generation failed")
	}
}

func validCreateClaimCommand() CreateClaimCommand {
	return CreateClaimCommand{
		PlayerID:       "player-123",
		CampaignID:     "campaign-123",
		RewardID:       "reward-123",
		IdempotencyKey: "claim-key-123",
	}
}

func stringOfLength(n int) string {
	return strings.Repeat("a", n)
}
