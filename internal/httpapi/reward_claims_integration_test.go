//go:build integration

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sarapersson/game-rewards-service/internal/idempotency"
	"github.com/sarapersson/game-rewards-service/internal/rewards"
)

const (
	defaultHTTPIntegrationDatabaseURL         = "postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable"
	httpIntegrationOutboxAggregateRewardClaim = "reward_claim"
	httpIntegrationOutboxEventRewardClaimed   = "RewardClaimed"
)

func TestRewardClaimsHandlerReplaysClaimWithPostgres(t *testing.T) {
	pool := openHTTPIntegrationPool(t)
	store := rewards.NewPostgresStore(pool, 2*time.Second)
	service := rewards.NewService(store)

	testName := strings.ReplaceAll(t.Name(), "/", "-")
	playerID := "player-" + testName
	campaignID := "campaign-" + testName
	rewardID := "reward-" + testName
	idempotencyKey := "claim-key-" + testName
	body := `{"player_id":"` + playerID + `","campaign_id":"` + campaignID + `","reward_id":"` + rewardID + `"}`

	keyHash, err := idempotency.HashKey(idempotencyKey)
	if err != nil {
		t.Fatalf("hash idempotency key: %v", err)
	}

	cleanupHTTPIntegrationRewardClaim(
		t,
		pool,
		keyHash[:],
		playerID,
		campaignID,
		rewardID,
	)
	t.Cleanup(func() {
		cleanupHTTPIntegrationRewardClaim(
			t,
			pool,
			keyHash[:],
			playerID,
			campaignID,
			rewardID,
		)
	})

	first := performRewardClaimRequest(t, service, idempotencyKey, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want %d; body = %s", first.Code, http.StatusCreated, first.Body.String())
	}

	replay := performRewardClaimRequest(t, service, idempotencyKey, body)
	if replay.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, want %d; body = %s", replay.Code, http.StatusCreated, replay.Body.String())
	}

	if got := replay.Header().Get(headerIdempotentReplayed); got != "true" {
		t.Fatalf("%s = %q, want true", headerIdempotentReplayed, got)
	}

	if replay.Body.String() != first.Body.String() {
		t.Fatalf("replay body = %s, want %s", replay.Body.String(), first.Body.String())
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

	var outboxCount int
	err = pool.QueryRow(
		context.Background(),
		`
SELECT count(*)
FROM outbox_events
WHERE aggregate_type = $1
  AND event_type = $2
  AND aggregate_id IN (
      SELECT id
      FROM reward_claims
      WHERE player_id = $3 AND campaign_id = $4 AND reward_id = $5
  )`,
		httpIntegrationOutboxAggregateRewardClaim,
		httpIntegrationOutboxEventRewardClaimed,
		playerID,
		campaignID,
		rewardID,
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("count outbox events: %v", err)
	}

	if outboxCount != 1 {
		t.Fatalf("outbox event count = %d, want 1", outboxCount)
	}
}

func performRewardClaimRequest(t *testing.T, service rewardClaimCreator, idempotencyKey, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, routeRewardClaims, strings.NewReader(body))
	req.Header.Set(headerIdempotencyKey, idempotencyKey)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	rewardClaimsHandler(service).ServeHTTP(rec, req)

	return rec
}

func cleanupHTTPIntegrationRewardClaim(
	t *testing.T,
	pool *pgxpool.Pool,
	keyHash []byte,
	playerID string,
	campaignID string,
	rewardID string,
) {
	t.Helper()

	_, err := pool.Exec(
		context.Background(),
		"DELETE FROM idempotency_keys WHERE operation = $1 AND key_hash = $2",
		idempotency.RewardClaimOperation,
		keyHash,
	)
	if err != nil {
		t.Fatalf("cleanup idempotency key: %v", err)
	}

	_, err = pool.Exec(
		context.Background(),
		`
DELETE FROM outbox_events
WHERE aggregate_type = $1
  AND aggregate_id IN (
      SELECT id
      FROM reward_claims
      WHERE player_id = $2 AND campaign_id = $3 AND reward_id = $4
  )`,
		httpIntegrationOutboxAggregateRewardClaim,
		playerID,
		campaignID,
		rewardID,
	)
	if err != nil {
		t.Fatalf("cleanup outbox events: %v", err)
	}

	_, err = pool.Exec(
		context.Background(),
		"DELETE FROM reward_claims WHERE player_id = $1 AND campaign_id = $2 AND reward_id = $3",
		playerID,
		campaignID,
		rewardID,
	)
	if err != nil {
		t.Fatalf("cleanup reward claims: %v", err)
	}
}

func openHTTPIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if strings.TrimSpace(databaseURL) == "" {
		databaseURL = defaultHTTPIntegrationDatabaseURL
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
