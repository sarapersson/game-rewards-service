package idempotency

import (
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
)

func TestHashKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		wantKey string
		wantErr bool
	}{
		{
			name:    "valid key",
			key:     "claim-key-123",
			wantKey: "claim-key-123",
		},
		{
			name:    "trims surrounding whitespace",
			key:     "  claim-key-123  ",
			wantKey: "claim-key-123",
		},
		{
			name:    "accepts maximum length",
			key:     strings.Repeat("a", 255),
			wantKey: strings.Repeat("a", 255),
		},
		{
			name:    "empty key",
			key:     "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			key:     "   ",
			wantErr: true,
		},
		{
			name:    "control character",
			key:     "claim\nkey",
			wantErr: true,
		},
		{
			name:    "delete control character",
			key:     "claim\x7fkey",
			wantErr: true,
		},
		{
			name:    "too long",
			key:     strings.Repeat("a", 256),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := HashKey(tt.key)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidKey) {
					t.Fatalf("HashKey() error = %v, want %v", err, ErrInvalidKey)
				}
				return
			}

			if err != nil {
				t.Fatalf("HashKey() error = %v", err)
			}

			want := sha256.Sum256([]byte(tt.wantKey))
			if got != want {
				t.Fatalf("HashKey() = %x, want %x", got, want)
			}
		})
	}
}

func TestHashKeyNormalizesWhitespace(t *testing.T) {
	t.Parallel()

	a, err := HashKey("claim-key-123")
	if err != nil {
		t.Fatalf("HashKey() error = %v", err)
	}

	b, err := HashKey("  claim-key-123  ")
	if err != nil {
		t.Fatalf("HashKey() error = %v", err)
	}

	if a != b {
		t.Fatal("HashKey() should trim surrounding whitespace")
	}
}

func TestHashRewardClaimRequest(t *testing.T) {
	t.Parallel()

	req := RewardClaimRequest{
		PlayerID:   "player-123",
		CampaignID: "winter-2026",
		RewardID:   "daily-login",
	}

	a, err := HashRewardClaimRequest(req)
	if err != nil {
		t.Fatalf("HashRewardClaimRequest() error = %v", err)
	}

	b, err := HashRewardClaimRequest(req)
	if err != nil {
		t.Fatalf("HashRewardClaimRequest() error = %v", err)
	}

	if a != b {
		t.Fatal("HashRewardClaimRequest() should be deterministic")
	}
}

func TestHashRewardClaimRequestChangesWhenPayloadChanges(t *testing.T) {
	t.Parallel()

	base := RewardClaimRequest{
		PlayerID:   "player-123",
		CampaignID: "winter-2026",
		RewardID:   "daily-login",
	}

	baseHash, err := HashRewardClaimRequest(base)
	if err != nil {
		t.Fatalf("HashRewardClaimRequest() error = %v", err)
	}

	tests := []struct {
		name string
		req  RewardClaimRequest
	}{
		{
			name: "player id",
			req: RewardClaimRequest{
				PlayerID:   "player-456",
				CampaignID: base.CampaignID,
				RewardID:   base.RewardID,
			},
		},
		{
			name: "campaign id",
			req: RewardClaimRequest{
				PlayerID:   base.PlayerID,
				CampaignID: "summer-2026",
				RewardID:   base.RewardID,
			},
		},
		{
			name: "reward id",
			req: RewardClaimRequest{
				PlayerID:   base.PlayerID,
				CampaignID: base.CampaignID,
				RewardID:   "weekly-login",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := HashRewardClaimRequest(tt.req)
			if err != nil {
				t.Fatalf("HashRewardClaimRequest() error = %v", err)
			}

			if got == baseHash {
				t.Fatal("HashRewardClaimRequest() should change when payload changes")
			}
		})
	}
}
