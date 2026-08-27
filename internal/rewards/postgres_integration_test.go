//go:build integration

package rewards

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultIntegrationDatabaseURL = "postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable"
	outboxStatusPending           = "pending"
)

func TestCreateClaimPersistenceAllowsSameRewardInDifferentCampaigns(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	firstCampaignID := "campaign-winter-" + testName
	secondCampaignID := "campaign-spring-" + testName
	rewardID := "reward-" + testName

	first := newIntegrationCreateClaimParams(t, "claim-key-first-"+testName, playerID, firstCampaignID, rewardID)
	second := newIntegrationCreateClaimParams(t, "claim-key-second-"+testName, playerID, secondCampaignID, rewardID)

	cleanupIntegrationCreateClaimData(t, pool, playerID, firstCampaignID, rewardID, first)
	cleanupIntegrationCreateClaimData(t, pool, playerID, secondCampaignID, rewardID, second)

	firstResult, err := service.createClaim(context.Background(), first)
	if err != nil {
		t.Fatalf("first CreateClaim returned error: %v", err)
	}
	if firstResult.StatusCode != createClaimStatusCreated {
		t.Fatalf("first status = %d, want %d", firstResult.StatusCode, createClaimStatusCreated)
	}

	secondResult, err := service.createClaim(context.Background(), second)
	if err != nil {
		t.Fatalf("second CreateClaim returned error: %v", err)
	}
	if secondResult.StatusCode != createClaimStatusCreated {
		t.Fatalf("second status = %d, want %d", secondResult.StatusCode, createClaimStatusCreated)
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

func TestCreateClaimPersistencePreservesCanceledCaller(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 2*time.Second)

	testName := integrationTestName(t)
	cmd := newIntegrationCreateClaimParams(
		t,
		"claim-key-"+testName,
		"player-"+testName,
		"campaign-"+testName,
		"reward-"+testName,
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.createClaim(ctx, cmd)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateClaim error = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("CreateClaim error = %v, did not want ErrUnavailable", err)
	}
}

func TestCreateClaimPersistencePreservesExpiredCallerDeadline(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 2*time.Second)

	testName := integrationTestName(t)
	cmd := newIntegrationCreateClaimParams(
		t,
		"claim-key-"+testName,
		"player-"+testName,
		"campaign-"+testName,
		"reward-"+testName,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	_, err := service.createClaim(ctx, cmd)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CreateClaim error = %v, want context.DeadlineExceeded", err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("CreateClaim error = %v, did not want ErrUnavailable", err)
	}
}

func TestCreateClaimPersistenceStoresRewardSpecificIdempotencyState(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimParams(t, "claim-key-"+testName, playerID, campaignID, rewardID)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

	result, err := service.createClaim(context.Background(), cmd)
	if err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}
	if result.StatusCode != createClaimStatusCreated {
		t.Fatalf("status = %d, want %d", result.StatusCode, createClaimStatusCreated)
	}
	if result.Replayed {
		t.Fatal("first CreateClaim call should not be replayed")
	}
	if len(result.ResponseBody) == 0 {
		t.Fatal("response body is empty")
	}

	var (
		storedPlayerID   string
		storedCampaignID string
		storedRewardID   string
		responseStatus   int
		responseBody     []byte
		rewardClaimID    string
	)

	err = pool.QueryRow(
		context.Background(),
		`
SELECT player_id, campaign_id, reward_id, response_status, response_body, reward_claim_id::text
FROM reward_claim_idempotency_keys
WHERE key_hash = $1`,
		cmd.KeyHash[:],
	).Scan(
		&storedPlayerID,
		&storedCampaignID,
		&storedRewardID,
		&responseStatus,
		&responseBody,
		&rewardClaimID,
	)
	if err != nil {
		t.Fatalf("query idempotency key: %v", err)
	}

	if storedPlayerID != playerID || storedCampaignID != campaignID || storedRewardID != rewardID {
		t.Fatalf(
			"stored request identity = (%q, %q, %q), want (%q, %q, %q)",
			storedPlayerID,
			storedCampaignID,
			storedRewardID,
			playerID,
			campaignID,
			rewardID,
		)
	}
	if responseStatus != createClaimStatusCreated {
		t.Fatalf("response_status = %d, want %d", responseStatus, createClaimStatusCreated)
	}
	if !bytes.Equal(responseBody, result.ResponseBody) {
		t.Fatalf("stored response body = %s, want %s", responseBody, result.ResponseBody)
	}

	claimID := rewardClaimIDForIdentity(t, pool, playerID, campaignID, rewardID)
	if rewardClaimID != claimID {
		t.Fatalf("reward_claim_id = %q, want %q", rewardClaimID, claimID)
	}
}

