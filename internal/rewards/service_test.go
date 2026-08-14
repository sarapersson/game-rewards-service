package rewards

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sarapersson/game-rewards-service/internal/idempotency"
)

const serviceTestClaimID = "11111111-1111-4111-8111-111111111111"

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

	service := NewService(store)

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

	if !validUUID(store.cmd.Claim.ID) {
		t.Fatalf("stored claim ID = %q, want UUID", store.cmd.Claim.ID)
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

func TestServiceCreateClaimReturnsValidStoreResultUnchanged(t *testing.T) {
	want := CreateClaimResult{
		StatusCode: CreateClaimStatusCreated,
		ResponseBody: []byte(" \n{" +
			`"claim_id":"` + serviceTestClaimID + `",` +
			`"player_id":"player-123",` +
			`"campaign_id":"campaign-123",` +
			`"reward_id":"reward-123",` +
			`"status":"claimed",` +
			`"claimed_at":"2026-08-11T12:00:00Z"}` + "\t"),
		Replayed: true,
	}

	store := &fakeStore{result: want}
	service := NewService(store)

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
		t.Fatalf("response body = %q, want %q", got.ResponseBody, want.ResponseBody)
	}
}

func TestServiceCreateClaimRejectsInvalidStoreResult(t *testing.T) {
	store := &fakeStore{result: CreateClaimResult{
		StatusCode:   CreateClaimStatusCreated,
		ResponseBody: []byte(`{}`),
	}}
	service := NewService(store)

	got, err := service.CreateClaim(context.Background(), validCreateClaimCommand())
	if err == nil {
		t.Fatal("CreateClaim returned nil error, want internal error")
	}

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("CreateClaim error = %v, want ErrInternal", err)
	}

	if got.StatusCode != 0 || got.ResponseBody != nil || got.Replayed {
		t.Fatalf("CreateClaim result = %+v, want zero value", got)
	}
}

func TestServiceCreateClaimValidation(t *testing.T) {
	tests := []struct {
		name        string
		cmd         CreateClaimCommand
		wantField   string
		wantMessage string
	}{
		{
			name: "missing player_id",
			cmd: CreateClaimCommand{
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "player_id",
			wantMessage: "player_id is required",
		},
		{
			name: "whitespace-only player_id",
			cmd: CreateClaimCommand{
				PlayerID:       "   ",
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "player_id",
			wantMessage: "player_id is required",
		},
		{
			name: "missing campaign_id",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "campaign_id",
			wantMessage: "campaign_id is required",
		},
		{
			name: "whitespace-only campaign_id",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "   ",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "campaign_id",
			wantMessage: "campaign_id is required",
		},
		{
			name: "missing reward_id",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "reward_id",
			wantMessage: "reward_id is required",
		},
		{
			name: "whitespace-only reward_id",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				RewardID:       "   ",
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "reward_id",
			wantMessage: "reward_id is required",
		},
		{
			name: "player_id contains NUL",
			cmd: CreateClaimCommand{
				PlayerID:       "player\x00one",
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "player_id",
			wantMessage: "player_id must not contain NUL characters",
		},
		{
			name: "campaign_id contains NUL",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign\x00one",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "campaign_id",
			wantMessage: "campaign_id must not contain NUL characters",
		},
		{
			name: "reward_id contains NUL",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				RewardID:       "reward\x00one",
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "reward_id",
			wantMessage: "reward_id must not contain NUL characters",
		},
		{
			name: "player_id too long",
			cmd: CreateClaimCommand{
				PlayerID:       stringOfLength(maxIDLength + 1),
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "player_id",
			wantMessage: "player_id must be at most 128 characters",
		},
		{
			name: "campaign_id too long",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     stringOfLength(maxIDLength + 1),
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "campaign_id",
			wantMessage: "campaign_id must be at most 128 characters",
		},
		{
			name: "reward_id too long",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				RewardID:       stringOfLength(maxIDLength + 1),
				IdempotencyKey: "claim-key-123",
			},
			wantField:   "reward_id",
			wantMessage: "reward_id must be at most 128 characters",
		},
		{
			name: "missing idempotency key",
			cmd: CreateClaimCommand{
				PlayerID:   "player-123",
				CampaignID: "campaign-123",
				RewardID:   "reward-123",
			},
			wantField:   "idempotency_key",
			wantMessage: "idempotency key is required",
		},
		{
			name: "whitespace-only idempotency key",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "   ",
			},
			wantField:   "idempotency_key",
			wantMessage: "idempotency key is required",
		},
		{
			name: "invalid idempotency key",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim\nkey",
			},
			wantField:   "idempotency_key",
			wantMessage: "idempotency key is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			service := NewService(store)

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

			if validationErr.Message != tt.wantMessage {
				t.Fatalf("ValidationError.Message = %q, want %q", validationErr.Message, tt.wantMessage)
			}

			if err.Error() != tt.wantMessage {
				t.Fatalf("CreateClaim error = %q, want %q", err.Error(), tt.wantMessage)
			}

			if store.called {
				t.Fatal("store was called for invalid command")
			}
		})
	}
}

func TestServiceCreateClaimAcceptsMaximumMultibyteIDLength(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)
	maxLengthID := strings.Repeat("å", maxIDLength)

	if _, err := service.CreateClaim(context.Background(), CreateClaimCommand{
		PlayerID:       maxLengthID,
		CampaignID:     maxLengthID,
		RewardID:       maxLengthID,
		IdempotencyKey: "claim-key-123",
	}); err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}

	if store.cmd.Claim.PlayerID != maxLengthID ||
		store.cmd.Claim.CampaignID != maxLengthID ||
		store.cmd.Claim.RewardID != maxLengthID {
		t.Fatalf("stored claim identifiers do not preserve %d-character multibyte values", maxIDLength)
	}
}

func TestServiceCreateClaimReturnsDuplicateResult(t *testing.T) {
	body, err := MarshalDuplicateClaimResponse()
	if err != nil {
		t.Fatalf("MarshalDuplicateClaimResponse returned error: %v", err)
	}

	want := CreateClaimResult{
		StatusCode:   CreateClaimStatusConflict,
		ResponseBody: body,
	}
	store := &fakeStore{result: want}

	service := NewService(store)

	got, err := service.CreateClaim(context.Background(), validCreateClaimCommand())
	if err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}

	if got.StatusCode != want.StatusCode {
		t.Fatalf("status code = %d, want %d", got.StatusCode, want.StatusCode)
	}
	if got.Replayed {
		t.Fatal("duplicate result should not be marked replayed")
	}
	if !bytes.Equal(got.ResponseBody, want.ResponseBody) {
		t.Fatalf("response body = %q, want %q", got.ResponseBody, want.ResponseBody)
	}
}

func TestServiceCreateClaimReturnsUnavailableWithoutStore(t *testing.T) {
	service := NewService(nil)

	_, err := service.CreateClaim(context.Background(), validCreateClaimCommand())
	if err == nil {
		t.Fatal("CreateClaim returned nil error, want unavailable error")
	}

	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("CreateClaim error = %v, want ErrUnavailable", err)
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
