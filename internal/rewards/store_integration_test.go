//go:build integration

package rewards

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestPostgresStoreCreateClaimAllowsSameRewardInDifferentCampaigns(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	firstCampaignID := "campaign-winter-" + testName
	secondCampaignID := "campaign-spring-" + testName
	rewardID := "reward-" + testName

	first := newIntegrationCreateClaimCommand(
		t,
		"claim-key-first-"+testName,
		playerID,
		firstCampaignID,
		rewardID,
	)
	second := newIntegrationCreateClaimCommand(
		t,
		"claim-key-second-"+testName,
		playerID,
		secondCampaignID,
		rewardID,
	)

	cleanupIntegrationCreateClaimData(t, pool, playerID, firstCampaignID, rewardID, first)
	cleanupIntegrationCreateClaimData(t, pool, playerID, secondCampaignID, rewardID, second)

	firstResult, err := store.CreateClaim(context.Background(), first)
	if err != nil {
		t.Fatalf("first CreateClaim returned error: %v", err)
	}

	if firstResult.StatusCode != CreateClaimStatusCreated {
		t.Fatalf("first status = %d, want %d", firstResult.StatusCode, CreateClaimStatusCreated)
	}

	secondResult, err := store.CreateClaim(context.Background(), second)
	if err != nil {
		t.Fatalf("second CreateClaim returned error: %v", err)
	}

	if secondResult.StatusCode != CreateClaimStatusCreated {
		t.Fatalf("second status = %d, want %d", secondResult.StatusCode, CreateClaimStatusCreated)
	}

	var claimCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM reward_claims
WHERE player_id = $1
  AND reward_id = $2
  AND campaign_id IN ($3, $4)`,
		playerID,
		rewardID,
		firstCampaignID,
		secondCampaignID,
	).Scan(&claimCount)
	if err != nil {
		t.Fatalf("count reward claims: %v", err)
	}

	if claimCount != 2 {
		t.Fatalf("reward claim count = %d, want 2", claimCount)
	}
}

func TestPostgresStoreCreateClaimRejectsInvalidQueryTimeout(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 0)

	testName := integrationTestName(t)
	cmd := newIntegrationCreateClaimCommand(
		t,
		"claim-key-"+testName,
		"player-"+testName,
		"campaign-"+testName,
		"reward-"+testName,
	)

	_, err := store.CreateClaim(context.Background(), cmd)
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("CreateClaim error = %v, want ErrInternal", err)
	}
}

func TestPostgresStoreCreateClaimCompletesIdempotencyKey(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimCommand(t, "claim-key-"+testName, playerID, campaignID, rewardID)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

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

	var (
		state          string
		responseStatus int
		rewardClaimID  string
		completedAt    time.Time
	)

	err = pool.QueryRow(
		context.Background(),
		`
SELECT state, response_status, reward_claim_id::text, completed_at
FROM idempotency_keys
WHERE operation = $1 AND key_hash = $2`,
		cmd.Operation,
		cmd.KeyHash,
	).Scan(&state, &responseStatus, &rewardClaimID, &completedAt)
	if err != nil {
		t.Fatalf("query idempotency key: %v", err)
	}

	if state != idempotencyStateCompleted {
		t.Fatalf("state = %q, want %q", state, idempotencyStateCompleted)
	}

	if responseStatus != CreateClaimStatusCreated {
		t.Fatalf("response_status = %d, want %d", responseStatus, CreateClaimStatusCreated)
	}

	if rewardClaimID != cmd.Claim.ID {
		t.Fatalf("reward_claim_id = %q, want %q", rewardClaimID, cmd.Claim.ID)
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

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	first := newIntegrationCreateClaimCommand(t, "claim-key-"+testName, playerID, campaignID, rewardID)

	replay := first
	replay.Claim.ID = mustUUID(t)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, first, replay)

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

	if !bytes.Equal(replayResult.ResponseBody, firstResult.ResponseBody) {
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

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	first := newIntegrationCreateClaimCommand(t, "claim-key-"+testName, playerID, campaignID, rewardID)

	mismatch := first
	mismatch.Claim.ID = mustUUID(t)
	mismatch.Claim.RewardID = rewardID + "-different"
	mismatch.RequestHash = []byte("different-request-hash-32-bytes!")

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, first)
	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, mismatch.Claim.RewardID, mismatch)

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

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	first := newIntegrationCreateClaimCommand(t, "claim-key-first-"+testName, playerID, campaignID, rewardID)
	duplicate := newIntegrationCreateClaimCommand(t, "claim-key-duplicate-"+testName, playerID, campaignID, rewardID)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, first, duplicate)

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

	var (
		state          string
		responseStatus int
		responseBody   []byte
	)

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

	if !bytes.Equal(responseBody, result.ResponseBody) {
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

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	first := newIntegrationCreateClaimCommand(t, "claim-key-first-"+testName, playerID, campaignID, rewardID)
	duplicate := newIntegrationCreateClaimCommand(t, "claim-key-duplicate-"+testName, playerID, campaignID, rewardID)

	duplicateReplay := duplicate
	duplicateReplay.Claim.ID = mustUUID(t)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, first, duplicate, duplicateReplay)

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

	if !bytes.Equal(replayResult.ResponseBody, duplicateResult.ResponseBody) {
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

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName

	const attempts = 8

	cmds := make([]CreateClaimStoreCommand, attempts)
	for i := range cmds {
		cmds[i] = newIntegrationCreateClaimCommand(
			t,
			"claim-key-"+testName+"-"+strconv.Itoa(i),
			playerID,
			campaignID,
			rewardID,
		)
	}

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmds...)

	ready := make(chan struct{}, attempts)
	start := make(chan struct{})
	results := make(chan CreateClaimResult, attempts)
	errs := make(chan error, attempts)

	var wg sync.WaitGroup
	for _, cmd := range cmds {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ready <- struct{}{}
			<-start

			result, err := store.CreateClaim(context.Background(), cmd)
			if err != nil {
				errs <- err
				return
			}

			results <- result
		}()
	}

	for range attempts {
		<-ready
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent CreateClaim returned error: %v", err)
	}

	var (
		createdCount  int
		conflictCount int
	)

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

	claimIDs := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		claimIDs = append(claimIDs, cmd.Claim.ID)
	}

	outboxCount := countRewardClaimedOutboxEvents(t, pool, claimIDs...)
	if outboxCount != 1 {
		t.Fatalf("reward claimed outbox event count = %d, want 1", outboxCount)
	}

	var (
		completedCount         int
		processingCount        int
		createdResponseCount   int
		conflictResponseCount  int
		linkedRewardClaimCount int
	)
	err = pool.QueryRow(
		context.Background(),
		`
SELECT
    count(*) FILTER (WHERE state = $3),
    count(*) FILTER (WHERE state = $4),
    count(*) FILTER (WHERE response_status = $5),
    count(*) FILTER (WHERE response_status = $6),
    count(*) FILTER (WHERE reward_claim_id IS NOT NULL)
FROM idempotency_keys
WHERE operation = $1 AND request_hash = $2`,
		cmds[0].Operation,
		cmds[0].RequestHash,
		idempotencyStateCompleted,
		idempotencyStateProcessing,
		CreateClaimStatusCreated,
		CreateClaimStatusConflict,
	).Scan(
		&completedCount,
		&processingCount,
		&createdResponseCount,
		&conflictResponseCount,
		&linkedRewardClaimCount,
	)
	if err != nil {
		t.Fatalf("query concurrent idempotency state: %v", err)
	}

	if completedCount != attempts {
		t.Fatalf("completed idempotency keys = %d, want %d", completedCount, attempts)
	}

	if processingCount != 0 {
		t.Fatalf("processing idempotency keys = %d, want 0", processingCount)
	}

	if createdResponseCount != 1 {
		t.Fatalf("stored created responses = %d, want 1", createdResponseCount)
	}

	if conflictResponseCount != attempts-1 {
		t.Fatalf("stored conflict responses = %d, want %d", conflictResponseCount, attempts-1)
	}

	if linkedRewardClaimCount != 1 {
		t.Fatalf("idempotency keys linked to reward claim = %d, want 1", linkedRewardClaimCount)
	}
}

func TestPostgresStoreCreateClaimReplaysSameKeySamePayloadConcurrently(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 5*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimCommand(t, "claim-key-"+testName, playerID, campaignID, rewardID)

	const attempts = 8

	cmds := make([]CreateClaimStoreCommand, attempts)
	for i := range cmds {
		cmds[i] = cmd
		cmds[i].Claim.ID = mustUUID(t)
	}

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmds...)

	ready := make(chan struct{}, attempts)
	start := make(chan struct{})
	results := make(chan CreateClaimResult, attempts)
	errs := make(chan error, attempts)

	var wg sync.WaitGroup
	for _, cmd := range cmds {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ready <- struct{}{}
			<-start

			result, err := store.CreateClaim(context.Background(), cmd)
			if err != nil {
				errs <- err
				return
			}

			results <- result
		}()
	}

	for range attempts {
		<-ready
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent same-key CreateClaim returned error: %v", err)
	}

	var (
		createdCount  int
		replayedCount int
		responseBody  []byte
	)

	for result := range results {
		if result.StatusCode != CreateClaimStatusCreated {
			t.Fatalf("status = %d, want %d; body = %s", result.StatusCode, CreateClaimStatusCreated, result.ResponseBody)
		}

		if result.Replayed {
			replayedCount++
		} else {
			createdCount++
		}

		if responseBody == nil {
			responseBody = append([]byte(nil), result.ResponseBody...)
			continue
		}

		if !bytes.Equal(result.ResponseBody, responseBody) {
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

	var (
		state          string
		responseStatus int
		storedBody     []byte
		rewardClaimID  string
	)
	err = pool.QueryRow(
		context.Background(),
		`
SELECT state, response_status, response_body, reward_claim_id::text
FROM idempotency_keys
WHERE operation = $1 AND key_hash = $2`,
		cmd.Operation,
		cmd.KeyHash,
	).Scan(&state, &responseStatus, &storedBody, &rewardClaimID)
	if err != nil {
		t.Fatalf("query idempotency key: %v", err)
	}

	if state != idempotencyStateCompleted {
		t.Fatalf("idempotency state = %q, want %q", state, idempotencyStateCompleted)
	}

	if responseStatus != CreateClaimStatusCreated {
		t.Fatalf("stored response status = %d, want %d", responseStatus, CreateClaimStatusCreated)
	}

	if !bytes.Equal(storedBody, responseBody) {
		t.Fatalf("stored response body = %s, want %s", storedBody, responseBody)
	}

	if rewardClaimID == "" {
		t.Fatal("idempotency key is not linked to the created reward claim")
	}

	claimIDs := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		claimIDs = append(claimIDs, cmd.Claim.ID)
	}

	outboxCount := countRewardClaimedOutboxEvents(t, pool, claimIDs...)
	if outboxCount != 1 {
		t.Fatalf("reward claimed outbox event count = %d, want 1", outboxCount)
	}
}

func TestPostgresStoreCreateClaimReturnsInProgressForProcessingKey(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimCommand(t, "claim-key-"+testName, playerID, campaignID, rewardID)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

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

func TestPostgresStoreCreateClaimCreatesRewardClaimedOutboxEvent(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimCommand(t, "claim-key-"+testName, playerID, campaignID, rewardID)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

	result, err := store.CreateClaim(context.Background(), cmd)
	if err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}

	if result.StatusCode != CreateClaimStatusCreated {
		t.Fatalf("status = %d, want %d", result.StatusCode, CreateClaimStatusCreated)
	}

	var (
		eventID       string
		aggregateType string
		aggregateID   string
		eventType     string
		status        string
		payload       []byte
	)

	err = pool.QueryRow(
		context.Background(),
		`
SELECT id, aggregate_type, aggregate_id, event_type, status, payload
FROM outbox_events
WHERE aggregate_type = $1 AND aggregate_id = $2 AND event_type = $3`,
		outboxAggregateTypeRewardClaim,
		cmd.Claim.ID,
		outboxEventTypeRewardClaimed,
	).Scan(&eventID, &aggregateType, &aggregateID, &eventType, &status, &payload)
	if err != nil {
		t.Fatalf("query outbox event: %v", err)
	}

	if eventID == "" {
		t.Fatal("event ID is empty")
	}

	if aggregateType != outboxAggregateTypeRewardClaim {
		t.Fatalf("aggregate type = %q, want %q", aggregateType, outboxAggregateTypeRewardClaim)
	}

	if aggregateID != cmd.Claim.ID {
		t.Fatalf("aggregate ID = %q, want %q", aggregateID, cmd.Claim.ID)
	}

	if eventType != outboxEventTypeRewardClaimed {
		t.Fatalf("event type = %q, want %q", eventType, outboxEventTypeRewardClaimed)
	}

	if status != outboxStatusPending {
		t.Fatalf("status = %q, want %q", status, outboxStatusPending)
	}

	var event RewardClaimedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal event payload: %v", err)
	}

	if event.SchemaVersion != rewardClaimedSchemaVersion {
		t.Fatalf("schema version = %d, want %d", event.SchemaVersion, rewardClaimedSchemaVersion)
	}

	if event.EventID != eventID {
		t.Fatalf("payload event ID = %q, want %q", event.EventID, eventID)
	}

	if event.EventType != outboxEventTypeRewardClaimed {
		t.Fatalf("payload event type = %q, want %q", event.EventType, outboxEventTypeRewardClaimed)
	}

	if event.Claim.ClaimID != cmd.Claim.ID {
		t.Fatalf("payload claim ID = %q, want %q", event.Claim.ClaimID, cmd.Claim.ID)
	}

	if event.Claim.PlayerID != playerID {
		t.Fatalf("payload player ID = %q, want %q", event.Claim.PlayerID, playerID)
	}

	if event.Claim.CampaignID != campaignID {
		t.Fatalf("payload campaign ID = %q, want %q", event.Claim.CampaignID, campaignID)
	}

	if event.Claim.RewardID != rewardID {
		t.Fatalf("payload reward ID = %q, want %q", event.Claim.RewardID, rewardID)
	}

	if event.Claim.Status != ClaimStatusClaimed {
		t.Fatalf("payload claim status = %q, want %q", event.Claim.Status, ClaimStatusClaimed)
	}

	if event.Claim.ClaimedAt.IsZero() {
		t.Fatal("payload claimed_at is zero")
	}

	if event.OccurredAt.IsZero() {
		t.Fatal("payload occurred_at is zero")
	}
}

func TestPostgresStoreCreateClaimRollsBackWhenOutboxInsertFails(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimCommand(t, "claim-key-"+testName, playerID, campaignID, rewardID)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

	runIntegrationCleanup(t, func() error {
		_, err := pool.Exec(
			context.Background(),
			`
DELETE FROM outbox_events
WHERE aggregate_type = $1 AND aggregate_id = $2 AND event_type = $3`,
			outboxAggregateTypeRewardClaim,
			cmd.Claim.ID,
			outboxEventTypeRewardClaimed,
		)
		if err != nil {
			return fmt.Errorf("delete conflicting outbox event: %w", err)
		}

		return nil
	})

	_, err := pool.Exec(
		context.Background(),
		`
INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, status)
VALUES ($1, $2, $3, $4, $5::jsonb, $6)`,
		mustUUID(t),
		outboxAggregateTypeRewardClaim,
		cmd.Claim.ID,
		outboxEventTypeRewardClaimed,
		`{"schema_version":1}`,
		outboxStatusPending,
	)
	if err != nil {
		t.Fatalf("seed conflicting outbox event: %v", err)
	}

	_, err = store.CreateClaim(context.Background(), cmd)
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("CreateClaim error = %v, want %v", err, ErrInternal)
	}

	var claimCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM reward_claims
WHERE id = $1`,
		cmd.Claim.ID,
	).Scan(&claimCount)
	if err != nil {
		t.Fatalf("count reward claims: %v", err)
	}

	if claimCount != 0 {
		t.Fatalf("reward claim count = %d, want 0", claimCount)
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

	if idempotencyCount != 0 {
		t.Fatalf("idempotency key count = %d, want 0", idempotencyCount)
	}

	var outboxCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM outbox_events
WHERE aggregate_type = $1 AND aggregate_id = $2 AND event_type = $3`,
		outboxAggregateTypeRewardClaim,
		cmd.Claim.ID,
		outboxEventTypeRewardClaimed,
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("count outbox events: %v", err)
	}

	if outboxCount != 1 {
		t.Fatalf("outbox event count = %d, want only pre-seeded event", outboxCount)
	}
}

func TestPostgresStoreCreateClaimReplayDoesNotCreateSecondOutboxEvent(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	first := newIntegrationCreateClaimCommand(t, "claim-key-"+testName, playerID, campaignID, rewardID)

	replay := first
	replay.Claim.ID = mustUUID(t)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, first, replay)

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

	if !bytes.Equal(replayResult.ResponseBody, firstResult.ResponseBody) {
		t.Fatalf("replay response body = %s, want %s", replayResult.ResponseBody, firstResult.ResponseBody)
	}

	if !replayResult.Replayed {
		t.Fatal("replay result should be marked replayed")
	}

	var outboxCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM outbox_events
WHERE aggregate_type = $1
  AND event_type = $2
  AND aggregate_id IN ($3, $4)`,
		outboxAggregateTypeRewardClaim,
		outboxEventTypeRewardClaimed,
		first.Claim.ID,
		replay.Claim.ID,
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("count outbox events: %v", err)
	}

	if outboxCount != 1 {
		t.Fatalf("outbox event count = %d, want 1", outboxCount)
	}
}

