package rewards

import (
	"bytes"
	"testing"
	"time"
)

const responseTestClaimID = "11111111-1111-4111-8111-111111111111"

func TestMarshalCreatedClaimResponseGolden(t *testing.T) {
	claimedAt := time.Date(2026, 8, 16, 15, 45, 12, 123456000, time.FixedZone("UTC+2", 2*60*60))

	body, err := marshalCreatedClaimResponse(claim{
		ID:         responseTestClaimID,
		PlayerID:   "player-123",
		CampaignID: "campaign-123",
		RewardID:   "reward-123",
		CreatedAt:  claimedAt,
	})
	if err != nil {
		t.Fatalf("marshalCreatedClaimResponse returned error: %v", err)
	}

	want := []byte(`{"claim_id":"11111111-1111-4111-8111-111111111111","player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123","claimed_at":"2026-08-16T13:45:12.123456Z"}`)
	if !bytes.Equal(body, want) {
		t.Fatalf("created response = %s, want %s", body, want)
	}
}

func TestMarshalDuplicateClaimResponseGolden(t *testing.T) {
	body, err := marshalDuplicateClaimResponse()
	if err != nil {
		t.Fatalf("marshalDuplicateClaimResponse returned error: %v", err)
	}

	want := []byte(`{"error":{"code":"reward_already_claimed","message":"Reward has already been claimed"}}`)
	if !bytes.Equal(body, want) {
		t.Fatalf("duplicate response = %s, want %s", body, want)
	}
}
