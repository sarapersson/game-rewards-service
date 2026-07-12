//go:build integration

package outbox

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultIntegrationDatabaseURL = "postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable"
	integrationQueryTimeout       = 2 * time.Second
)

func TestPostgresStoreClaimNextClaimsPendingEvent(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, integrationQueryTimeout)

	eventID := integrationEventID(t)
	aggregateID := integrationEventID(t)
	workerID := integrationWorkerID(t, "worker")

	cleanupIntegrationOutboxEvent(t, pool, eventID)
	t.Cleanup(func() {
		cleanupIntegrationOutboxEvent(t, pool, eventID)
	})

	insertIntegrationOutboxEvent(
		t,
		pool,
		eventID,
		aggregateID,
		StatusPending,
		0,
		time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
	)

	event, claimed, err := store.ClaimNext(context.Background(), workerID, 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}

	if !claimed {
		t.Fatal("expected an event to be claimed")
	}

	if event.ID != eventID {
		t.Fatalf("event id = %q, want %q", event.ID, eventID)
	}

	if event.Status != StatusProcessing {
		t.Fatalf("event status = %q, want %q", event.Status, StatusProcessing)
	}

	var (
		status           string
		lockedBy         string
		lockExpiresLater bool
	)

	err = pool.QueryRow(
		context.Background(),
		`
SELECT
    status,
    locked_by,
    locked_until > now()
FROM outbox_events
WHERE id = $1`,
		eventID,
	).Scan(&status, &lockedBy, &lockExpiresLater)
	if err != nil {
		t.Fatalf("query claimed event: %v", err)
	}

	if status != StatusProcessing {
		t.Fatalf("database status = %q, want %q", status, StatusProcessing)
	}

	if lockedBy != workerID {
		t.Fatalf("locked_by = %q, want %q", lockedBy, workerID)
	}

	if !lockExpiresLater {
		t.Fatal("expected locked_until to be in the future")
	}
}

func TestPostgresStoreClaimNextSkipsFuturePendingEvent(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, integrationQueryTimeout)

	eventID := integrationEventID(t)
	aggregateID := integrationEventID(t)
	workerID := integrationWorkerID(t, "worker")

	cleanupIntegrationOutboxEvent(t, pool, eventID)
	t.Cleanup(func() {
		cleanupIntegrationOutboxEvent(t, pool, eventID)
	})

	insertIntegrationOutboxEvent(
		t,
		pool,
		eventID,
		aggregateID,
		StatusPending,
		0,
		integrationDatabaseNow(t, pool).Add(time.Hour),
	)

	_, claimed, err := store.ClaimNext(context.Background(), workerID, 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}

	if claimed {
		t.Fatalf("future event %q should not have been claimed", eventID)
	}

	var (
		status      string
		lockedBy    *string
		lockedUntil *time.Time
	)

	err = pool.QueryRow(
		context.Background(),
		`
SELECT status, locked_by, locked_until
FROM outbox_events
WHERE id = $1`,
		eventID,
	).Scan(&status, &lockedBy, &lockedUntil)
	if err != nil {
		t.Fatalf("query future event: %v", err)
	}

	if status != StatusPending {
		t.Fatalf("status = %q, want %q", status, StatusPending)
	}

	if lockedBy != nil {
		t.Fatalf("locked_by = %q, want nil", *lockedBy)
	}

	if lockedUntil != nil {
		t.Fatalf("locked_until = %s, want nil", lockedUntil)
	}
}

