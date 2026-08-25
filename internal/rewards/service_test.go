package rewards

import (
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

func TestPrepareCreateClaimNormalizesCommandAndHashesKey(t *testing.T) {
	params, err := prepareCreateClaim(CreateClaimCommand{
		PlayerID:       " player-123 ",
		CampaignID:     " campaign-123 ",
		RewardID:       " reward-123 ",
		IdempotencyKey: " claim-key-123 ",
	})
	if err != nil {
		t.Fatalf("prepareCreateClaim returned error: %v", err)
	}

	if params.PlayerID != "player-123" {
		t.Fatalf("player ID = %q, want player-123", params.PlayerID)
	}
	if params.CampaignID != "campaign-123" {
		t.Fatalf("campaign ID = %q, want campaign-123", params.CampaignID)
	}
	if params.RewardID != "reward-123" {
		t.Fatalf("reward ID = %q, want reward-123", params.RewardID)
	}

	wantKeyHash := sha256.Sum256([]byte("claim-key-123"))
	if params.KeyHash != wantKeyHash {
		t.Fatalf("key hash = %x, want %x", params.KeyHash, wantKeyHash)
	}
}

func TestPrepareCreateClaimValidation(t *testing.T) {
	tests := []struct {
		name        string
		cmd         CreateClaimCommand
		wantMessage string
	}{
		{
			name: "missing player_id",
			cmd: CreateClaimCommand{
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
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
			wantMessage: "player_id is required",
		},
		{
			name: "missing campaign_id",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantMessage: "campaign_id is required",
		},
		{
			name: "missing reward_id",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				IdempotencyKey: "claim-key-123",
			},
			wantMessage: "reward_id is required",
		},
		{
			name: "player_id contains invalid UTF-8",
			cmd: CreateClaimCommand{
				PlayerID:       "player-\xff",
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
			wantMessage: "player_id must be valid UTF-8",
		},
		{
			name: "player_id contains NUL",
			cmd: CreateClaimCommand{
				PlayerID:       "player\x00one",
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim-key-123",
			},
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
			wantMessage: "reward_id must be at most 128 characters",
		},
		{
			name: "missing idempotency key",
			cmd: CreateClaimCommand{
				PlayerID:   "player-123",
				CampaignID: "campaign-123",
				RewardID:   "reward-123",
			},
			wantMessage: "idempotency key is required",
		},
		{
			name: "idempotency key contains control character",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim\nkey",
			},
			wantMessage: "idempotency key is invalid",
		},
		{
			name: "idempotency key contains DEL",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: "claim\x7fkey",
			},
			wantMessage: "idempotency key is invalid",
		},
		{
			name: "idempotency key too long",
			cmd: CreateClaimCommand{
				PlayerID:       "player-123",
				CampaignID:     "campaign-123",
				RewardID:       "reward-123",
				IdempotencyKey: strings.Repeat("a", maxIdempotencyKeyLength+1),
			},
			wantMessage: "idempotency key is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := prepareCreateClaim(tt.cmd)
			if err == nil {
				t.Fatal("prepareCreateClaim returned nil error, want validation error")
			}
			var invalidInputErr *InvalidInputError
			if !errors.As(err, &invalidInputErr) {
				t.Fatalf("prepareCreateClaim error = %v, want *InvalidInputError", err)
			}
			if invalidInputErr.Message != tt.wantMessage {
				t.Fatalf("InvalidInputError.Message = %q, want %q", invalidInputErr.Message, tt.wantMessage)
			}
		})
	}
}

func TestPrepareCreateClaimAcceptsMaximumLengths(t *testing.T) {
	maxLengthID := strings.Repeat("å", maxIDLength)
	maxLengthKey := strings.Repeat("k", maxIdempotencyKeyLength)

	params, err := prepareCreateClaim(CreateClaimCommand{
		PlayerID:       maxLengthID,
		CampaignID:     maxLengthID,
		RewardID:       maxLengthID,
		IdempotencyKey: maxLengthKey,
	})
	if err != nil {
		t.Fatalf("prepareCreateClaim returned error: %v", err)
	}

	if params.PlayerID != maxLengthID || params.CampaignID != maxLengthID || params.RewardID != maxLengthID {
		t.Fatal("prepareCreateClaim changed valid maximum-length identifiers")
	}

	wantKeyHash := sha256.Sum256([]byte(maxLengthKey))
	if params.KeyHash != wantKeyHash {
		t.Fatalf("key hash = %x, want %x", params.KeyHash, wantKeyHash)
	}
}

func stringOfLength(length int) string {
	return strings.Repeat("a", length)
}
