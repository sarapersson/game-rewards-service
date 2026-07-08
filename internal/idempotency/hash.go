// Package idempotency provides deterministic hashing helpers for retry-safe API requests.
package idempotency

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
)

const (
	RewardClaimOperation = "POST /v1/reward-claims"

	requestHashVersion = "reward-claim-request/v1"
	keyHashSize        = sha256.Size
)

var ErrInvalidKey = errors.New("invalid idempotency key")

type RewardClaimRequest struct {
	PlayerID   string
	CampaignID string
	RewardID   string
}

func HashKey(key string) ([keyHashSize]byte, error) {
	normalized := strings.TrimSpace(key)
	if normalized == "" || len(normalized) > 255 {
		return [keyHashSize]byte{}, ErrInvalidKey
	}

	for _, r := range normalized {
		if r < 0x20 || r == 0x7f {
			return [keyHashSize]byte{}, ErrInvalidKey
		}
	}

	return sha256.Sum256([]byte(normalized)), nil
}

func HashRewardClaimRequest(req RewardClaimRequest) ([keyHashSize]byte, error) {
	payload := struct {
		Version    string `json:"version"`
		Operation  string `json:"operation"`
		PlayerID   string `json:"player_id"`
		CampaignID string `json:"campaign_id"`
		RewardID   string `json:"reward_id"`
	}{
		Version:    requestHashVersion,
		Operation:  RewardClaimOperation,
		PlayerID:   req.PlayerID,
		CampaignID: req.CampaignID,
		RewardID:   req.RewardID,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return [keyHashSize]byte{}, err
	}

	return sha256.Sum256(b), nil
}