func TestPostgresStoreClaimNextReclaimsExpiredProcessingEvent(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, integrationQueryTimeout)

	eventID := integrationEventID(t)
	aggregateID := integrationEventID(t)
	oldWorkerID := integrationWorkerID(t, "old-worker")
	newWorkerID := integrationWorkerID(t, "new-worker")

	cleanupIntegrationOutboxEvent(t, pool, eventID)
	t.Cleanup(func() {
		cleanupIntegrationOutboxEvent(t, pool, eventID)
	})

	insertIntegrationProcessingOutboxEvent(
		t,
		pool,
		eventID,
		aggregateID,
		oldWorkerID,
		integrationDatabaseNow(t, pool).Add(-time.Minute),
	)

	event, claimed, err := store.ClaimNext(context.Background(), newWorkerID, 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}

	if !claimed {
		t.Fatal("expected expired processing event to be reclaimed")
	}

	if event.ID != eventID {
		t.Fatalf("event id = %q, want %q", event.ID, eventID)
	}

	var (
		status           string
		lockedBy         string
		lockExpiresLater bool
	)

	err = pool.QueryRow(
		context.Background(),
		`
SELECT
    status,
    locked_by,
    locked_until > now()
FROM outbox_events
WHERE id = $1`,
		eventID,
	).Scan(&status, &lockedBy, &lockExpiresLater)
	if err != nil {
		t.Fatalf("query reclaimed event: %v", err)
	}

	if status != StatusProcessing {
		t.Fatalf("status = %q, want %q", status, StatusProcessing)
	}

	if lockedBy != newWorkerID {
		t.Fatalf("locked_by = %q, want %q", lockedBy, newWorkerID)
	}

	if !lockExpiresLater {
		t.Fatal("expected reclaimed lease to expire in the future")
	}
}

func TestPostgresStoreClaimNextSkipsActiveProcessingEvent(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, integrationQueryTimeout)

	eventID := integrationEventID(t)
	aggregateID := integrationEventID(t)
	ownerWorkerID := integrationWorkerID(t, "owner-worker")
	otherWorkerID := integrationWorkerID(t, "other-worker")

	cleanupIntegrationOutboxEvent(t, pool, eventID)
	t.Cleanup(func() {
		cleanupIntegrationOutboxEvent(t, pool, eventID)
	})

	insertIntegrationProcessingOutboxEvent(
		t,
		pool,
		eventID,
		aggregateID,
		ownerWorkerID,
		integrationDatabaseNow(t, pool).Add(time.Hour),
	)

	_, claimed, err := store.ClaimNext(context.Background(), otherWorkerID, 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}

	if claimed {
		t.Fatalf("active processing event %q should not have been claimed", eventID)
	}

	var (
		status   string
		lockedBy string
	)

	err = pool.QueryRow(
		context.Background(),
		`
SELECT status, locked_by
FROM outbox_events
WHERE id = $1`,
		eventID,
	).Scan(&status, &lockedBy)
	if err != nil {
		t.Fatalf("query active processing event: %v", err)
	}

	if status != StatusProcessing {
		t.Fatalf("status = %q, want %q", status, StatusProcessing)
	}

	if lockedBy != ownerWorkerID {
		t.Fatalf("locked_by = %q, want %q", lockedBy, ownerWorkerID)
	}
}

