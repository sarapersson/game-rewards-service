package idempotency

import (
	"errors"
	"strings"
	"testing"
)

func TestHashKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{
			name: "valid key",
			key:  "claim-key-123",
		},
		{
			name: "trims surrounding whitespace",
			key:  "  claim-key-123  ",
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
			name:    "too long",
			key:     strings.Repeat("a", 256),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt

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

			if len(got) != keyHashSize {
				t.Fatalf("len(HashKey()) = %d, want %d", len(got), keyHashSize)
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

	a, err := HashRewardClaimRequest(RewardClaimRequest{
		PlayerID:   "player-123",
		CampaignID: "winter-2026",
		RewardID:   "daily-login",
	})
	if err != nil {
		t.Fatalf("HashRewardClaimRequest() error = %v", err)
	}

	b, err := HashRewardClaimRequest(RewardClaimRequest{
		PlayerID:   "player-123",
		CampaignID: "winter-2026",
		RewardID:   "weekly-login",
	})
	if err != nil {
		t.Fatalf("HashRewardClaimRequest() error = %v", err)
	}

	if a == b {
		t.Fatal("HashRewardClaimRequest() should change when payload changes")
	}
}