func TestCreateClaimPersistenceReplaysStoredResponse(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimParams(t, "claim-key-"+testName, playerID, campaignID, rewardID)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

	firstResult, err := service.createClaim(context.Background(), cmd)
	if err != nil {
		t.Fatalf("first CreateClaim returned error: %v", err)
	}

	replayResult, err := service.createClaim(context.Background(), cmd)
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

	if got := countRewardClaims(t, pool, playerID, campaignID, rewardID); got != 1 {
		t.Fatalf("reward claim count = %d, want 1", got)
	}
	if got := countRewardClaimedOutboxEventsForIdentity(t, pool, playerID, campaignID, rewardID); got != 1 {
		t.Fatalf("outbox event count = %d, want 1", got)
	}
}

func TestCreateClaimPersistenceRejectsKeyReuseWithDifferentRequest(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 2*time.Second)

	tests := []struct {
		name           string
		playerSuffix   string
		campaignSuffix string
		rewardSuffix   string
	}{
		{
			name:         "different player ID",
			playerSuffix: "-different",
		},
		{
			name:           "different campaign ID",
			campaignSuffix: "-different",
		},
		{
			name:         "different reward ID",
			rewardSuffix: "-different",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testName := integrationTestName(t)
			playerID := "player-" + testName
			campaignID := "campaign-" + testName
			rewardID := "reward-" + testName
			key := "claim-key-" + testName

			first := newIntegrationCreateClaimParams(
				t,
				key,
				playerID,
				campaignID,
				rewardID,
			)
			mismatch := newIntegrationCreateClaimParams(
				t,
				key,
				playerID+tt.playerSuffix,
				campaignID+tt.campaignSuffix,
				rewardID+tt.rewardSuffix,
			)

			cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, first, mismatch)
			cleanupIntegrationCreateClaimData(
				t,
				pool,
				mismatch.PlayerID,
				mismatch.CampaignID,
				mismatch.RewardID,
			)

			_, err := service.createClaim(context.Background(), first)
			if err != nil {
				t.Fatalf("first CreateClaim returned error: %v", err)
			}

			_, err = service.createClaim(context.Background(), mismatch)
			if !errors.Is(err, ErrIdempotencyKeyReused) {
				t.Fatalf("CreateClaim error = %v, want %v", err, ErrIdempotencyKeyReused)
			}

			if got := countRewardClaims(t, pool, playerID, campaignID, rewardID); got != 1 {
				t.Fatalf("original reward claim count = %d, want 1", got)
			}
			if got := countRewardClaimedOutboxEventsForIdentity(
				t,
				pool,
				playerID,
				campaignID,
				rewardID,
			); got != 1 {
				t.Fatalf("original outbox event count = %d, want 1", got)
			}

			if got := countRewardClaims(
				t,
				pool,
				mismatch.PlayerID,
				mismatch.CampaignID,
				mismatch.RewardID,
			); got != 0 {
				t.Fatalf("mismatched reward claim count = %d, want 0", got)
			}
			if got := countRewardClaimedOutboxEventsForIdentity(
				t,
				pool,
				mismatch.PlayerID,
				mismatch.CampaignID,
				mismatch.RewardID,
			); got != 0 {
				t.Fatalf("mismatched outbox event count = %d, want 0", got)
			}
		})
	}
}