func TestPostgresStoreClaimNextDoesNotDoubleClaimConcurrently(t *testing.T) {
	firstPool := openIntegrationPool(t)
	secondPool := openIntegrationPool(t)

	firstStore := NewPostgresStore(firstPool, integrationQueryTimeout)
	secondStore := NewPostgresStore(secondPool, integrationQueryTimeout)

	eventID := integrationEventID(t)
	aggregateID := integrationEventID(t)
	firstWorkerID := integrationWorkerID(t, "first-worker")
	secondWorkerID := integrationWorkerID(t, "second-worker")

	cleanupIntegrationOutboxEvent(t, firstPool, eventID)
	t.Cleanup(func() {
		cleanupIntegrationOutboxEvent(t, firstPool, eventID)
	})

	insertIntegrationOutboxEvent(
		t,
		firstPool,
		eventID,
		aggregateID,
		StatusPending,
		0,
		time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
	)

	type claimResult struct {
		workerID string
		event    Event
		claimed  bool
		err      error
	}

	start := make(chan struct{})
	results := make(chan claimResult, 2)

	claim := func(store *PostgresStore, workerID string) {
		<-start

		event, claimed, err := store.ClaimNext(context.Background(), workerID, time.Hour)
		results <- claimResult{
			workerID: workerID,
			event:    event,
			claimed:  claimed,
			err:      err,
		}
	}

	go claim(firstStore, firstWorkerID)
	go claim(secondStore, secondWorkerID)

	close(start)

	var claimResults []claimResult

	for range 2 {
		select {
		case result := <-results:
			claimResults = append(claimResults, result)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for concurrent claims")
		}
	}

	claimedCount := 0
	winningWorkerID := ""

	for _, result := range claimResults {
		if result.err != nil {
			t.Fatalf("worker %q ClaimNext returned error: %v", result.workerID, result.err)
		}

		if !result.claimed {
			continue
		}

		claimedCount++
		winningWorkerID = result.workerID

		if result.event.ID != eventID {
			t.Fatalf(
				"worker %q claimed event %q, want %q",
				result.workerID,
				result.event.ID,
				eventID,
			)
		}
	}

	if claimedCount != 1 {
		t.Fatalf("claimed count = %d, want exactly 1", claimedCount)
	}

	var lockedBy string

	err := firstPool.QueryRow(
		context.Background(),
		`
SELECT locked_by
FROM outbox_events
WHERE id = $1`,
		eventID,
	).Scan(&lockedBy)
	if err != nil {
		t.Fatalf("query claimed event owner: %v", err)
	}

	if lockedBy != winningWorkerID {
		t.Fatalf("locked_by = %q, want winning worker %q", lockedBy, winningWorkerID)
	}
}

func TestPostgresStoreMarkPublished(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, integrationQueryTimeout)

	eventID := integrationEventID(t)
	aggregateID := integrationEventID(t)
	workerID := integrationWorkerID(t, "worker")

	cleanupIntegrationOutboxEvent(t, pool, eventID)
	t.Cleanup(func() {
		cleanupIntegrationOutboxEvent(t, pool, eventID)
	})

	insertIntegrationProcessingOutboxEvent(
		t,
		pool,
		eventID,
		aggregateID,
		workerID,
		integrationDatabaseNow(t, pool).Add(time.Hour),
	)

	if err := store.MarkPublished(context.Background(), workerID, eventID); err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	var (
		status      string
		lockedBy    *string
		lockedUntil *time.Time
		publishedAt *time.Time
		lastError   *string
	)

	err := pool.QueryRow(
		context.Background(),
		`
SELECT
    status,
    locked_by,
    locked_until,
    published_at,
    last_error
FROM outbox_events
WHERE id = $1`,
		eventID,
	).Scan(&status, &lockedBy, &lockedUntil, &publishedAt, &lastError)
	if err != nil {
		t.Fatalf("query published event: %v", err)
	}

	if status != StatusPublished {
		t.Fatalf("status = %q, want %q", status, StatusPublished)
	}

	if lockedBy != nil {
		t.Fatalf("locked_by = %q, want nil", *lockedBy)
	}

	if lockedUntil != nil {
		t.Fatalf("locked_until = %s, want nil", lockedUntil)
	}

	if publishedAt == nil || publishedAt.IsZero() {
		t.Fatal("published_at was not set")
	}

	if lastError != nil {
		t.Fatalf("last_error = %q, want nil", *lastError)
	}
}

