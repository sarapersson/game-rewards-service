package rewards

import (
	"testing"
	"time"
)

func TestNewRewardClaimedEvent(t *testing.T) {
	claimedAt := time.Date(2026, 7, 8, 12, 34, 56, 0, time.UTC)

	claim := Claim{
		ID:         "claim_123",
		PlayerID:   "player_123",
		CampaignID: "winter_2026",
		RewardID:   "coins_1000",
		Status:     ClaimStatusClaimed,
		CreatedAt:  claimedAt,
		UpdatedAt:  claimedAt,
	}

	event := NewRewardClaimedEvent("event_123", claim)

	if event.SchemaVersion != rewardClaimedSchemaVersion {
		t.Fatalf("schema version = %d, want %d", event.SchemaVersion, rewardClaimedSchemaVersion)
	}
	if event.EventID != "event_123" {
		t.Fatalf("event ID = %q, want event_123", event.EventID)
	}
	if event.EventType != outboxEventTypeRewardClaimed {
		t.Fatalf("event type = %q, want %q", event.EventType, outboxEventTypeRewardClaimed)
	}
	if !event.OccurredAt.Equal(claimedAt) {
		t.Fatalf("occurred at = %s, want %s", event.OccurredAt, claimedAt)
	}

	if event.Claim.ClaimID != claim.ID {
		t.Fatalf("claim ID = %q, want %q", event.Claim.ClaimID, claim.ID)
	}
	if event.Claim.PlayerID != claim.PlayerID {
		t.Fatalf("player ID = %q, want %q", event.Claim.PlayerID, claim.PlayerID)
	}
	if event.Claim.CampaignID != claim.CampaignID {
		t.Fatalf("campaign ID = %q, want %q", event.Claim.CampaignID, claim.CampaignID)
	}
	if event.Claim.RewardID != claim.RewardID {
		t.Fatalf("reward ID = %q, want %q", event.Claim.RewardID, claim.RewardID)
	}
	if event.Claim.Status != ClaimStatusClaimed {
		t.Fatalf("claim status = %q, want %q", event.Claim.Status, ClaimStatusClaimed)
	}
	if !event.Claim.ClaimedAt.Equal(claimedAt) {
		t.Fatalf("claimed at = %s, want %s", event.Claim.ClaimedAt, claimedAt)
	}
}
