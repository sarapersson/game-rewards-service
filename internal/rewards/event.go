package rewards

import "time"

const (
	outboxAggregateTypeRewardClaim = "reward_claim"
	outboxEventTypeRewardClaimed   = "RewardClaimed"
	outboxStatusPending            = "pending"

	rewardClaimedSchemaVersion = 1
)

type RewardClaimedEvent struct {
	SchemaVersion int                     `json:"schema_version"`
	EventID       string                  `json:"event_id"`
	EventType     string                  `json:"event_type"`
	OccurredAt    time.Time               `json:"occurred_at"`
	Claim         RewardClaimedEventClaim `json:"claim"`
}

type RewardClaimedEventClaim struct {
	ClaimID    string    `json:"claim_id"`
	PlayerID   string    `json:"player_id"`
	CampaignID string    `json:"campaign_id"`
	RewardID   string    `json:"reward_id"`
	Status     string    `json:"status"`
	ClaimedAt  time.Time `json:"claimed_at"`
}

func NewRewardClaimedEvent(eventID string, claim Claim) RewardClaimedEvent {
	return RewardClaimedEvent{
		SchemaVersion: rewardClaimedSchemaVersion,
		EventID:       eventID,
		EventType:     outboxEventTypeRewardClaimed,
		OccurredAt:    claim.CreatedAt,
		Claim: RewardClaimedEventClaim{
			ClaimID:    claim.ID,
			PlayerID:   claim.PlayerID,
			CampaignID: claim.CampaignID,
			RewardID:   claim.RewardID,
			Status:     claim.Status,
			ClaimedAt:  claim.CreatedAt,
		},
	}
}