func TestPostgresStoreScheduleRetryUsesDatabaseTime(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, integrationQueryTimeout)

	eventID := integrationEventID(t)
	aggregateID := integrationEventID(t)
	workerID := integrationWorkerID(t, "worker")
	retryDelay := 30 * time.Second

	cleanupIntegrationOutboxEvent(t, pool, eventID)
	t.Cleanup(func() {
		cleanupIntegrationOutboxEvent(t, pool, eventID)
	})

	insertIntegrationProcessingOutboxEvent(
		t,
		pool,
		eventID,
		aggregateID,
		workerID,
		integrationDatabaseNow(t, pool).Add(time.Hour),
	)

	databaseTimeBefore := integrationDatabaseNow(t, pool)

	nextAvailableAt, err := store.ScheduleRetry(
		context.Background(),
		workerID,
		eventID,
		retryDelay,
		publishFailureFailed,
	)
	if err != nil {
		t.Fatalf("ScheduleRetry returned error: %v", err)
	}

	databaseTimeAfter := integrationDatabaseNow(t, pool)

	earliestExpected := databaseTimeBefore.Add(retryDelay)
	latestExpected := databaseTimeAfter.Add(retryDelay)

	if nextAvailableAt.Before(earliestExpected) {
		t.Fatalf(
			"next available at %s is before earliest expected %s",
			nextAvailableAt,
			earliestExpected,
		)
	}

	if nextAvailableAt.After(latestExpected) {
		t.Fatalf(
			"next available at %s is after latest expected %s",
			nextAvailableAt,
			latestExpected,
		)
	}

	var (
		status      string
		attempts    int
		availableAt time.Time
		lockedBy    *string
		lockedUntil *time.Time
		lastError   *string
		publishedAt *time.Time
	)

	err = pool.QueryRow(
		context.Background(),
		`
SELECT
    status,
    attempts,
    available_at,
    locked_by,
    locked_until,
    last_error,
    published_at
FROM outbox_events
WHERE id = $1`,
		eventID,
	).Scan(
		&status,
		&attempts,
		&availableAt,
		&lockedBy,
		&lockedUntil,
		&lastError,
		&publishedAt,
	)
	if err != nil {
		t.Fatalf("query retry event: %v", err)
	}

	if status != StatusPending {
		t.Fatalf("status = %q, want %q", status, StatusPending)
	}

	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}

	if !availableAt.Equal(nextAvailableAt) {
		t.Fatalf("stored available_at = %s, returned %s", availableAt, nextAvailableAt)
	}

	if lockedBy != nil {
		t.Fatalf("locked_by = %q, want nil", *lockedBy)
	}

	if lockedUntil != nil {
		t.Fatalf("locked_until = %s, want nil", lockedUntil)
	}

	if lastError == nil || *lastError != publishFailureFailed {
		t.Fatalf("last_error = %v, want %q", lastError, publishFailureFailed)
	}

	if publishedAt != nil {
		t.Fatalf("published_at = %s, want nil", publishedAt)
	}
}

func TestPostgresStoreMarkDeadLetter(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, integrationQueryTimeout)

	eventID := integrationEventID(t)
	aggregateID := integrationEventID(t)
	workerID := integrationWorkerID(t, "worker")

	cleanupIntegrationOutboxEvent(t, pool, eventID)
	t.Cleanup(func() {
		cleanupIntegrationOutboxEvent(t, pool, eventID)
	})

	insertIntegrationProcessingOutboxEvent(
		t,
		pool,
		eventID,
		aggregateID,
		workerID,
		integrationDatabaseNow(t, pool).Add(time.Hour),
	)

	if err := store.MarkDeadLetter(
		context.Background(),
		workerID,
		eventID,
		publishFailureFailed,
	); err != nil {
		t.Fatalf("MarkDeadLetter returned error: %v", err)
	}

	var (
		status         string
		attempts       int
		lockedBy       *string
		lockedUntil    *time.Time
		lastError      *string
		publishedAt    *time.Time
		deadLetteredAt *time.Time
	)

	err := pool.QueryRow(
		context.Background(),
		`
SELECT
    status,
    attempts,
    locked_by,
    locked_until,
    last_error,
    published_at,
    dead_lettered_at
FROM outbox_events
WHERE id = $1`,
		eventID,
	).Scan(
		&status,
		&attempts,
		&lockedBy,
		&lockedUntil,
		&lastError,
		&publishedAt,
		&deadLetteredAt,
	)
	if err != nil {
		t.Fatalf("query dead-letter event: %v", err)
	}

	if status != StatusDeadLetter {
		t.Fatalf("status = %q, want %q", status, StatusDeadLetter)
	}

	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}

	if lockedBy != nil {
		t.Fatalf("locked_by = %q, want nil", *lockedBy)
	}

	if lockedUntil != nil {
		t.Fatalf("locked_until = %s, want nil", lockedUntil)
	}

	if lastError == nil || *lastError != publishFailureFailed {
		t.Fatalf("last_error = %v, want %q", lastError, publishFailureFailed)
	}

	if publishedAt != nil {
		t.Fatalf("published_at = %s, want nil", publishedAt)
	}

	if deadLetteredAt == nil || deadLetteredAt.IsZero() {
		t.Fatal("dead_lettered_at was not set")
	}
}

