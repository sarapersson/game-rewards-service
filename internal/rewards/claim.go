// Package rewards implements the reward-claim use case and its persistence logic.
package rewards

import (
	"crypto/sha256"
	"time"
)

const (
	// maxIDLength matches the database constraints for player, campaign, and reward identifiers.
	maxIDLength = 128

	maxIdempotencyKeyLength = 255

	createClaimStatusCreated  = 201
	createClaimStatusConflict = 409
)

type claim struct {
	ID         string
	PlayerID   string
	CampaignID string
	RewardID   string
	CreatedAt  time.Time
}

type CreateClaimCommand struct {
	PlayerID       string
	CampaignID     string
	RewardID       string
	IdempotencyKey string
}

type CreateClaimResult struct {
	StatusCode   int
	ResponseBody []byte
	Replayed     bool
}

type createClaimParams struct {
	PlayerID   string
	CampaignID string
	RewardID   string
	KeyHash    [sha256.Size]byte
}
