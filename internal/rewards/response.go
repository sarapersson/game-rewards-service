package rewards

import (
	"encoding/json"
	"fmt"
	"time"
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