func TestPostgresStoreAllowsLeaseOwnerToCompleteExpiredUnreclaimedLease(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, integrationQueryTimeout)

	eventID := integrationEventID(t)
	aggregateID := integrationEventID(t)
	workerID := integrationWorkerID(t, "worker")

	cleanupIntegrationOutboxEvent(t, pool, eventID)
	t.Cleanup(func() {
		cleanupIntegrationOutboxEvent(t, pool, eventID)
	})

	insertIntegrationProcessingOutboxEvent(
		t,
		pool,
		eventID,
		aggregateID,
		workerID,
		integrationDatabaseNow(t, pool).Add(-time.Minute),
	)

	if err := store.MarkPublished(context.Background(), workerID, eventID); err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	var (
		status      string
		lockedBy    *string
		lockedUntil *time.Time
		publishedAt *time.Time
	)

	err := pool.QueryRow(
		context.Background(),
		`
SELECT status, locked_by, locked_until, published_at
FROM outbox_events
WHERE id = $1`,
		eventID,
	).Scan(&status, &lockedBy, &lockedUntil, &publishedAt)
	if err != nil {
		t.Fatalf("query completed event: %v", err)
	}

	if status != StatusPublished {
		t.Fatalf("status = %q, want %q", status, StatusPublished)
	}

	if lockedBy != nil {
		t.Fatalf("locked_by = %q, want nil", *lockedBy)
	}

	if lockedUntil != nil {
		t.Fatalf("locked_until = %s, want nil", lockedUntil)
	}

	if publishedAt == nil || publishedAt.IsZero() {
		t.Fatal("published_at was not set")
	}
}

func TestPostgresStoreStaleWorkerCannotOverwriteReclaimedLease(t *testing.T) {
	pool := openIntegrationPool(t)
	store := NewPostgresStore(pool, integrationQueryTimeout)

	eventID := integrationEventID(t)
	aggregateID := integrationEventID(t)
	oldWorkerID := integrationWorkerID(t, "old-worker")
	newWorkerID := integrationWorkerID(t, "new-worker")

	cleanupIntegrationOutboxEvent(t, pool, eventID)
	t.Cleanup(func() {
		cleanupIntegrationOutboxEvent(t, pool, eventID)
	})

	insertIntegrationProcessingOutboxEvent(
		t,
		pool,
		eventID,
		aggregateID,
		oldWorkerID,
		integrationDatabaseNow(t, pool).Add(-time.Minute),
	)

	event, claimed, err := store.ClaimNext(context.Background(), newWorkerID, time.Hour)
	if err != nil {
		t.Fatalf("new worker ClaimNext returned error: %v", err)
	}

	if !claimed {
		t.Fatal("expected new worker to reclaim expired event")
	}

	if event.ID != eventID {
		t.Fatalf("event id = %q, want %q", event.ID, eventID)
	}

	err = store.MarkPublished(context.Background(), oldWorkerID, eventID)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old worker MarkPublished error = %v, want ErrLeaseLost", err)
	}

	_, err = store.ScheduleRetry(
		context.Background(),
		oldWorkerID,
		eventID,
		time.Minute,
		publishFailureFailed,
	)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old worker ScheduleRetry error = %v, want ErrLeaseLost", err)
	}

	err = store.MarkDeadLetter(
		context.Background(),
		oldWorkerID,
		eventID,
		publishFailureFailed,
	)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old worker MarkDeadLetter error = %v, want ErrLeaseLost", err)
	}

	var (
		status   string
		lockedBy string
		attempts int
	)

	err = pool.QueryRow(
		context.Background(),
		`
SELECT status, locked_by, attempts
FROM outbox_events
WHERE id = $1`,
		eventID,
	).Scan(&status, &lockedBy, &attempts)
	if err != nil {
		t.Fatalf("query event after stale writes: %v", err)
	}

	if status != StatusProcessing {
		t.Fatalf("status = %q, want %q", status, StatusProcessing)
	}

	if lockedBy != newWorkerID {
		t.Fatalf("locked_by = %q, want %q", lockedBy, newWorkerID)
	}

	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}

	if err := store.MarkPublished(context.Background(), newWorkerID, eventID); err != nil {
		t.Fatalf("new worker MarkPublished returned error: %v", err)
	}

	var finalStatus string

	err = pool.QueryRow(
		context.Background(),
		`
SELECT status
FROM outbox_events
WHERE id = $1`,
		eventID,
	).Scan(&finalStatus)
	if err != nil {
		t.Fatalf("query completed event: %v", err)
	}

	if finalStatus != StatusPublished {
		t.Fatalf("final status = %q, want %q", finalStatus, StatusPublished)
	}
}