func TestPostgresStoreCreateClaimDuplicateRewardDoesNotCreateOutboxEvent(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	first := newIntegrationCreateClaimCommand(t, "claim-key-first-"+testName, playerID, campaignID, rewardID)
	duplicate := newIntegrationCreateClaimCommand(t, "claim-key-duplicate-"+testName, playerID, campaignID, rewardID)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, first, duplicate)

	firstResult, err := store.CreateClaim(context.Background(), first)
	if err != nil {
		t.Fatalf("first CreateClaim returned error: %v", err)
	}

	if firstResult.StatusCode != CreateClaimStatusCreated {
		t.Fatalf("first status = %d, want %d", firstResult.StatusCode, CreateClaimStatusCreated)
	}

	duplicateResult, err := store.CreateClaim(context.Background(), duplicate)
	if err != nil {
		t.Fatalf("duplicate CreateClaim returned error: %v", err)
	}

	if duplicateResult.StatusCode != CreateClaimStatusConflict {
		t.Fatalf("duplicate status = %d, want %d", duplicateResult.StatusCode, CreateClaimStatusConflict)
	}

	if duplicateResult.Replayed {
		t.Fatal("first duplicate response should not be replayed")
	}

	var totalOutboxCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM outbox_events
WHERE aggregate_type = $1 AND event_type = $2 AND aggregate_id IN ($3, $4)`,
		outboxAggregateTypeRewardClaim,
		outboxEventTypeRewardClaimed,
		first.Claim.ID,
		duplicate.Claim.ID,
	).Scan(&totalOutboxCount)
	if err != nil {
		t.Fatalf("count outbox events: %v", err)
	}

	if totalOutboxCount != 1 {
		t.Fatalf("outbox event count = %d, want 1", totalOutboxCount)
	}
}

func TestPostgresStoreCreateClaimKeyMismatchDoesNotCreateOutboxEvent(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	first := newIntegrationCreateClaimCommand(t, "claim-key-"+testName, playerID, campaignID, rewardID)

	mismatch := first
	mismatch.Claim.ID = mustUUID(t)
	mismatch.Claim.RewardID = rewardID + "-different"
	mismatch.RequestHash = []byte("different-request-hash-32-bytes!")

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, first)
	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, mismatch.Claim.RewardID, mismatch)

	firstResult, err := store.CreateClaim(context.Background(), first)
	if err != nil {
		t.Fatalf("first CreateClaim returned error: %v", err)
	}

	if firstResult.StatusCode != CreateClaimStatusCreated {
		t.Fatalf("first status = %d, want %d", firstResult.StatusCode, CreateClaimStatusCreated)
	}

	_, err = store.CreateClaim(context.Background(), mismatch)
	if !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("mismatch CreateClaim error = %v, want %v", err, ErrIdempotencyKeyReused)
	}

	var outboxCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM outbox_events
WHERE aggregate_type = $1 AND event_type = $2 AND aggregate_id IN ($3, $4)`,
		outboxAggregateTypeRewardClaim,
		outboxEventTypeRewardClaimed,
		first.Claim.ID,
		mismatch.Claim.ID,
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("count outbox events: %v", err)
	}

	if outboxCount != 1 {
		t.Fatalf("outbox event count = %d, want 1", outboxCount)
	}
}

