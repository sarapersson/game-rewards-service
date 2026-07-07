//go:build integration

package rewards

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresStoreInsertClaim(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	claimID := mustUUID(t)
	playerID := "player-" + strings.ReplaceAll(t.Name(), "/", "-")
	campaignID := "campaign-" + strings.ReplaceAll(t.Name(), "/", "-")
	rewardID := "reward-" + strings.ReplaceAll(t.Name(), "/", "-")

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM reward_claims WHERE id = $1", claimID)
	})

	claim, err := store.InsertClaim(context.Background(), Claim{
		ID:         claimID,
		PlayerID:   playerID,
		CampaignID: campaignID,
		RewardID:   rewardID,
		Status:     ClaimStatusClaimed,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if claim.ID != claimID {
		t.Fatalf("expected id %q, got %q", claimID, claim.ID)
	}

	if claim.PlayerID != playerID {
		t.Fatalf("expected player_id %q, got %q", playerID, claim.PlayerID)
	}

	if claim.CampaignID != campaignID {
		t.Fatalf("expected campaign_id %q, got %q", campaignID, claim.CampaignID)
	}

	if claim.RewardID != rewardID {
		t.Fatalf("expected reward_id %q, got %q", rewardID, claim.RewardID)
	}

	if claim.Status != ClaimStatusClaimed {
		t.Fatalf("expected status %q, got %q", ClaimStatusClaimed, claim.Status)
	}

	if claim.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be set by database")
	}

	if claim.UpdatedAt.IsZero() {
		t.Fatal("expected updated_at to be set by database")
	}
}

func TestPostgresStoreInsertClaimMapsDuplicateReward(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	firstID := mustUUID(t)
	secondID := mustUUID(t)
	playerID := "player-" + strings.ReplaceAll(t.Name(), "/", "-")
	campaignID := "campaign-" + strings.ReplaceAll(t.Name(), "/", "-")
	rewardID := "reward-" + strings.ReplaceAll(t.Name(), "/", "-")

	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM reward_claims WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3",
			playerID,
			campaignID,
			rewardID,
		)
	})

	_, err := store.InsertClaim(context.Background(), Claim{
		ID:         firstID,
		PlayerID:   playerID,
		CampaignID: campaignID,
		RewardID:   rewardID,
		Status:     ClaimStatusClaimed,
	})
	if err != nil {
		t.Fatalf("expected first insert to succeed, got %v", err)
	}

	_, err = store.InsertClaim(context.Background(), Claim{
		ID:         secondID,
		PlayerID:   playerID,
		CampaignID: campaignID,
		RewardID:   rewardID,
		Status:     ClaimStatusClaimed,
	})
	if !errors.Is(err, ErrDuplicateClaim) {
		t.Fatalf("expected ErrDuplicateClaim, got %v", err)
	}
}

func TestPostgresStoreAllowsSameRewardInDifferentCampaigns(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	firstID := mustUUID(t)
	secondID := mustUUID(t)
	playerID := "player-" + strings.ReplaceAll(t.Name(), "/", "-")
	firstCampaignID := "campaign-winter-" + strings.ReplaceAll(t.Name(), "/", "-")
	secondCampaignID := "campaign-spring-" + strings.ReplaceAll(t.Name(), "/", "-")
	rewardID := "reward-" + strings.ReplaceAll(t.Name(), "/", "-")

	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM reward_claims WHERE player_id = $1 AND reward_id = $2",
			playerID,
			rewardID,
		)
	})

	_, err := store.InsertClaim(context.Background(), Claim{
		ID:         firstID,
		PlayerID:   playerID,
		CampaignID: firstCampaignID,
		RewardID:   rewardID,
		Status:     ClaimStatusClaimed,
	})
	if err != nil {
		t.Fatalf("expected first insert to succeed, got %v", err)
	}

	claim, err := store.InsertClaim(context.Background(), Claim{
		ID:         secondID,
		PlayerID:   playerID,
		CampaignID: secondCampaignID,
		RewardID:   rewardID,
		Status:     ClaimStatusClaimed,
	})
	if err != nil {
		t.Fatalf("expected same reward in different campaign to succeed, got %v", err)
	}

	if claim.ID != secondID {
		t.Fatalf("expected id %q, got %q", secondID, claim.ID)
	}

	if claim.CampaignID != secondCampaignID {
		t.Fatalf("expected campaign_id %q, got %q", secondCampaignID, claim.CampaignID)
	}
}

func openIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if strings.TrimSpace(databaseURL) == "" {
		databaseURL = "postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open postgres pool: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping postgres: %v", err)
	}

	t.Cleanup(pool.Close)

	return pool
}

func mustUUID(t *testing.T) string {
	t.Helper()

	id, err := NewUUIDV4()
	if err != nil {
		t.Fatalf("generate uuid: %v", err)
	}

	return id
}