func TestPostgresStoreClaimNextSkipsTerminalEvents(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{name: "published", status: StatusPublished},
		{name: "dead letter", status: StatusDeadLetter},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := openIntegrationPool(t)
			store := NewPostgresStore(pool, integrationQueryTimeout)

			eventID := integrationEventID(t)
			aggregateID := integrationEventID(t)
			workerID := integrationWorkerID(t, "worker")

			cleanupIntegrationOutboxEvent(t, pool, eventID)
			t.Cleanup(func() {
				cleanupIntegrationOutboxEvent(t, pool, eventID)
			})

			insertIntegrationTerminalOutboxEvent(
				t,
				pool,
				eventID,
				aggregateID,
				tt.status,
			)

			_, claimed, err := store.ClaimNext(
				context.Background(),
				workerID,
				30*time.Second,
			)
			if err != nil {
				t.Fatalf("ClaimNext returned error: %v", err)
			}

			if claimed {
				t.Fatalf("%s event %q should not have been claimed", tt.status, eventID)
			}

			var storedStatus string

			err = pool.QueryRow(
				context.Background(),
				`
SELECT status
FROM outbox_events
WHERE id = $1`,
				eventID,
			).Scan(&storedStatus)
			if err != nil {
				t.Fatalf("query terminal event: %v", err)
			}

			if storedStatus != tt.status {
				t.Fatalf("status = %q, want %q", storedStatus, tt.status)
			}
		})
	}
}

