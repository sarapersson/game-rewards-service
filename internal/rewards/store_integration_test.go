//go:build integration

package rewards

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sarapersson/game-rewards-service/internal/idempotency"
)

const defaultIntegrationDatabaseURL = "postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable"

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

func TestPostgresStoreCreateClaimCompletesIdempotencyKey(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	playerID := "player-" + strings.ReplaceAll(t.Name(), "/", "-")
	campaignID := "campaign-" + strings.ReplaceAll(t.Name(), "/", "-")
	rewardID := "reward-" + strings.ReplaceAll(t.Name(), "/", "-")
	cmd := newIntegrationCreateClaimCommand(t, "claim-key-"+t.Name(), playerID, campaignID, rewardID)

	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
			cmd.Operation,
			cmd.KeyHash,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM reward_claims WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3",
			playerID,
			campaignID,
			rewardID,
		)
	})

	result, err := store.CreateClaim(context.Background(), cmd)
	if err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}

	if result.StatusCode != CreateClaimStatusCreated {
		t.Fatalf("status = %d, want %d", result.StatusCode, CreateClaimStatusCreated)
	}

	if result.Replayed {
		t.Fatal("first CreateClaim call should not be replayed")
	}

	if len(result.ResponseBody) == 0 {
		t.Fatal("response body is empty")
	}

	var state string
	var responseStatus int
	var completedAt time.Time
	err = pool.QueryRow(
		context.Background(),
		`
SELECT state, response_status, completed_at
FROM idempotency_keys
WHERE operation = $1 AND key_hash = $2`,
		cmd.Operation,
		cmd.KeyHash,
	).Scan(&state, &responseStatus, &completedAt)
	if err != nil {
		t.Fatalf("query idempotency key: %v", err)
	}

	if state != idempotencyStateCompleted {
		t.Fatalf("state = %q, want %q", state, idempotencyStateCompleted)
	}

	if responseStatus != CreateClaimStatusCreated {
		t.Fatalf("response_status = %d, want %d", responseStatus, CreateClaimStatusCreated)
	}

	if completedAt.IsZero() {
		t.Fatal("completed_at is zero")
	}

	var claimCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM reward_claims
WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3`,
		playerID,
		campaignID,
		rewardID,
	).Scan(&claimCount)
	if err != nil {
		t.Fatalf("count reward claims: %v", err)
	}

	if claimCount != 1 {
		t.Fatalf("reward claim count = %d, want 1", claimCount)
	}
}

func TestPostgresStoreCreateClaimReplaysCompletedResponse(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	playerID := "player-" + strings.ReplaceAll(t.Name(), "/", "-")
	campaignID := "campaign-" + strings.ReplaceAll(t.Name(), "/", "-")
	rewardID := "reward-" + strings.ReplaceAll(t.Name(), "/", "-")
	first := newIntegrationCreateClaimCommand(t, "claim-key-"+t.Name(), playerID, campaignID, rewardID)
	replay := first
	replay.Claim.ID = mustUUID(t)

	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
			first.Operation,
			first.KeyHash,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM reward_claims WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3",
			playerID,
			campaignID,
			rewardID,
		)
	})

	firstResult, err := store.CreateClaim(context.Background(), first)
	if err != nil {
		t.Fatalf("first CreateClaim returned error: %v", err)
	}

	replayResult, err := store.CreateClaim(context.Background(), replay)
	if err != nil {
		t.Fatalf("replay CreateClaim returned error: %v", err)
	}

	if replayResult.StatusCode != firstResult.StatusCode {
		t.Fatalf("replay status = %d, want %d", replayResult.StatusCode, firstResult.StatusCode)
	}

	if string(replayResult.ResponseBody) != string(firstResult.ResponseBody) {
		t.Fatalf("replay response body = %s, want %s", replayResult.ResponseBody, firstResult.ResponseBody)
	}

	if !replayResult.Replayed {
		t.Fatal("replay result should be marked replayed")
	}

	var claimCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM reward_claims
WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3`,
		playerID,
		campaignID,
		rewardID,
	).Scan(&claimCount)
	if err != nil {
		t.Fatalf("count reward claims: %v", err)
	}

	if claimCount != 1 {
		t.Fatalf("reward claim count = %d, want 1", claimCount)
	}
}

