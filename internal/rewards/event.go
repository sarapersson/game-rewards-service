package rewards

import "time"

const (
	outboxAggregateTypeRewardClaim = "reward_claim"
	outboxEventTypeRewardClaimed   = "RewardClaimed"

	rewardClaimedSchemaVersion = 1
)

type rewardClaimedEvent struct {
	SchemaVersion int                     `json:"schema_version"`
	EventID       string                  `json:"event_id"`
	EventType     string                  `json:"event_type"`
	OccurredAt    time.Time               `json:"occurred_at"`
	Claim         rewardClaimedEventClaim `json:"claim"`
}

type rewardClaimedEventClaim struct {
	ClaimID    string    `json:"claim_id"`
	PlayerID   string    `json:"player_id"`
	CampaignID string    `json:"campaign_id"`
	RewardID   string    `json:"reward_id"`
	ClaimedAt  time.Time `json:"claimed_at"`
}

func newRewardClaimedEvent(eventID string, claim claim) rewardClaimedEvent {
	return rewardClaimedEvent{
		SchemaVersion: rewardClaimedSchemaVersion,
		EventID:       eventID,
		EventType:     outboxEventTypeRewardClaimed,
		OccurredAt:    claim.CreatedAt.UTC(),
		Claim: rewardClaimedEventClaim{
			ClaimID:    claim.ID,
			PlayerID:   claim.PlayerID,
			CampaignID: claim.CampaignID,
			RewardID:   claim.RewardID,
			ClaimedAt:  claim.CreatedAt.UTC(),
		},
	}
}