func TestPostgresStoreOutboxEventsAreUniquePerAggregateAndEventType(t *testing.T) {
	pool := openIntegrationPool(t)

	aggregateID := mustUUID(t)
	firstEventID := mustUUID(t)
	secondEventID := mustUUID(t)

	runIntegrationCleanup(t, func() error {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM outbox_events WHERE aggregate_type = $1 AND aggregate_id = $2",
			outboxAggregateTypeRewardClaim,
			aggregateID,
		)
		if err != nil {
			return fmt.Errorf("delete outbox events: %w", err)
		}

		return nil
	})

	payload := []byte(`{"schema_version":1}`)

	_, err := pool.Exec(
		context.Background(),
		`
INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, status)
VALUES ($1, $2, $3, $4, $5::jsonb, $6)`,
		firstEventID,
		outboxAggregateTypeRewardClaim,
		aggregateID,
		outboxEventTypeRewardClaimed,
		string(payload),
		outboxStatusPending,
	)
	if err != nil {
		t.Fatalf("insert first outbox event: %v", err)
	}

	_, err = pool.Exec(
		context.Background(),
		`
INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, status)
VALUES ($1, $2, $3, $4, $5::jsonb, $6)`,
		secondEventID,
		outboxAggregateTypeRewardClaim,
		aggregateID,
		outboxEventTypeRewardClaimed,
		string(payload),
		outboxStatusPending,
	)
	if err == nil {
		t.Fatal("insert second outbox event succeeded, want unique constraint violation")
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

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse postgres pool config: invalid DATABASE_URL")
	}

	poolConfig.MaxConns = 8

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
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