func TestPostgresStoreCreateClaimRejectsKeyReuseWithDifferentPayload(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	playerID := "player-" + strings.ReplaceAll(t.Name(), "/", "-")
	campaignID := "campaign-" + strings.ReplaceAll(t.Name(), "/", "-")
	rewardID := "reward-" + strings.ReplaceAll(t.Name(), "/", "-")
	first := newIntegrationCreateClaimCommand(t, "claim-key-"+t.Name(), playerID, campaignID, rewardID)

	mismatch := first
	mismatch.Claim.ID = mustUUID(t)
	mismatch.Claim.RewardID = rewardID + "-different"
	mismatch.RequestHash = []byte("different-request-hash-32-bytes!")

	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
			first.Operation,
			first.KeyHash,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM reward_claims WHERE player_id = $1 AND campaign_id = $2",
			playerID,
			campaignID,
		)
	})

	_, err := store.CreateClaim(context.Background(), first)
	if err != nil {
		t.Fatalf("first CreateClaim returned error: %v", err)
	}

	_, err = store.CreateClaim(context.Background(), mismatch)
	if !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("CreateClaim error = %v, want %v", err, ErrIdempotencyKeyReused)
	}

	var claimCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM reward_claims
WHERE player_id = $1 AND campaign_id = $2`,
		playerID,
		campaignID,
	).Scan(&claimCount)
	if err != nil {
		t.Fatalf("count reward claims: %v", err)
	}

	if claimCount != 1 {
		t.Fatalf("reward claim count = %d, want 1", claimCount)
	}
}

func TestPostgresStoreCreateClaimStoresDuplicateRewardResponse(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	playerID := "player-" + strings.ReplaceAll(t.Name(), "/", "-")
	campaignID := "campaign-" + strings.ReplaceAll(t.Name(), "/", "-")
	rewardID := "reward-" + strings.ReplaceAll(t.Name(), "/", "-")
	first := newIntegrationCreateClaimCommand(t, "claim-key-first-"+t.Name(), playerID, campaignID, rewardID)
	duplicate := newIntegrationCreateClaimCommand(t, "claim-key-duplicate-"+t.Name(), playerID, campaignID, rewardID)

	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
			first.Operation,
			first.KeyHash,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
			duplicate.Operation,
			duplicate.KeyHash,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM reward_claims WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3",
			playerID,
			campaignID,
			rewardID,
		)
	})

	_, err := store.CreateClaim(context.Background(), first)
	if err != nil {
		t.Fatalf("first CreateClaim returned error: %v", err)
	}

	result, err := store.CreateClaim(context.Background(), duplicate)
	if err != nil {
		t.Fatalf("duplicate CreateClaim returned error: %v", err)
	}

	if result.StatusCode != CreateClaimStatusConflict {
		t.Fatalf("duplicate status = %d, want %d", result.StatusCode, CreateClaimStatusConflict)
	}

	if result.Replayed {
		t.Fatal("first duplicate response should not be replayed")
	}

	var duplicateBody errorResponse
	if err := json.Unmarshal(result.ResponseBody, &duplicateBody); err != nil {
		t.Fatalf("unmarshal duplicate response: %v; body = %s", err, result.ResponseBody)
	}

	if duplicateBody.Error.Code != DuplicateClaimErrorCode {
		t.Fatalf("duplicate error code = %q, want %q", duplicateBody.Error.Code, DuplicateClaimErrorCode)
	}

	var state string
	var responseStatus int
	var responseBody []byte
	err = pool.QueryRow(
		context.Background(),
		`
