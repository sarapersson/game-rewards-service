// Package rewards contains reward claim domain logic and persistence boundaries.
package rewards

import "time"

const (
	// MaxIDLength matches the database constraints for player and reward identifiers.
	MaxIDLength = 128

	ClaimStatusClaimed = "claimed"
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
	PlayerID   string
	CampaignID string
	RewardID   string
}