func TestCreateClaimPersistenceStoresAndReplaysDuplicateRewardResponse(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	first := newIntegrationCreateClaimParams(t, "claim-key-first-"+testName, playerID, campaignID, rewardID)
	duplicate := newIntegrationCreateClaimParams(t, "claim-key-duplicate-"+testName, playerID, campaignID, rewardID)

	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, first, duplicate)

	firstResult, err := service.createClaim(context.Background(), first)
	if err != nil {
		t.Fatalf("first CreateClaim returned error: %v", err)
	}
	if firstResult.StatusCode != createClaimStatusCreated {
		t.Fatalf("first status = %d, want %d", firstResult.StatusCode, createClaimStatusCreated)
	}

	duplicateResult, err := service.createClaim(context.Background(), duplicate)
	if err != nil {
		t.Fatalf("duplicate CreateClaim returned error: %v", err)
	}
	if duplicateResult.StatusCode != createClaimStatusConflict {
		t.Fatalf("duplicate status = %d, want %d", duplicateResult.StatusCode, createClaimStatusConflict)
	}
	if duplicateResult.Replayed {
		t.Fatal("first duplicate response should not be replayed")
	}

	var duplicateBody errorResponse
	if err := json.Unmarshal(duplicateResult.ResponseBody, &duplicateBody); err != nil {
		t.Fatalf("unmarshal duplicate response: %v; body = %s", err, duplicateResult.ResponseBody)
	}
	if duplicateBody.Error.Code != duplicateClaimErrorCode {
		t.Fatalf("duplicate error code = %q, want %q", duplicateBody.Error.Code, duplicateClaimErrorCode)
	}

	replayResult, err := service.createClaim(context.Background(), duplicate)
	if err != nil {
		t.Fatalf("duplicate replay CreateClaim returned error: %v", err)
	}
	if replayResult.StatusCode != createClaimStatusConflict {
		t.Fatalf("duplicate replay status = %d, want %d", replayResult.StatusCode, createClaimStatusConflict)
	}
	if !replayResult.Replayed {
		t.Fatal("duplicate replay should be marked replayed")
	}
	if !bytes.Equal(replayResult.ResponseBody, duplicateResult.ResponseBody) {
		t.Fatalf("duplicate replay body = %s, want %s", replayResult.ResponseBody, duplicateResult.ResponseBody)
	}

	var (
		responseStatus int
		responseBody   []byte
		rewardClaimID  sql.NullString
	)
	err = pool.QueryRow(
		context.Background(),
		`
SELECT response_status, response_body, reward_claim_id::text
FROM reward_claim_idempotency_keys
WHERE key_hash = $1`,
		duplicate.KeyHash[:],
	).Scan(&responseStatus, &responseBody, &rewardClaimID)
	if err != nil {
		t.Fatalf("query duplicate idempotency key: %v", err)
	}
	if responseStatus != createClaimStatusConflict {
		t.Fatalf("stored duplicate status = %d, want %d", responseStatus, createClaimStatusConflict)
	}
	if !bytes.Equal(responseBody, duplicateResult.ResponseBody) {
		t.Fatalf("stored duplicate body = %s, want %s", responseBody, duplicateResult.ResponseBody)
	}
	if rewardClaimID.Valid {
		t.Fatalf("stored duplicate reward_claim_id = %q, want NULL", rewardClaimID.String)
	}

	if got := countRewardClaims(t, pool, playerID, campaignID, rewardID); got != 1 {
		t.Fatalf("reward claim count = %d, want 1", got)
	}
	if got := countRewardClaimedOutboxEventsForIdentity(t, pool, playerID, campaignID, rewardID); got != 1 {
		t.Fatalf("outbox event count = %d, want 1", got)
	}
}