SELECT state, response_status, response_body
FROM idempotency_keys
WHERE operation = $1 AND key_hash = $2`,
		duplicate.Operation,
		duplicate.KeyHash,
	).Scan(&state, &responseStatus, &responseBody)
	if err != nil {
		t.Fatalf("query duplicate idempotency key: %v", err)
	}

	if state != idempotencyStateCompleted {
		t.Fatalf("duplicate state = %q, want %q", state, idempotencyStateCompleted)
	}

	if responseStatus != CreateClaimStatusConflict {
		t.Fatalf("duplicate response_status = %d, want %d", responseStatus, CreateClaimStatusConflict)
	}

	if string(responseBody) != string(result.ResponseBody) {
		t.Fatalf("stored duplicate response body = %s, want %s", responseBody, result.ResponseBody)
	}

	var claimCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM reward_claims
WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3`,
		playerID,
		campaignID,
		rewardID,
	).Scan(&claimCount)
	if err != nil {
		t.Fatalf("count reward claims: %v", err)
	}

	if claimCount != 1 {
		t.Fatalf("reward claim count = %d, want 1", claimCount)
	}
}

func TestPostgresStoreCreateClaimReplaysDuplicateRewardResponse(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	playerID := "player-" + strings.ReplaceAll(t.Name(), "/", "-")
	campaignID := "campaign-" + strings.ReplaceAll(t.Name(), "/", "-")
	rewardID := "reward-" + strings.ReplaceAll(t.Name(), "/", "-")
	first := newIntegrationCreateClaimCommand(t, "claim-key-first-"+t.Name(), playerID, campaignID, rewardID)
	duplicate := newIntegrationCreateClaimCommand(t, "claim-key-duplicate-"+t.Name(), playerID, campaignID, rewardID)
	duplicateReplay := duplicate
	duplicateReplay.Claim.ID = mustUUID(t)

	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
			first.Operation,
			first.KeyHash,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
			duplicate.Operation,
			duplicate.KeyHash,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM reward_claims WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3",
			playerID,
			campaignID,
			rewardID,
		)
	})

	_, err := store.CreateClaim(context.Background(), first)
	if err != nil {
		t.Fatalf("first CreateClaim returned error: %v", err)
	}

	duplicateResult, err := store.CreateClaim(context.Background(), duplicate)
	if err != nil {
		t.Fatalf("duplicate CreateClaim returned error: %v", err)
	}

	replayResult, err := store.CreateClaim(context.Background(), duplicateReplay)
	if err != nil {
		t.Fatalf("duplicate replay CreateClaim returned error: %v", err)
	}

	if replayResult.StatusCode != duplicateResult.StatusCode {
		t.Fatalf("replay status = %d, want %d", replayResult.StatusCode, duplicateResult.StatusCode)
	}

	if string(replayResult.ResponseBody) != string(duplicateResult.ResponseBody) {
		t.Fatalf("replay response body = %s, want %s", replayResult.ResponseBody, duplicateResult.ResponseBody)
	}

	if !replayResult.Replayed {
		t.Fatal("duplicate replay result should be marked replayed")
	}

	var claimCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM reward_claims
WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3`,
		playerID,
		campaignID,
		rewardID,
	).Scan(&claimCount)
	if err != nil {
		t.Fatalf("count reward claims: %v", err)
	}

	if claimCount != 1 {
		t.Fatalf("reward claim count = %d, want 1", claimCount)
	}
}

func TestPostgresStoreCreateClaimPreventsDuplicateRewardsConcurrently(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 5*time.Second)

	playerID := "player-" + strings.ReplaceAll(t.Name(), "/", "-")
	campaignID := "campaign-" + strings.ReplaceAll(t.Name(), "/", "-")
	rewardID := "reward-" + strings.ReplaceAll(t.Name(), "/", "-")

	const attempts = 8

	cmds := make([]CreateClaimStoreCommand, attempts)
	for i := range cmds {
		cmds[i] = newIntegrationCreateClaimCommand(
			t,
			"claim-key-"+strings.ReplaceAll(t.Name(), "/", "-")+"-"+strconv.Itoa(i),
			playerID,
			campaignID,
			rewardID,
		)
	}

	t.Cleanup(func() {
		for _, cmd := range cmds {
			_, _ = pool.Exec(
				context.Background(),
				"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
				cmd.Operation,
				cmd.KeyHash,
			)
		}

		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM reward_claims WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3",
			playerID,
			campaignID,
			rewardID,
		)
	})

	start := make(chan struct{})
	results := make(chan CreateClaimResult, attempts)
	errs := make(chan error, attempts)

	var wg sync.WaitGroup
	for _, cmd := range cmds {
		cmd := cmd

		wg.Add(1)
		go func() {
			defer wg.Done()

			<-start

			result, err := store.CreateClaim(context.Background(), cmd)
			if err != nil {
				errs <- err
				return
			}

			results <- result
		}()
	}

	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent CreateClaim returned error: %v", err)
		}
	}

	var createdCount int
	var conflictCount int
	for result := range results {
		switch result.StatusCode {
		case CreateClaimStatusCreated:
			createdCount++
		case CreateClaimStatusConflict:
			conflictCount++
		default:
			t.Fatalf("unexpected status = %d; body = %s", result.StatusCode, result.ResponseBody)
		}

		if result.Replayed {
			t.Fatal("concurrent CreateClaim with distinct keys should not be replayed")
		}
	}

	if createdCount != 1 {
		t.Fatalf("created responses = %d, want 1", createdCount)
	}

	if conflictCount != attempts-1 {
		t.Fatalf("conflict responses = %d, want %d", conflictCount, attempts-1)
	}

	var claimCount int
	err := pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM reward_claims
WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3`,
		playerID,
		campaignID,
		rewardID,
	).Scan(&claimCount)
	if err != nil {
		t.Fatalf("count reward claims: %v", err)
	}

	if claimCount != 1 {
		t.Fatalf("reward claim count = %d, want 1", claimCount)
	}
}

func TestPostgresStoreCreateClaimReplaysSameKeySamePayloadConcurrently(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 5*time.Second)

	playerID := "player-" + strings.ReplaceAll(t.Name(), "/", "-")
	campaignID := "campaign-" + strings.ReplaceAll(t.Name(), "/", "-")
	rewardID := "reward-" + strings.ReplaceAll(t.Name(), "/", "-")
	cmd := newIntegrationCreateClaimCommand(t, "claim-key-"+t.Name(), playerID, campaignID, rewardID)

	const attempts = 8

	cmds := make([]CreateClaimStoreCommand, attempts)
	for i := range cmds {
		cmds[i] = cmd
		cmds[i].Claim.ID = mustUUID(t)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
			cmd.Operation,
			cmd.KeyHash,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM reward_claims WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3",
			playerID,
			campaignID,
			rewardID,
		)
	})

	start := make(chan struct{})
	results := make(chan CreateClaimResult, attempts)
	errs := make(chan error, attempts)

	var wg sync.WaitGroup
	for _, cmd := range cmds {
		cmd := cmd

		wg.Add(1)
		go func() {
			defer wg.Done()

			<-start

			result, err := store.CreateClaim(context.Background(), cmd)
			if err != nil {
				errs <- err
				return
			}

			results <- result
		}()
	}

	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent same-key CreateClaim returned error: %v", err)
		}
	}

	var createdCount int
	var replayedCount int
	var responseBody string

	for result := range results {
		if result.StatusCode != CreateClaimStatusCreated {
			t.Fatalf("status = %d, want %d; body = %s", result.StatusCode, CreateClaimStatusCreated, result.ResponseBody)
		}

		if result.Replayed {
			replayedCount++
		} else {
			createdCount++
		}

		if responseBody == "" {
			responseBody = string(result.ResponseBody)
			continue
		}

		if string(result.ResponseBody) != responseBody {
			t.Fatalf("response body = %s, want %s", result.ResponseBody, responseBody)
		}
	}

	if createdCount != 1 {
		t.Fatalf("created responses = %d, want 1", createdCount)
	}

	if replayedCount != attempts-1 {
		t.Fatalf("replayed responses = %d, want %d", replayedCount, attempts-1)
	}

	var claimCount int
	err := pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM reward_claims
WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3`,
		playerID,
		campaignID,
		rewardID,
	).Scan(&claimCount)
	if err != nil {
		t.Fatalf("count reward claims: %v", err)
	}

	if claimCount != 1 {
		t.Fatalf("reward claim count = %d, want 1", claimCount)
	}

	var idempotencyCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM idempotency_keys
WHERE operation = $1 AND key_hash = $2`,
		cmd.Operation,
		cmd.KeyHash,
	).Scan(&idempotencyCount)
	if err != nil {
		t.Fatalf("count idempotency keys: %v", err)
	}

	if idempotencyCount != 1 {
		t.Fatalf("idempotency key count = %d, want 1", idempotencyCount)
	}
}

func TestPostgresStoreCreateClaimReturnsInProgressForProcessingKey(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	playerID := "player-" + strings.ReplaceAll(t.Name(), "/", "-")
	campaignID := "campaign-" + strings.ReplaceAll(t.Name(), "/", "-")
	rewardID := "reward-" + strings.ReplaceAll(t.Name(), "/", "-")
	cmd := newIntegrationCreateClaimCommand(t, "claim-key-"+t.Name(), playerID, campaignID, rewardID)

	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
			cmd.Operation,
			cmd.KeyHash,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM reward_claims WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3",
			playerID,
			campaignID,
			rewardID,
		)
	})

	_, err := pool.Exec(
		context.Background(),
		`
INSERT INTO idempotency_keys (operation, key_hash, request_hash, state)
VALUES ($1, $2, $3, $4)`,
		cmd.Operation,
		cmd.KeyHash,
		cmd.RequestHash,
		idempotencyStateProcessing,
	)
	if err != nil {
		t.Fatalf("seed processing idempotency key: %v", err)
	}

	_, err = store.CreateClaim(context.Background(), cmd)
	if !errors.Is(err, ErrIdempotencyInProgress) {
		t.Fatalf("CreateClaim error = %v, want %v", err, ErrIdempotencyInProgress)
	}

	var claimCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM reward_claims
WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3`,
		playerID,
		campaignID,
		rewardID,
	).Scan(&claimCount)
	if err != nil {
		t.Fatalf("count reward claims: %v", err)
	}

	if claimCount != 0 {
		t.Fatalf("reward claim count = %d, want 0", claimCount)
	}
}

func openIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if strings.TrimSpace(databaseURL) == "" {
		databaseURL = defaultIntegrationDatabaseURL
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

func newIntegrationCreateClaimCommand(t *testing.T, key string, playerID string, campaignID string, rewardID string) CreateClaimStoreCommand {
	t.Helper()

	keyHash, err := idempotency.HashKey(key)
	if err != nil {
		t.Fatalf("hash idempotency key: %v", err)
	}

	requestHash, err := idempotency.HashRewardClaimRequest(idempotency.RewardClaimRequest{
		PlayerID:   playerID,
		CampaignID: campaignID,
		RewardID:   rewardID,
	})
	if err != nil {
		t.Fatalf("hash request: %v", err)
	}

	return CreateClaimStoreCommand{
		Claim: Claim{
			ID:         mustUUID(t),
			PlayerID:   playerID,
			CampaignID: campaignID,
			RewardID:   rewardID,
			Status:     ClaimStatusClaimed,
		},
		Operation:   idempotency.RewardClaimOperation,
		KeyHash:     keyHash[:],
		RequestHash: requestHash[:],
	}
}

func mustUUID(t *testing.T) string {
	t.Helper()

	id, err := NewUUIDV4()
	if err != nil {
		t.Fatalf("generate uuid: %v", err)
	}

	return id
}
