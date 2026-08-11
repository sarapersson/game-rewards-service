package rewards

import (
	"encoding/json"
	"errors"
	"testing"
)

const (
	responseTestClaimID   = "11111111-1111-4111-8111-111111111111"
	responseTestReplayID  = "22222222-2222-4222-8222-222222222222"
	responseTestClaimedAt = "2026-08-11T12:00:00.123456Z"
)

func TestValidateCreateClaimResultAcceptsValidResults(t *testing.T) {
	claim := responseValidationClaim()
	created := responseForClaim(claim)
	replayed := created
	replayed.ClaimID = responseTestReplayID

	duplicateBody, err := MarshalDuplicateClaimResponse()
	if err != nil {
		t.Fatalf("MarshalDuplicateClaimResponse returned error: %v", err)
	}
	replayedDuplicateBody := mustJSON(t, errorResponse{
		Error: errorBody{
			Code:    DuplicateClaimErrorCode,
			Message: "Reward was already claimed",
		},
	})

	tests := []struct {
		name   string
		result CreateClaimResult
	}{
		{
			name: "created",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusCreated,
				ResponseBody: mustJSON(t, created),
			},
		},
		{
			name: "replayed created",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusCreated,
				ResponseBody: mustJSON(t, replayed),
				Replayed:     true,
			},
		},
		{
			name: "duplicate",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusConflict,
				ResponseBody: duplicateBody,
			},
		},
		{
			name: "replayed duplicate with compatible message",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusConflict,
				ResponseBody: replayedDuplicateBody,
				Replayed:     true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateCreateClaimResult(tt.result, claim); err != nil {
				t.Fatalf("validateCreateClaimResult returned error: %v", err)
			}
		})
	}
}

func TestValidateCreateClaimResultRejectsInvalidResults(t *testing.T) {
	claim := responseValidationClaim()
	validCreated := responseForClaim(claim)
	validCreatedBody := mustJSON(t, validCreated)

	mismatchedNewClaimID := validCreated
	mismatchedNewClaimID.ClaimID = responseTestReplayID

	mismatchedReplay := validCreated
	mismatchedReplay.ClaimID = responseTestReplayID
	mismatchedReplay.PlayerID = "other-player"

	wrongStatus := validCreated
	wrongStatus.Status = "pending"

	invalidClaimedAt := validCreated
	invalidClaimedAt.ClaimedAt = "not-a-time"

	tests := []struct {
		name   string
		result CreateClaimResult
	}{
		{
			name: "unsupported status",
			result: CreateClaimResult{
				StatusCode:   200,
				ResponseBody: validCreatedBody,
			},
		},
		{
			name: "empty body",
			result: CreateClaimResult{
				StatusCode: CreateClaimStatusCreated,
			},
		},
		{
			name: "malformed JSON",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusCreated,
				ResponseBody: []byte(`{"claim_id":`),
			},
		},
		{
			name: "invalid UTF-8",
			result: CreateClaimResult{
				StatusCode: CreateClaimStatusConflict,
				ResponseBody: []byte(
					"{\"error\":{\"code\":\"reward_already_claimed\",\"message\":\"\xff\"}}",
				),
			},
		},
		{
			name: "trailing JSON value",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusCreated,
				ResponseBody: append(append([]byte(nil), validCreatedBody...), []byte(`{}`)...),
			},
		},
		{
			name: "created empty object",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusCreated,
				ResponseBody: []byte(`{}`),
			},
		},
		{
			name: "created unknown field",
			result: CreateClaimResult{
				StatusCode: CreateClaimStatusCreated,
				ResponseBody: []byte(`{"claim_id":"` + responseTestClaimID +
					`","player_id":"player-123","campaign_id":"campaign-123","reward_id":"reward-123","status":"claimed","claimed_at":"` + responseTestClaimedAt + `","extra":true}`),
			},
		},
		{
			name: "created mismatched new claim ID",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusCreated,
				ResponseBody: mustJSON(t, mismatchedNewClaimID),
			},
		},
		{
			name: "replayed created mismatched request",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusCreated,
				ResponseBody: mustJSON(t, mismatchedReplay),
				Replayed:     true,
			},
		},
		{
			name: "created wrong status",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusCreated,
				ResponseBody: mustJSON(t, wrongStatus),
			},
		},
		{
			name: "created invalid claimed_at",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusCreated,
				ResponseBody: mustJSON(t, invalidClaimedAt),
			},
		},
		{
			name: "duplicate missing message",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusConflict,
				ResponseBody: []byte(`{"error":{"code":"reward_already_claimed"}}`),
			},
		},
		{
			name: "duplicate wrong code",
			result: CreateClaimResult{
				StatusCode:   CreateClaimStatusConflict,
				ResponseBody: []byte(`{"error":{"code":"idempotency_key_reused","message":"Reward has already been claimed"}}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCreateClaimResult(tt.result, claim)
			if err == nil {
				t.Fatal("validateCreateClaimResult returned nil error")
			}

			if !errors.Is(err, ErrInternal) {
				t.Fatalf("validateCreateClaimResult error = %v, want ErrInternal", err)
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

func responseValidationClaim() Claim {
	return Claim{
		ID:         responseTestClaimID,
		PlayerID:   "player-123",
		CampaignID: "campaign-123",
		RewardID:   "reward-123",
		Status:     ClaimStatusClaimed,
	}
}

func responseForClaim(claim Claim) createClaimResponse {
	return createClaimResponse{
		ClaimID:    claim.ID,
		PlayerID:   claim.PlayerID,
		CampaignID: claim.CampaignID,
		RewardID:   claim.RewardID,
		Status:     ClaimStatusClaimed,
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
