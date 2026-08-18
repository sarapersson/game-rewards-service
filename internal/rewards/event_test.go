package rewards

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestRewardClaimedEventJSONContract(t *testing.T) {
	claimedAt := time.Date(2026, 7, 8, 14, 34, 56, 123456000, time.FixedZone("UTC+2", 2*60*60))

	event := newRewardClaimedEvent("event_123", claim{
		ID:         "claim_123",
		PlayerID:   "player_123",
		CampaignID: "winter_2026",
		RewardID:   "coins_1000",
		CreatedAt:  claimedAt,
	})

	actual, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal RewardClaimed event: %v", err)
	}

	expected := []byte(`{
		"schema_version": 1,
		"event_id": "event_123",
		"event_type": "RewardClaimed",
		"occurred_at": "2026-07-08T12:34:56.123456Z",
		"claim": {
			"claim_id": "claim_123",
			"player_id": "player_123",
			"campaign_id": "winter_2026",
			"reward_id": "coins_1000",
			"claimed_at": "2026-07-08T12:34:56.123456Z"
		}
	}`)

	assertJSONSemanticallyEqual(t, actual, expected)
}

func assertJSONSemanticallyEqual(t *testing.T, actual, expected []byte) {
	t.Helper()

	var actualValue any
	if err := json.Unmarshal(actual, &actualValue); err != nil {
		t.Fatalf("unmarshal actual JSON: %v; JSON = %s", err, actual)
	}

	var expectedValue any
	if err := json.Unmarshal(expected, &expectedValue); err != nil {
		t.Fatalf("unmarshal expected JSON: %v; JSON = %s", err, expected)
	}

	if !reflect.DeepEqual(actualValue, expectedValue) {
		t.Fatalf("JSON mismatch:\nactual:   %s\nexpected: %s", actual, expected)
	}
}
