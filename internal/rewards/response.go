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
	duplicateClaimErrorCode    = "reward_already_claimed"
	duplicateClaimErrorMessage = "Reward has already been claimed"
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

func validateStoredCreateClaimResponse(statusCode int, responseBody []byte, requested claimToCreate, linkedClaimID string) error {
	switch statusCode {
	case createClaimStatusCreated:
		var response createClaimResponse
		if !decodeStrictJSONResponse(responseBody, &response) {
			return fmt.Errorf("invalid stored reward claim response body: %w", ErrInternal)
		}

		if linkedClaimID == "" ||
			response.ClaimID != linkedClaimID ||
			!validUUID(response.ClaimID) ||
			response.PlayerID != requested.PlayerID ||
			response.CampaignID != requested.CampaignID ||
			response.RewardID != requested.RewardID ||
			response.Status != claimStatusClaimed {
			return fmt.Errorf("stored reward claim response does not match request: %w", ErrInternal)
		}

		if _, err := time.Parse(time.RFC3339Nano, response.ClaimedAt); err != nil {
			return fmt.Errorf("stored reward claim response has invalid claimed_at: %w", ErrInternal)
		}

	case createClaimStatusConflict:
		if linkedClaimID != "" {
			return fmt.Errorf("stored duplicate reward claim response has claim link: %w", ErrInternal)
		}

		var response errorResponse
		if !decodeStrictJSONResponse(responseBody, &response) {
			return fmt.Errorf("invalid stored duplicate reward claim response body: %w", ErrInternal)
		}

		if response.Error.Code != duplicateClaimErrorCode || response.Error.Message == "" {
			return fmt.Errorf("unexpected stored duplicate reward claim response: %w", ErrInternal)
		}

	default:
		return fmt.Errorf("unexpected stored reward claim response status %d: %w", statusCode, ErrInternal)
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

func marshalCreatedClaimResponse(claim claim) ([]byte, error) {
	body, err := json.Marshal(createClaimResponse{
		ClaimID:    claim.ID,
		PlayerID:   claim.PlayerID,
		CampaignID: claim.CampaignID,
		RewardID:   claim.RewardID,
		Status:     claimStatusClaimed,
		ClaimedAt:  claim.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal created reward claim response: %w", ErrInternal)
	}

	return body, nil
}

func marshalDuplicateClaimResponse() ([]byte, error) {
	body, err := json.Marshal(errorResponse{
		Error: errorBody{
			Code:    duplicateClaimErrorCode,
			Message: duplicateClaimErrorMessage,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal duplicate reward claim response: %w", ErrInternal)
	}

	return body, nil
}