func TestCreateClaimPersistencePreventsDuplicateRewardsConcurrently(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 5*time.Second)

	const attempts = 8

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName

	cmds := make([]createClaimParams, attempts)
	for i := range cmds {
		cmds[i] = newIntegrationCreateClaimParams(
			t,
			"claim-key-"+strconv.Itoa(i)+"-"+testName,
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
		go func(cmd createClaimParams) {
			defer wg.Done()
			ready <- struct{}{}
			<-start

			result, err := service.createClaim(context.Background(), cmd)
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(cmd)
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

	var createdCount, conflictCount int
	for result := range results {
		switch result.StatusCode {
		case createClaimStatusCreated:
			createdCount++
		case createClaimStatusConflict:
			conflictCount++
		default:
			t.Fatalf("unexpected status = %d; body = %s", result.StatusCode, result.ResponseBody)
		}
		if result.Replayed {
			t.Fatal("different-key concurrent request unexpectedly replayed")
		}
	}

	if createdCount != 1 {
		t.Fatalf("created responses = %d, want 1", createdCount)
	}
	if conflictCount != attempts-1 {
		t.Fatalf("conflict responses = %d, want %d", conflictCount, attempts-1)
	}
	if got := countRewardClaims(t, pool, playerID, campaignID, rewardID); got != 1 {
		t.Fatalf("reward claim count = %d, want 1", got)
	}
	if got := countRewardClaimedOutboxEventsForIdentity(t, pool, playerID, campaignID, rewardID); got != 1 {
		t.Fatalf("outbox event count = %d, want 1", got)
	}

	var (
		totalCount       int
		incompleteCount  int
		linkedClaimCount int
	)
	err := pool.QueryRow(
		context.Background(),
		`
SELECT count(*),
       count(*) FILTER (WHERE response_status IS NULL OR response_body IS NULL),
       count(*) FILTER (WHERE reward_claim_id IS NOT NULL)
FROM reward_claim_idempotency_keys
WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3`,
		playerID,
		campaignID,
		rewardID,
	).Scan(&totalCount, &incompleteCount, &linkedClaimCount)
	if err != nil {
		t.Fatalf("query concurrent idempotency records: %v", err)
	}
	if totalCount != attempts {
		t.Fatalf("idempotency keys = %d, want %d", totalCount, attempts)
	}
	if incompleteCount != 0 {
		t.Fatalf("incomplete idempotency keys = %d, want 0", incompleteCount)
	}
	if linkedClaimCount != 1 {
		t.Fatalf("idempotency keys linked to reward claim = %d, want 1", linkedClaimCount)
	}
}

func TestCreateClaimPersistenceReplaysSameKeySameRequestConcurrently(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 5*time.Second)

	const attempts = 8

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimParams(t, "claim-key-"+testName, playerID, campaignID, rewardID)
	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

	ready := make(chan struct{}, attempts)
	start := make(chan struct{})
	results := make(chan CreateClaimResult, attempts)
	errs := make(chan error, attempts)

	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			<-start

			result, err := service.createClaim(context.Background(), cmd)
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
		if result.StatusCode != createClaimStatusCreated {
			t.Fatalf("status = %d, want %d; body = %s", result.StatusCode, createClaimStatusCreated, result.ResponseBody)
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
	if got := countRewardClaims(t, pool, playerID, campaignID, rewardID); got != 1 {
		t.Fatalf("reward claim count = %d, want 1", got)
	}
	if got := countRewardClaimedOutboxEventsForIdentity(t, pool, playerID, campaignID, rewardID); got != 1 {
		t.Fatalf("outbox event count = %d, want 1", got)
	}

	var idempotencyCount int
	err := pool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM reward_claim_idempotency_keys WHERE key_hash = $1`,
		cmd.KeyHash[:],
	).Scan(&idempotencyCount)
	if err != nil {
		t.Fatalf("count idempotency keys: %v", err)
	}
	if idempotencyCount != 1 {
		t.Fatalf("idempotency key count = %d, want 1", idempotencyCount)
	}
}

func TestCreateClaimPersistenceTreatsCommittedIncompleteKeyAsInternal(t *testing.T) {
	tests := []struct {
		name           string
		storedRewardID func(string) string
	}{
		{name: "same request", storedRewardID: func(rewardID string) string { return rewardID }},
		{name: "different request", storedRewardID: func(rewardID string) string { return rewardID + "-different" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := openIntegrationPool(t)
			service := mustNewIntegrationService(t, pool, 2*time.Second)

			testName := integrationTestName(t)
			playerID := "player-" + testName
			campaignID := "campaign-" + testName
			rewardID := "reward-" + testName
			cmd := newIntegrationCreateClaimParams(t, "claim-key-"+testName, playerID, campaignID, rewardID)

			cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

			_, err := pool.Exec(
				context.Background(),
				`
INSERT INTO reward_claim_idempotency_keys (key_hash, player_id, campaign_id, reward_id)
VALUES ($1, $2, $3, $4)`,
				cmd.KeyHash[:],
				playerID,
				campaignID,
				tt.storedRewardID(rewardID),
			)
			if err != nil {
				t.Fatalf("seed committed incomplete idempotency key: %v", err)
			}

			_, err = service.createClaim(context.Background(), cmd)
			if !errors.Is(err, ErrInternal) {
				t.Fatalf("CreateClaim error = %v, want %v", err, ErrInternal)
			}
			if got := countRewardClaims(t, pool, playerID, campaignID, rewardID); got != 0 {
				t.Fatalf("reward claim count = %d, want 0", got)
			}
		})
	}
}

func TestRewardClaimIdempotencyResponseShapeConstraintRejectsInvalidRows(t *testing.T) {
	pool := openIntegrationPool(t)

	tests := []struct {
		name      string
		status    any
		body      any
		linkClaim bool
	}{
		{name: "incomplete with body", body: []byte(`{}`)},
		{name: "incomplete with claim link", linkClaim: true},
		{name: "created without body", status: createClaimStatusCreated, linkClaim: true},
		{name: "created with empty body", status: createClaimStatusCreated, body: []byte{}, linkClaim: true},
		{name: "created without claim link", status: createClaimStatusCreated, body: []byte(`{}`)},
		{name: "conflict with claim link", status: createClaimStatusConflict, body: []byte(`{}`), linkClaim: true},
		{name: "unsupported status", status: 500, body: []byte(`{}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testName := integrationTestName(t)
			playerID := "player-" + testName
			campaignID := "campaign-" + testName
			rewardID := "reward-" + testName
			cmd := newIntegrationCreateClaimParams(t, "claim-key-"+testName, playerID, campaignID, rewardID)
			cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

			var rewardClaimID any
			if tt.linkClaim {
				rewardClaimID = newUUIDV4()
				_, err := pool.Exec(
					context.Background(),
					`INSERT INTO reward_claims (id, player_id, campaign_id, reward_id) VALUES ($1, $2, $3, $4)`,
					rewardClaimID,
					playerID,
					campaignID,
					rewardID,
				)
				if err != nil {
					t.Fatalf("seed reward claim: %v", err)
				}
			}

			_, err := pool.Exec(
				context.Background(),
				`
INSERT INTO reward_claim_idempotency_keys (
    key_hash,
    player_id,
    campaign_id,
    reward_id,
    response_status,
    response_body,
    reward_claim_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				cmd.KeyHash[:],
				playerID,
				campaignID,
				rewardID,
				tt.status,
				tt.body,
				rewardClaimID,
			)
			if err == nil {
				t.Fatal("insert invalid idempotency response shape succeeded")
			}

			pgErr, ok := errors.AsType[*pgconn.PgError](err)
			if !ok {
				t.Fatalf("insert invalid idempotency response shape error = %T %v, want PostgreSQL error", err, err)
			}
			if pgErr.Code != "23514" {
				t.Fatalf("PostgreSQL error code = %q, want check_violation (23514)", pgErr.Code)
			}
			if pgErr.ConstraintName != "reward_claim_idempotency_keys_response_shape_chk" {
				t.Fatalf(
					"constraint = %q, want reward_claim_idempotency_keys_response_shape_chk",
					pgErr.ConstraintName,
				)
			}
		})
	}
}

func TestCreateClaimPersistenceReplaysStoredResponseAsOpaqueBytes(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimParams(t, "claim-key-"+testName, playerID, campaignID, rewardID)
	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

	storedBody := []byte(`{"error":{"code":"reward_already_claimed","message":"Historical wording","legacy_detail":true}}`)
	_, err := pool.Exec(
		context.Background(),
		`
INSERT INTO reward_claim_idempotency_keys (
    key_hash,
    player_id,
    campaign_id,
    reward_id,
    response_status,
    response_body
)
VALUES ($1, $2, $3, $4, $5, $6)`,
		cmd.KeyHash[:],
		playerID,
		campaignID,
		rewardID,
		createClaimStatusConflict,
		storedBody,
	)
	if err != nil {
		t.Fatalf("seed stored idempotency response: %v", err)
	}

	result, err := service.createClaim(context.Background(), cmd)
	if err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}
	if result.StatusCode != createClaimStatusConflict {
		t.Fatalf("status = %d, want %d", result.StatusCode, createClaimStatusConflict)
	}
	if !result.Replayed {
		t.Fatal("stored response should be marked replayed")
	}
	if !bytes.Equal(result.ResponseBody, storedBody) {
		t.Fatalf("response body = %q, want %q", result.ResponseBody, storedBody)
	}
	if got := countRewardClaims(t, pool, playerID, campaignID, rewardID); got != 0 {
		t.Fatalf("reward claim count = %d, want 0", got)
	}
}

func TestCreateClaimPersistenceRejectsMalformedStoredResponse(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimParams(t, "claim-key-"+testName, playerID, campaignID, rewardID)
	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

	_, err := pool.Exec(
		context.Background(),
		`
INSERT INTO reward_claim_idempotency_keys (
    key_hash,
    player_id,
    campaign_id,
    reward_id,
    response_status,
    response_body
)
VALUES ($1, $2, $3, $4, $5, $6)`,
		cmd.KeyHash[:],
		playerID,
		campaignID,
		rewardID,
		createClaimStatusConflict,
		[]byte(`{"error":`),
	)
	if err != nil {
		t.Fatalf("seed malformed stored response: %v", err)
	}

	_, err = service.createClaim(context.Background(), cmd)
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("CreateClaim error = %v, want %v", err, ErrInternal)
	}
}

func TestCreateClaimPersistenceCreatesRewardClaimedOutboxEvent(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimParams(t, "claim-key-"+testName, playerID, campaignID, rewardID)
	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

	result, err := service.createClaim(context.Background(), cmd)
	if err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}
	if result.StatusCode != createClaimStatusCreated {
		t.Fatalf("status = %d, want %d", result.StatusCode, createClaimStatusCreated)
	}

	var (
		eventID       string
		aggregateType string
		aggregateID   string
		eventType     string
		status        string
		payload       []byte
		claimedAt     time.Time
	)
	err = pool.QueryRow(
		context.Background(),
		`
SELECT o.id, o.aggregate_type, o.aggregate_id, o.event_type, o.status, o.payload, r.created_at
FROM outbox_events AS o
JOIN reward_claims AS r ON r.id = o.aggregate_id
WHERE o.aggregate_type = $1
  AND o.event_type = $2
  AND r.player_id = $3
  AND r.campaign_id = $4
  AND r.reward_id = $5`,
		outboxAggregateTypeRewardClaim,
		outboxEventTypeRewardClaimed,
		playerID,
		campaignID,
		rewardID,
	).Scan(&eventID, &aggregateType, &aggregateID, &eventType, &status, &payload, &claimedAt)
	if err != nil {
		t.Fatalf("query outbox event: %v", err)
	}

	if eventID == "" {
		t.Fatal("event ID is empty")
	}
	if aggregateType != outboxAggregateTypeRewardClaim {
		t.Fatalf("aggregate type = %q, want %q", aggregateType, outboxAggregateTypeRewardClaim)
	}
	if eventType != outboxEventTypeRewardClaimed {
		t.Fatalf("event type = %q, want %q", eventType, outboxEventTypeRewardClaimed)
	}
	if status != outboxStatusPending {
		t.Fatalf("status = %q, want %q", status, outboxStatusPending)
	}

	expectedPayload := []byte(fmt.Sprintf(`{
		"schema_version": 1,
		"event_id": %q,
		"event_type": "RewardClaimed",
		"occurred_at": %q,
		"claim": {
			"claim_id": %q,
			"player_id": %q,
			"campaign_id": %q,
			"reward_id": %q,
			"claimed_at": %q
		}
	}`,
		eventID,
		claimedAt.UTC().Format(time.RFC3339Nano),
		aggregateID,
		playerID,
		campaignID,
		rewardID,
		claimedAt.UTC().Format(time.RFC3339Nano),
	))

	assertJSONSemanticallyEqual(t, payload, expectedPayload)
}

func TestCreateClaimPersistenceRollsBackWhenOutboxInsertFails(t *testing.T) {
	pool := openIntegrationPool(t)
	service := mustNewIntegrationService(t, pool, 2*time.Second)

	testName := integrationTestName(t)
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	cmd := newIntegrationCreateClaimParams(t, "claim-key-"+testName, playerID, campaignID, rewardID)
	cleanupIntegrationCreateClaimData(t, pool, playerID, campaignID, rewardID, cmd)

	const constraintName = "test_reject_reward_claimed_insert"
	removeConstraint := func() error {
		_, err := pool.Exec(
			context.Background(),
			"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS "+constraintName,
		)
		return err
	}
	if err := removeConstraint(); err != nil {
		t.Fatalf("remove stale test constraint: %v", err)
	}
	rejectedPlayerID := strings.ReplaceAll(playerID, "'", "''")
	_, err := pool.Exec(
		context.Background(),
		"ALTER TABLE outbox_events ADD CONSTRAINT "+constraintName+
			" CHECK ((payload #>> '{claim,player_id}') IS DISTINCT FROM '"+rejectedPlayerID+"') NOT VALID",
	)
	if err != nil {
		t.Fatalf("add test outbox constraint: %v", err)
	}
	t.Cleanup(func() {
		if err := removeConstraint(); err != nil {
			t.Errorf("remove test outbox constraint: %v", err)
		}
	})

	_, err = service.createClaim(context.Background(), cmd)
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("CreateClaim error = %v, want %v", err, ErrInternal)
	}
	if got := countRewardClaims(t, pool, playerID, campaignID, rewardID); got != 0 {
		t.Fatalf("reward claim count = %d, want 0", got)
	}

	var idempotencyCount int
	err = pool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM reward_claim_idempotency_keys WHERE key_hash = $1`,
		cmd.KeyHash[:],
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
FROM outbox_events AS o
JOIN reward_claims AS r ON r.id = o.aggregate_id
WHERE o.aggregate_type = $1
  AND o.event_type = $2
  AND r.player_id = $3
  AND r.campaign_id = $4
  AND r.reward_id = $5`,
		outboxAggregateTypeRewardClaim,
		outboxEventTypeRewardClaimed,
		playerID,
		campaignID,
		rewardID,
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("count outbox events: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf("outbox event count = %d, want 0", outboxCount)
	}
}

func TestOutboxEventsAreUniquePerAggregateAndEventType(t *testing.T) {
	pool := openIntegrationPool(t)

	aggregateID := newUUIDV4()
	firstEventID := newUUIDV4()
	secondEventID := newUUIDV4()

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

func mustNewIntegrationService(t *testing.T, pool *pgxpool.Pool, queryTimeout time.Duration) *Service {
	t.Helper()

	service, err := NewService(pool, queryTimeout)
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	return service
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

func countRewardClaims(t *testing.T, pool *pgxpool.Pool, playerID, campaignID, rewardID string) int {
	t.Helper()

	var count int
	err := pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM reward_claims
WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3`,
		playerID,
		campaignID,
		rewardID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count reward claims: %v", err)
	}
	return count
}

func rewardClaimIDForIdentity(t *testing.T, pool *pgxpool.Pool, playerID, campaignID, rewardID string) string {
	t.Helper()

	var claimID string
	err := pool.QueryRow(
		context.Background(),
		`
SELECT id::text
FROM reward_claims
WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3`,
		playerID,
		campaignID,
		rewardID,
	).Scan(&claimID)
	if err != nil {
		t.Fatalf("query reward claim ID: %v", err)
	}
	return claimID
}

func countRewardClaimedOutboxEventsForIdentity(
	t *testing.T,
	pool *pgxpool.Pool,
	playerID, campaignID, rewardID string,
) int {
	t.Helper()

	var count int
	err := pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM outbox_events AS o
JOIN reward_claims AS r ON r.id = o.aggregate_id
WHERE o.aggregate_type = $1
  AND o.event_type = $2
  AND r.player_id = $3
  AND r.campaign_id = $4
  AND r.reward_id = $5`,
		outboxAggregateTypeRewardClaim,
		outboxEventTypeRewardClaimed,
		playerID,
		campaignID,
		rewardID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count reward claimed outbox events: %v", err)
	}
	return count
}

func cleanupIntegrationCreateClaimData(
	t *testing.T,
	pool *pgxpool.Pool,
	playerID, campaignID, rewardID string,
	cmds ...createClaimParams,
) {
	t.Helper()

	runIntegrationCleanup(t, func() error {
		for _, cmd := range cmds {
			_, err := pool.Exec(
				context.Background(),
				"DELETE FROM reward_claim_idempotency_keys WHERE key_hash = $1",
				cmd.KeyHash[:],
			)
			if err != nil {
				return fmt.Errorf("delete idempotency key: %w", err)
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

func newIntegrationCreateClaimParams(
	t *testing.T,
	key, playerID, campaignID, rewardID string,
) createClaimParams {
	t.Helper()

	params, err := prepareCreateClaim(CreateClaimCommand{
		PlayerID:       playerID,
		CampaignID:     campaignID,
		RewardID:       rewardID,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("prepare CreateClaim params: %v", err)
	}
	return params
}

func integrationTestName(t *testing.T) string {
	t.Helper()
	return strings.ReplaceAll(t.Name(), "/", "-")
}
