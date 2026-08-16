package rewards

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sarapersson/game-rewards-service/internal/idempotency"
)

func TestNewServiceValidatesConstruction(t *testing.T) {
	tests := []struct {
		name         string
		pool         *pgxpool.Pool
		queryTimeout time.Duration
	}{
		{name: "missing pool", queryTimeout: time.Second},
		{name: "non-positive query timeout", pool: new(pgxpool.Pool)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewService(tt.pool, tt.queryTimeout)
			if err == nil {
				t.Fatal("NewService returned nil error")
			}
			if service != nil {
				t.Fatalf("NewService service = %#v, want nil", service)
			}
		})
	}
}

func TestPrepareCreateClaimNormalizesAndHashesCommand(t *testing.T) {
	params, err := prepareCreateClaim(CreateClaimCommand{
		PlayerID:       " player-123 ",
		CampaignID:     " campaign-123 ",
		RewardID:       " reward-123 ",
		IdempotencyKey: " claim-key-123 ",
	})
	if err != nil {
		t.Fatalf("prepareCreateClaim returned error: %v", err)
	}

	if !validUUID(params.Claim.ID) {
		t.Fatalf("claim ID = %q, want UUID", params.Claim.ID)
	}
	if params.Claim.PlayerID != "player-123" {
		t.Fatalf("player ID = %q, want player-123", params.Claim.PlayerID)
	}
	if params.Claim.CampaignID != "campaign-123" {
		t.Fatalf("campaign ID = %q, want campaign-123", params.Claim.CampaignID)
	}
	if params.Claim.RewardID != "reward-123" {
		t.Fatalf("reward ID = %q, want reward-123", params.Claim.RewardID)
	}

	wantKeyHash, err := idempotency.HashKey("claim-key-123")
	if err != nil {
		t.Fatalf("HashKey returned error: %v", err)
	}
	if params.KeyHash != wantKeyHash {
		t.Fatalf("key hash = %x, want %x", params.KeyHash, wantKeyHash)
	}

	wantRequestHash, err := idempotency.HashRewardClaimRequest(idempotency.RewardClaimRequest{
		PlayerID:   "player-123",
		CampaignID: "campaign-123",
		RewardID:   "reward-123",
	})
	if err != nil {
		t.Fatalf("HashRewardClaimRequest returned error: %v", err)
	}
	if params.RequestHash != wantRequestHash {
		t.Fatalf("request hash = %x, want %x", params.RequestHash, wantRequestHash)
	}
}

func TestPrepareCreateClaimValidation(t *testing.T) {
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
			_, err := prepareCreateClaim(tt.cmd)
			if err == nil {
				t.Fatal("prepareCreateClaim returned nil error, want validation error")
			}
			if !IsValidationError(err) {
				t.Fatalf("prepareCreateClaim error = %v, want validation error", err)
			}

			var validationErr ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("prepareCreateClaim error = %v, want ValidationError", err)
			}
			if validationErr.Field != tt.wantField {
				t.Fatalf("ValidationError.Field = %q, want %q", validationErr.Field, tt.wantField)
			}
			if validationErr.Message != tt.wantMessage {
				t.Fatalf("ValidationError.Message = %q, want %q", validationErr.Message, tt.wantMessage)
			}
		})
	}
}

func TestPrepareCreateClaimAcceptsMaximumMultibyteIDLength(t *testing.T) {
	maxLengthID := strings.Repeat("å", maxIDLength)

	params, err := prepareCreateClaim(CreateClaimCommand{
		PlayerID:       maxLengthID,
		CampaignID:     maxLengthID,
		RewardID:       maxLengthID,
		IdempotencyKey: "claim-key-123",
	})
	if err != nil {
		t.Fatalf("prepareCreateClaim returned error: %v", err)
	}

	if params.Claim.PlayerID != maxLengthID ||
		params.Claim.CampaignID != maxLengthID ||
		params.Claim.RewardID != maxLengthID {
		t.Fatal("prepareCreateClaim changed valid maximum-length identifiers")
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

func stringOfLength(length int) string {
	return strings.Repeat("a", length)
}
