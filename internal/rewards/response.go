package rewards

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DuplicateClaimErrorCode    = "reward_already_claimed"
	DuplicateClaimErrorMessage = "Reward has already been claimed"
)

type createClaimResponse struct {
	ClaimID    string `json:"claim_id"`
	PlayerID   string `json:"player_id"`
	CampaignID string `json:"campaign_id"`
	RewardID   string `json:"reward_id"`
	Status     string `json:"status"`
	ClaimedAt  string `json:"claimed_at"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func validateCreateClaimResult(result CreateClaimResult, claim Claim) error {
	switch result.StatusCode {
	case CreateClaimStatusCreated:
		var response createClaimResponse
		if !decodeStrictJSONResponse(result.ResponseBody, &response) {
			return fmt.Errorf("invalid created reward claim response body: %w", ErrInternal)
		}

		if !validUUID(response.ClaimID) ||
			(!result.Replayed && response.ClaimID != claim.ID) ||
			response.PlayerID != claim.PlayerID ||
			response.CampaignID != claim.CampaignID ||
			response.RewardID != claim.RewardID ||
			response.Status != ClaimStatusClaimed {
			return fmt.Errorf("created reward claim response does not match claim: %w", ErrInternal)
		}

		if _, err := time.Parse(time.RFC3339Nano, response.ClaimedAt); err != nil {
			return fmt.Errorf("created reward claim response has invalid claimed_at: %w", ErrInternal)
		}

	case CreateClaimStatusConflict:
		var response errorResponse
		if !decodeStrictJSONResponse(result.ResponseBody, &response) {
			return fmt.Errorf("invalid duplicate reward claim response body: %w", ErrInternal)
		}

		if response.Error.Code != DuplicateClaimErrorCode || response.Error.Message == "" {
			return fmt.Errorf("unexpected duplicate reward claim response: %w", ErrInternal)
		}

	default:
		return fmt.Errorf("unexpected reward claim response status %d: %w", result.StatusCode, ErrInternal)
	}

	return nil
}

func decodeStrictJSONResponse(body []byte, dst any) bool {
	if len(body) == 0 || !utf8.Valid(body) {
		return false
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return false
	}

	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}

	encoded := strings.ReplaceAll(value, "-", "")
	if len(encoded) != 32 {
		return false
	}

	_, err := hex.DecodeString(encoded)
	return err == nil
}

func MarshalCreatedClaimResponse(claim Claim) ([]byte, error) {
	body, err := json.Marshal(createClaimResponse{
		ClaimID:    claim.ID,
		PlayerID:   claim.PlayerID,
		CampaignID: claim.CampaignID,
		RewardID:   claim.RewardID,
		Status:     claim.Status,
		ClaimedAt:  claim.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal created reward claim response: %w", ErrInternal)
	}

	return body, nil
}

func MarshalDuplicateClaimResponse() ([]byte, error) {
	body, err := json.Marshal(errorResponse{
		Error: errorBody{
			Code:    DuplicateClaimErrorCode,
			Message: DuplicateClaimErrorMessage,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal duplicate reward claim response: %w", ErrInternal)
	}

	return body, nil
}
