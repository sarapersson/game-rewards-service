// Package rewards contains reward claim domain logic and persistence boundaries.
package rewards

import "time"

const (
	// MaxIDLength matches the database constraints for player, campaign, and reward identifiers.
	MaxIDLength = 128

	ClaimStatusClaimed = "claimed"

	CreateClaimStatusCreated  = 201
	CreateClaimStatusConflict = 409
)

type Claim struct {
	ID         string
	PlayerID   string
	CampaignID string
	RewardID   string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
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

type CreateClaimStoreCommand struct {
	Claim       Claim
	Operation   string
	KeyHash     []byte
	RequestHash []byte
}