func TestPostgresStoreAppliesQueryTimeout(t *testing.T) {
	lockPool := openIntegrationPool(t)
	workerPool := openIntegrationPool(t)

	store := NewPostgresStore(workerPool, 100*time.Millisecond)

	lockCtx, cancelLock := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelLock()

	tx, err := lockPool.Begin(lockCtx)
	if err != nil {
		t.Fatalf("begin table-lock transaction: %v", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	_, err = tx.Exec(lockCtx, "LOCK TABLE outbox_events IN ACCESS EXCLUSIVE MODE")
	if err != nil {
		t.Fatalf("lock outbox_events table: %v", err)
	}

	_, _, err = store.ClaimNext(
		context.Background(),
		integrationWorkerID(t, "worker"),
		30*time.Second,
	)
	if err == nil {
		t.Fatal("expected ClaimNext to time out, got nil")
	}

	if !errors.Is(err, context.DeadlineExceeded) &&
		!strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("ClaimNext error = %v, want context deadline exceeded", err)
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

	poolConfig.MaxConns = 4

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

func insertIntegrationOutboxEvent(
	t *testing.T,
	pool *pgxpool.Pool,
	eventID string,
	aggregateID string,
	status string,
	attempts int,
	availableAt time.Time,
) {
	t.Helper()

	_, err := pool.Exec(
		context.Background(),
		`
INSERT INTO outbox_events (
    id,
    aggregate_type,
    aggregate_id,
    event_type,
    payload,
    status,
    attempts,
    available_at
)
VALUES (
    $1,
    'reward_claim',
    $2,
    'RewardClaimed',
    '{"schema_version":1}',
    $3,
    $4,
    $5
)`,
		eventID,
		aggregateID,
		status,
		attempts,
		availableAt,
	)
	if err != nil {
		t.Fatalf("insert outbox event: %v", err)
	}
}

func insertIntegrationProcessingOutboxEvent(
	t *testing.T,
	pool *pgxpool.Pool,
	eventID string,
	aggregateID string,
	workerID string,
	lockedUntil time.Time,
) {
	t.Helper()

	_, err := pool.Exec(
		context.Background(),
		`
INSERT INTO outbox_events (
    id,
    aggregate_type,
    aggregate_id,
    event_type,
    payload,
    status,
    attempts,
    available_at,
    locked_by,
    locked_until
)
VALUES (
    $1,
    'reward_claim',
    $2,
    'RewardClaimed',
    '{"schema_version":1}',
    'processing',
    1,
    $3,
    $4,
    $5
)`,
		eventID,
		aggregateID,
		time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
		workerID,
		lockedUntil,
	)
	if err != nil {
		t.Fatalf("insert processing outbox event: %v", err)
	}
}

func insertIntegrationTerminalOutboxEvent(
	t *testing.T,
	pool *pgxpool.Pool,
	eventID string,
	aggregateID string,
	status string,
) {
	t.Helper()

	availableAt := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)

	switch status {
	case StatusPublished:
		_, err := pool.Exec(
			context.Background(),
			`
INSERT INTO outbox_events (
    id,
    aggregate_type,
    aggregate_id,
    event_type,
    payload,
    status,
    attempts,
    available_at,
    published_at
)
VALUES (
    $1,
    'reward_claim',
    $2,
    'RewardClaimed',
    '{"schema_version":1}',
    'published',
    0,
    $3,
    now()
)`,
			eventID,
			aggregateID,
			availableAt,
		)
		if err != nil {
			t.Fatalf("insert published outbox event: %v", err)
		}

	case StatusDeadLetter:
		_, err := pool.Exec(
			context.Background(),
			`
INSERT INTO outbox_events (
    id,
    aggregate_type,
    aggregate_id,
    event_type,
    payload,
    status,
    attempts,
    available_at,
    last_error,
    dead_lettered_at
)
VALUES (
    $1,
    'reward_claim',
    $2,
    'RewardClaimed',
    '{"schema_version":1}',
    'dead_letter',
    5,
    $3,
    $4,
    now()
)`,
			eventID,
			aggregateID,
			availableAt,
			publishFailureFailed,
		)
		if err != nil {
			t.Fatalf("insert dead-letter outbox event: %v", err)
		}

	default:
		t.Fatalf("unsupported terminal status %q", status)
	}
}

func cleanupIntegrationOutboxEvent(t *testing.T, pool *pgxpool.Pool, eventID string) {
	t.Helper()

	_, err := pool.Exec(
		context.Background(),
		"DELETE FROM outbox_events WHERE id = $1",
		eventID,
	)
	if err != nil {
		t.Fatalf("cleanup outbox event: %v", err)
	}
}

func integrationDatabaseNow(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()

	var now time.Time

	err := pool.QueryRow(
		context.Background(),
		"SELECT clock_timestamp()",
	).Scan(&now)
	if err != nil {
		t.Fatalf("query database time: %v", err)
	}

	return now
}

func integrationWorkerID(t *testing.T, prefix string) string {
	t.Helper()

	return prefix + "-" + strings.ReplaceAll(t.Name(), "/", "-")
}

func integrationEventID(t *testing.T) string {
	t.Helper()

	sum := sha256.Sum256(
		[]byte(fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())),
	)

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		sum[0:4],
		sum[4:6],
		sum[6:8],
		sum[8:10],
		sum[10:16],
	)
}