func countRewardClaimedOutboxEvents(t *testing.T, pool *pgxpool.Pool, claimIDs ...string) int {
	t.Helper()

	total := 0
	for _, claimID := range claimIDs {
		var count int
		err := pool.QueryRow(
			context.Background(),
			`
SELECT count(*)
FROM outbox_events
WHERE aggregate_type = $1 AND aggregate_id = $2 AND event_type = $3`,
			outboxAggregateTypeRewardClaim,
			claimID,
			outboxEventTypeRewardClaimed,
		).Scan(&count)
		if err != nil {
			t.Fatalf("count reward claimed outbox events for claim %q: %v", claimID, err)
		}

		total += count
	}

	return total
}

func cleanupIntegrationCreateClaimData(
	t *testing.T,
	pool *pgxpool.Pool,
	playerID, campaignID, rewardID string,
	cmds ...CreateClaimStoreCommand,
) {
	t.Helper()

	runIntegrationCleanup(t, func() error {
		for _, cmd := range cmds {
			_, err := pool.Exec(
				context.Background(),
				"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
				cmd.Operation,
				cmd.KeyHash,
			)
			if err != nil {
				return fmt.Errorf("delete idempotency key: %w", err)
			}
		}

		for _, cmd := range cmds {
			_, err := pool.Exec(
				context.Background(),
				`
DELETE FROM outbox_events
WHERE aggregate_type = $1 AND aggregate_id = $2`,
				outboxAggregateTypeRewardClaim,
				cmd.Claim.ID,
			)
			if err != nil {
				return fmt.Errorf("delete outbox event by aggregate ID: %w", err)
			}
		}

		_, err := pool.Exec(
			context.Background(),
			`
DELETE FROM outbox_events
WHERE aggregate_type = $1
  AND aggregate_id IN (
      SELECT id
      FROM reward_claims
      WHERE player_id = $2 AND campaign_id = $3 AND reward_id = $4
  )`,
			outboxAggregateTypeRewardClaim,
			playerID,
			campaignID,
			rewardID,
		)
		if err != nil {
			return fmt.Errorf("delete outbox events for reward claim: %w", err)
		}

		_, err = pool.Exec(
			context.Background(),
			`
DELETE FROM reward_claims
WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3`,
			playerID,
			campaignID,
			rewardID,
		)
		if err != nil {
			return fmt.Errorf("delete reward claims: %w", err)
		}

		return nil
	})
}

func runIntegrationCleanup(t *testing.T, cleanup func() error) {
	t.Helper()

	if err := cleanup(); err != nil {
		t.Fatalf("initial integration cleanup: %v", err)
	}

	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("integration cleanup: %v", err)
		}
	})
}

func newIntegrationCreateClaimCommand(
	t *testing.T,
	key, playerID, campaignID, rewardID string,
) CreateClaimStoreCommand {
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

func integrationTestName(t *testing.T) string {
	t.Helper()

	return strings.ReplaceAll(t.Name(), "/", "-")
}

func mustUUID(t *testing.T) string {
	t.Helper()

	id, err := NewUUIDV4()
	if err != nil {
		t.Fatalf("generate uuid: %v", err)
	}

	return id
}
