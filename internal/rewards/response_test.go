package rewards

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

const (
	responseTestClaimID   = "11111111-1111-4111-8111-111111111111"
	responseTestReplayID  = "22222222-2222-4222-8222-222222222222"
	responseTestClaimedAt = "2026-08-11T12:00:00.123456Z"
)

func TestMarshalCreatedClaimResponseGolden(t *testing.T) {
	claimedAt := time.Date(2026, 8, 16, 13, 45, 12, 123456000, time.UTC)

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

	want := []byte(`{"claim_id":"11111111-1111-4111-8111-111111111111","player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123","status":"claimed","claimed_at":"2026-08-16T13:45:12.123456Z"}`)
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

func TestValidateStoredCreateClaimResponseAcceptsCompatibleResponses(t *testing.T) {
	claim := responseValidationClaim()
	created := responseForClaim(claim)
	created.ClaimID = responseTestReplayID

	duplicateBody, err := marshalDuplicateClaimResponse()
	if err != nil {
		t.Fatalf("marshalDuplicateClaimResponse returned error: %v", err)
	}
	compatibleDuplicateBody := mustJSON(t, errorResponse{
		Error: errorBody{
			Code:    duplicateClaimErrorCode,
			Message: "Reward was already claimed",
		},
	})

	tests := []struct {
		name          string
		statusCode    int
		body          []byte
		linkedClaimID string
	}{
		{
			name:          "created",
			statusCode:    createClaimStatusCreated,
			body:          mustJSON(t, created),
			linkedClaimID: responseTestReplayID,
		},
		{
			name:       "duplicate",
			statusCode: createClaimStatusConflict,
			body:       duplicateBody,
		},
		{
			name:       "duplicate with compatible historical message",
			statusCode: createClaimStatusConflict,
			body:       compatibleDuplicateBody,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateStoredCreateClaimResponse(tt.statusCode, tt.body, claim, tt.linkedClaimID); err != nil {
				t.Fatalf("validateStoredCreateClaimResponse returned error: %v", err)
			}
		})
	}
}

func TestValidateStoredCreateClaimResponseRejectsInvalidResponses(t *testing.T) {
	claim := responseValidationClaim()
	validCreated := responseForClaim(claim)
	validCreatedBody := mustJSON(t, validCreated)

	mismatchedRequest := validCreated
	mismatchedRequest.PlayerID = "other-player"

	wrongStatus := validCreated
	wrongStatus.Status = "pending"

	invalidClaimedAt := validCreated
	invalidClaimedAt.ClaimedAt = "not-a-time"

	tests := []struct {
		name          string
		statusCode    int
		body          []byte
		linkedClaimID string
	}{
		{
			name:       "unsupported status",
			statusCode: 200,
			body:       validCreatedBody,
		},
		{
			name:       "created missing claim link",
			statusCode: createClaimStatusCreated,
			body:       validCreatedBody,
		},
		{
			name:          "created mismatched claim link",
			statusCode:    createClaimStatusCreated,
			body:          validCreatedBody,
			linkedClaimID: responseTestReplayID,
		},
		{
			name:          "duplicate with claim link",
			statusCode:    createClaimStatusConflict,
			body:          []byte(`{"error":{"code":"reward_already_claimed","message":"Reward has already been claimed"}}`),
			linkedClaimID: responseTestClaimID,
		},
		{
			name:          "empty body",
			statusCode:    createClaimStatusCreated,
			linkedClaimID: responseTestClaimID,
		},
		{
			name:          "malformed JSON",
			statusCode:    createClaimStatusCreated,
			linkedClaimID: responseTestClaimID,
			body:          []byte(`{"claim_id":`),
		},
		{
			name:       "invalid UTF-8",
			statusCode: createClaimStatusConflict,
			body: []byte(
				"{\"error\":{\"code\":\"reward_already_claimed\",\"message\":\"\xff\"}}",
			),
		},
		{
			name:          "trailing JSON value",
			statusCode:    createClaimStatusCreated,
			linkedClaimID: responseTestClaimID,
			body:          append(append([]byte(nil), validCreatedBody...), []byte(`{}`)...),
		},
		{
			name:          "created empty object",
			statusCode:    createClaimStatusCreated,
			linkedClaimID: responseTestClaimID,
			body:          []byte(`{}`),
		},
		{
			name:          "created unknown field",
			statusCode:    createClaimStatusCreated,
			linkedClaimID: responseTestClaimID,
			body: []byte(`{"claim_id":"` + responseTestClaimID +
				`","player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123","status":"claimed","claimed_at":"` + responseTestClaimedAt + `","extra":true}`),
		},
		{
			name:          "created mismatched request",
			statusCode:    createClaimStatusCreated,
			linkedClaimID: responseTestClaimID,
			body:          mustJSON(t, mismatchedRequest),
		},
		{
			name:          "created wrong status",
			statusCode:    createClaimStatusCreated,
			linkedClaimID: responseTestClaimID,
			body:          mustJSON(t, wrongStatus),
		},
		{
			name:          "created invalid claimed_at",
			statusCode:    createClaimStatusCreated,
			linkedClaimID: responseTestClaimID,
			body:          mustJSON(t, invalidClaimedAt),
		},
		{
			name:       "duplicate missing message",
			statusCode: createClaimStatusConflict,
			body:       []byte(`{"error":{"code":"reward_already_claimed"}}`),
		},
		{
			name:       "duplicate wrong code",
			statusCode: createClaimStatusConflict,
			body:       []byte(`{"error":{"code":"idempotency_key_reused","message":"Reward has already been claimed"}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStoredCreateClaimResponse(tt.statusCode, tt.body, claim, tt.linkedClaimID)
			if err == nil {
				t.Fatal("validateStoredCreateClaimResponse returned nil error")
			}

			if !errors.Is(err, ErrInternal) {
				t.Fatalf("validateStoredCreateClaimResponse error = %v, want ErrInternal", err)
			}
		})
	}
}

func TestValidUUID(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "canonical", value: responseTestClaimID, want: true},
		{name: "invalid hex", value: "11111111-1111-4111-8111-11111111111g"},
		{name: "extra hyphen", value: "11111111-1111-4111-8111-1111111111--"},
		{name: "wrong length", value: "11111111-1111-4111-8111-11111111111"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validUUID(tt.value); got != tt.want {
				t.Fatalf("validUUID(%q) = %t, want %t", tt.value, got, tt.want)
			}
		})
	}
}

func responseValidationClaim() claimToCreate {
	return claimToCreate{
		ID:         responseTestClaimID,
		PlayerID:   "player-123",
		CampaignID: "campaign-123",
		RewardID:   "reward-123",
	}
}

func responseForClaim(claim claimToCreate) createClaimResponse {
	return createClaimResponse{
		ClaimID:    claim.ID,
		PlayerID:   claim.PlayerID,
		CampaignID: claim.CampaignID,
		RewardID:   claim.RewardID,
		Status:     claimStatusClaimed,
		ClaimedAt:  responseTestClaimedAt,
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	return body
}
