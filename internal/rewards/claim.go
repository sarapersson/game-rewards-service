// Package rewards implements the reward-claim use case and its persistence logic.
package rewards

import (
	"crypto/sha256"
	"time"
)

const (
	// maxIDLength matches the database constraints for player, campaign, and reward identifiers.
	maxIDLength = 128

	claimStatusClaimed = "claimed"

	createClaimStatusCreated  = 201
	createClaimStatusConflict = 409
)

type claimToCreate struct {
	ID         string
	PlayerID   string
	CampaignID string
	RewardID   string
}

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
	Claim       claimToCreate
	KeyHash     [sha256.Size]byte
	RequestHash [sha256.Size]byte
}
