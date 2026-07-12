package outbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerRunOncePublishesClaimedEvent(t *testing.T) {
	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 0),
		},
	}

	publisher := &fakePublisher{}
	worker := newTestWorker(t, store, publisher)

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if processed != 1 {
		t.Fatalf("expected 1 processed event, got %d", processed)
	}

	if len(publisher.published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(publisher.published))
	}

	if publisher.published[0].ID != "event-1" {
		t.Fatalf("expected event-1 to be published, got %q", publisher.published[0].ID)
	}

	if store.publishedEventID != "event-1" {
		t.Fatalf("expected event-1 to be marked published, got %q", store.publishedEventID)
	}

	if store.retryEventID != "" {
		t.Fatalf("expected no retry, got %q", store.retryEventID)
	}

	if store.deadLetterEventID != "" {
		t.Fatalf("expected no dead-letter, got %q", store.deadLetterEventID)
	}
}

func TestWorkerRunOnceClaimsOnlyOneEvent(t *testing.T) {
	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 0),
			testEvent("event-2", 0),
		},
	}

	publisher := &fakePublisher{}
	worker := newTestWorker(t, store, publisher)

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if processed != 1 {
		t.Fatalf("expected 1 processed event, got %d", processed)
	}

	if len(publisher.published) != 1 {
		t.Fatalf("expected exactly 1 published event, got %d", len(publisher.published))
	}

	if publisher.published[0].ID != "event-1" {
		t.Fatalf("expected event-1 to be published, got %q", publisher.published[0].ID)
	}

	if len(store.claimed) != 1 {
		t.Fatalf("expected one event to remain unclaimed, got %d", len(store.claimed))
	}

	if store.claimed[0].ID != "event-2" {
		t.Fatalf("expected event-2 to remain unclaimed, got %q", store.claimed[0].ID)
	}
}

func TestWorkerRunOnceReturnsZeroWhenNoEventIsDue(t *testing.T) {
	store := &fakeStore{}
	publisher := &fakePublisher{}
	worker := newTestWorker(t, store, publisher)

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if processed != 0 {
		t.Fatalf("expected 0 processed events, got %d", processed)
	}

	if len(publisher.published) != 0 {
		t.Fatalf("expected no published events, got %d", len(publisher.published))
	}
}

func TestWorkerRunOncePropagatesClaimError(t *testing.T) {
	claimErr := errors.New("claim failed")
	store := &fakeStore{claimErr: claimErr}
	publisher := &fakePublisher{}
	worker := newTestWorker(t, store, publisher)

	processed, err := worker.RunOnce(context.Background())
	if !errors.Is(err, claimErr) {
		t.Fatalf("expected claim error, got %v", err)
	}

	if processed != 0 {
		t.Fatalf("expected 0 processed events, got %d", processed)
	}

	if len(publisher.published) != 0 {
		t.Fatalf("expected no published events, got %d", len(publisher.published))
	}
}

func TestWorkerRunOnceSchedulesRetryOnPublishFailure(t *testing.T) {
	nextAvailableAt := time.Date(2026, time.July, 11, 12, 0, 2, 0, time.UTC)

	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 1),
		},
		retryAvailableAt: nextAvailableAt,
	}

	publisher := &fakePublisher{
		err: errors.New("temporary publish failure"),
	}

	worker := newTestWorker(t, store, publisher)

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if processed != 1 {
		t.Fatalf("expected 1 processed event, got %d", processed)
	}

	if store.retryEventID != "event-1" {
		t.Fatalf("expected event-1 to be scheduled for retry, got %q", store.retryEventID)
	}

	if store.retryDelay != 2*time.Second {
		t.Fatalf("expected retry delay 2s, got %s", store.retryDelay)
	}

	if store.lastError != publishFailureFailed {
		t.Fatalf("expected failure reason %q, got %q", publishFailureFailed, store.lastError)
	}

	if store.publishedEventID != "" {
		t.Fatalf("expected event not to be marked published, got %q", store.publishedEventID)
	}

	if store.deadLetterEventID != "" {
		t.Fatalf("expected event not to be dead-lettered, got %q", store.deadLetterEventID)
	}
}

func TestWorkerRunOncePropagatesRetrySchedulingError(t *testing.T) {
	retryErr := errors.New("schedule retry failed")

	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 0),
		},
		scheduleRetryErr: retryErr,
	}

	publisher := &fakePublisher{
		err: errors.New("temporary publish failure"),
	}

	worker := newTestWorker(t, store, publisher)

	processed, err := worker.RunOnce(context.Background())
	if !errors.Is(err, retryErr) {
		t.Fatalf("expected retry scheduling error, got %v", err)
	}

	if processed != 0 {
		t.Fatalf("expected 0 processed events, got %d", processed)
	}

	if store.retryEventID != "event-1" {
		t.Fatalf("expected retry scheduling for event-1, got %q", store.retryEventID)
	}

	if store.publishedEventID != "" {
		t.Fatalf("expected event not to be marked published, got %q", store.publishedEventID)
	}

	if store.deadLetterEventID != "" {
		t.Fatalf("expected event not to be dead-lettered, got %q", store.deadLetterEventID)
	}
}

func TestWorkerRunOnceSchedulesRetryOnPublishTimeout(t *testing.T) {
	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 0),
		},
		retryAvailableAt: time.Date(2026, time.July, 11, 12, 0, 1, 0, time.UTC),
	}

	publisher := &blockingPublisher{}
	worker := newTestWorker(t, store, publisher)
	worker.publishTimeout = 5 * time.Millisecond

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if processed != 1 {
		t.Fatalf("expected 1 processed event, got %d", processed)
	}

	if store.retryEventID != "event-1" {
		t.Fatalf("expected event-1 to be scheduled for retry, got %q", store.retryEventID)
	}

	if store.retryDelay != time.Second {
		t.Fatalf("expected retry delay 1s, got %s", store.retryDelay)
	}

	if store.lastError != publishFailureTimeout {
		t.Fatalf("expected failure reason %q, got %q", publishFailureTimeout, store.lastError)
	}
}

func TestWorkerRunOnceClassifiesPublisherCancellation(t *testing.T) {
	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 0),
		},
		retryAvailableAt: time.Date(2026, time.July, 11, 12, 0, 1, 0, time.UTC),
	}

	publisher := &fakePublisher{err: context.Canceled}
	worker := newTestWorker(t, store, publisher)

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if processed != 1 {
		t.Fatalf("expected 1 processed event, got %d", processed)
	}

	if store.lastError != publishFailureCanceled {
		t.Fatalf("expected failure reason %q, got %q", publishFailureCanceled, store.lastError)
	}
}

func TestWorkerRunOnceDeadLettersAfterMaxAttempts(t *testing.T) {
	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 4),
		},
	}

	publisher := &fakePublisher{
		err: errors.New("permanent publish failure"),
	}

	worker := newTestWorker(t, store, publisher)

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if processed != 1 {
		t.Fatalf("expected 1 processed event, got %d", processed)
	}

	if store.deadLetterEventID != "event-1" {
		t.Fatalf("expected event-1 to be dead-lettered, got %q", store.deadLetterEventID)
	}

	if store.lastError != publishFailureFailed {
		t.Fatalf("expected failure reason %q, got %q", publishFailureFailed, store.lastError)
	}

	if store.retryEventID != "" {
		t.Fatalf("expected no retry to be scheduled, got %q", store.retryEventID)
	}

	if store.publishedEventID != "" {
		t.Fatalf("expected event not to be marked published, got %q", store.publishedEventID)
	}
}

func TestWorkerRunOncePropagatesDeadLetterError(t *testing.T) {
	deadLetterErr := errors.New("dead-letter update failed")

	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 4),
		},
		markDeadLetterErr: deadLetterErr,
	}

	publisher := &fakePublisher{
		err: errors.New("permanent publish failure"),
	}

	worker := newTestWorker(t, store, publisher)

	processed, err := worker.RunOnce(context.Background())
	if !errors.Is(err, deadLetterErr) {
		t.Fatalf("expected dead-letter error, got %v", err)
	}

	if processed != 0 {
		t.Fatalf("expected 0 processed events, got %d", processed)
	}

	if store.deadLetterEventID != "event-1" {
		t.Fatalf("expected dead-letter update for event-1, got %q", store.deadLetterEventID)
	}

	if store.retryEventID != "" {
		t.Fatalf("expected no retry to be scheduled, got %q", store.retryEventID)
	}

	if store.publishedEventID != "" {
		t.Fatalf("expected event not to be marked published, got %q", store.publishedEventID)
	}
}

func TestWorkerRunOnceRetriesBeforeMaxAttempts(t *testing.T) {
	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 3),
		},
		retryAvailableAt: time.Date(2026, time.July, 11, 12, 0, 8, 0, time.UTC),
	}

	publisher := &fakePublisher{
		err: errors.New("temporary publish failure"),
	}

	worker := newTestWorker(t, store, publisher)

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if processed != 1 {
		t.Fatalf("expected 1 processed event, got %d", processed)
	}

	if store.retryEventID != "event-1" {
		t.Fatalf("expected event-1 to be retried, got %q", store.retryEventID)
	}

	if store.retryDelay != 8*time.Second {
		t.Fatalf("expected retry delay 8s, got %s", store.retryDelay)
	}

	if store.deadLetterEventID != "" {
		t.Fatalf("expected event not to be dead-lettered, got %q", store.deadLetterEventID)
	}
}

func TestWorkerRunOnceDoesNotRetryOnShutdownCancellation(t *testing.T) {
	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 1),
		},
	}

	publisher := &blockingPublisher{
		started: make(chan struct{}),
	}

	worker := newTestWorker(t, store, publisher)

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan runOnceResult, 1)

	go func() {
		processed, err := worker.RunOnce(ctx)
		resultCh <- runOnceResult{
			processed: processed,
			err:       err,
		}
	}()

	select {
	case <-publisher.started:
	case <-time.After(time.Second):
		t.Fatal("publisher did not start")
	}

	cancel()

	select {
	case result := <-resultCh:
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", result.err)
		}

		if result.processed != 0 {
			t.Fatalf("expected 0 processed events, got %d", result.processed)
		}
	case <-time.After(time.Second):
		t.Fatal("RunOnce did not stop after cancellation")
	}

	if store.retryEventID != "" {
		t.Fatalf("expected no retry on shutdown, got %q", store.retryEventID)
	}

	if store.deadLetterEventID != "" {
		t.Fatalf("expected no dead-letter on shutdown, got %q", store.deadLetterEventID)
	}

	if store.publishedEventID != "" {
		t.Fatalf("expected no published marker on shutdown, got %q", store.publishedEventID)
	}
}

func TestWorkerRunOncePropagatesCompletionLeaseLoss(t *testing.T) {
	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 0),
		},
		markPublishedErr: ErrLeaseLost,
	}

	publisher := &fakePublisher{}
	worker := newTestWorker(t, store, publisher)

	processed, err := worker.RunOnce(context.Background())
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost, got %v", err)
	}

	if processed != 0 {
		t.Fatalf("expected 0 processed events, got %d", processed)
	}

	if len(publisher.published) != 1 {
		t.Fatalf("expected publisher to have sent the event once, got %d", len(publisher.published))
	}
}

func TestWorkerDoesNotExposeRawPublisherError(t *testing.T) {
	const secret = "Bearer super-secret-token"

	var logs bytes.Buffer

	store := &fakeStore{
		claimed: []Event{
			testEvent("event-1", 0),
		},
		retryAvailableAt: time.Date(2026, time.July, 11, 12, 0, 1, 0, time.UTC),
	}

	publisher := &fakePublisher{
		err: errors.New("request failed with Authorization: " + secret),
	}

	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	worker := newTestWorkerWithLogger(t, store, publisher, logger)

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if processed != 1 {
		t.Fatalf("expected 1 processed event, got %d", processed)
	}

	if store.lastError != publishFailureFailed {
		t.Fatalf("expected persisted failure reason %q, got %q", publishFailureFailed, store.lastError)
	}

	if strings.Contains(logs.String(), secret) {
		t.Fatal("expected raw publisher error secret not to be logged")
	}

	if strings.Contains(logs.String(), "Authorization") {
		t.Fatal("expected raw publisher error not to be logged")
	}
}

func TestWorkerRunWaitsBetweenIdleAndFailedIterations(t *testing.T) {
	tests := []struct {
		name     string
		claimErr error
	}{
		{name: "idle queue"},
		{name: "database failure", claimErr: errors.New("database unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{
				claimErr:    tt.claimErr,
				claimSignal: make(chan struct{}),
			}

			worker := newTestWorker(t, store, &fakePublisher{})
			worker.pollInterval = time.Second

			ctx, cancel := context.WithCancel(context.Background())
			runResult := make(chan error, 1)

			go func() {
				runResult <- worker.Run(ctx)
			}()

			select {
			case <-store.claimSignal:
			case <-time.After(time.Second):
				cancel()
				t.Fatal("worker did not run its first iteration")
			}

			time.Sleep(25 * time.Millisecond)

			if got := store.claimCalls.Load(); got != 1 {
				cancel()
				t.Fatalf("expected exactly 1 claim attempt before poll interval, got %d", got)
			}

			cancel()

			select {
			case err := <-runResult:
				if err != nil {
					t.Fatalf("expected clean shutdown, got %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("worker did not stop after cancellation")
			}
		})
	}
}

func TestNewWorkerRejectsInvalidConfig(t *testing.T) {
	validStore := &fakeStore{}
	validPublisher := &fakePublisher{}
	validLogger := newDiscardLogger()

	validConfig := WorkerConfig{
		WorkerID:       "worker-test",
		PollInterval:   time.Millisecond,
		LockTTL:        30 * time.Second,
		PublishTimeout: 5 * time.Second,
		MaxAttempts:    5,
		Backoff: BackoffPolicy{
			Base: time.Second,
			Max:  time.Minute,
		},
	}

	tests := []struct {
		name      string
		store     Store
		publisher Publisher
		logger    *slog.Logger
		config    WorkerConfig
	}{
		{
			name:      "nil store",
			publisher: validPublisher,
			logger:    validLogger,
			config:    validConfig,
		},
		{
			name:   "nil publisher",
			store:  validStore,
			logger: validLogger,
			config: validConfig,
		},
		{
			name:      "nil logger",
			store:     validStore,
			publisher: validPublisher,
			config:    validConfig,
		},
		{
			name:      "empty worker id",
			store:     validStore,
			publisher: validPublisher,
			logger:    validLogger,
			config: mutateWorkerConfig(validConfig, func(cfg *WorkerConfig) {
				cfg.WorkerID = ""
			}),
		},
		{
			name:      "worker id too long",
			store:     validStore,
			publisher: validPublisher,
			logger:    validLogger,
			config: mutateWorkerConfig(validConfig, func(cfg *WorkerConfig) {
				cfg.WorkerID = strings.Repeat("w", maxWorkerIDLength+1)
			}),
		},
		{
			name:      "non-positive poll interval",
			store:     validStore,
			publisher: validPublisher,
			logger:    validLogger,
			config: mutateWorkerConfig(validConfig, func(cfg *WorkerConfig) {
				cfg.PollInterval = 0
			}),
		},
		{
			name:      "non-positive lock ttl",
			store:     validStore,
			publisher: validPublisher,
			logger:    validLogger,
			config: mutateWorkerConfig(validConfig, func(cfg *WorkerConfig) {
				cfg.LockTTL = 0
			}),
		},
		{
			name:      "non-positive publish timeout",
			store:     validStore,
			publisher: validPublisher,
			logger:    validLogger,
			config: mutateWorkerConfig(validConfig, func(cfg *WorkerConfig) {
				cfg.PublishTimeout = 0
			}),
		},
		{
			name:      "lock ttl not greater than publish timeout",
			store:     validStore,
			publisher: validPublisher,
			logger:    validLogger,
			config: mutateWorkerConfig(validConfig, func(cfg *WorkerConfig) {
				cfg.LockTTL = cfg.PublishTimeout
			}),
		},
		{
			name:      "non-positive max attempts",
			store:     validStore,
			publisher: validPublisher,
			logger:    validLogger,
			config: mutateWorkerConfig(validConfig, func(cfg *WorkerConfig) {
				cfg.MaxAttempts = 0
			}),
		},
		{
			name:      "invalid backoff",
			store:     validStore,
			publisher: validPublisher,
			logger:    validLogger,
			config: mutateWorkerConfig(validConfig, func(cfg *WorkerConfig) {
				cfg.Backoff = BackoffPolicy{
					Base: time.Minute,
					Max:  time.Second,
				}
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker, err := NewWorker(tt.store, tt.publisher, tt.logger, tt.config)
			if err == nil {
				t.Fatalf("expected error, got worker %#v", worker)
			}
		})
	}
}

func newTestWorker(t *testing.T, store Store, publisher Publisher) *Worker {
	t.Helper()

	return newTestWorkerWithLogger(t, store, publisher, newDiscardLogger())
}

func newTestWorkerWithLogger(
	t *testing.T,
	store Store,
	publisher Publisher,
	logger *slog.Logger,
) *Worker {
	t.Helper()

	worker, err := NewWorker(
		store,
		publisher,
		logger,
		WorkerConfig{
			WorkerID:       "worker-test",
			PollInterval:   time.Millisecond,
			LockTTL:        30 * time.Second,
			PublishTimeout: 5 * time.Second,
			MaxAttempts:    5,
			Backoff: BackoffPolicy{
				Base: time.Second,
				Max:  time.Minute,
			},
		},
	)
	if err != nil {
		t.Fatalf("expected worker to be created, got %v", err)
	}

	return worker
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mutateWorkerConfig(cfg WorkerConfig, mutate func(*WorkerConfig)) WorkerConfig {
	mutate(&cfg)
	return cfg
}

func testEvent(id string, attempts int) Event {
	return Event{
		ID:            id,
		AggregateType: "reward_claim",
		AggregateID:   "claim-1",
		EventType:     "RewardClaimed",
		Status:        StatusProcessing,
		Attempts:      attempts,
	}
}

type runOnceResult struct {
	processed int
	err       error
}

type fakeStore struct {
	claimed []Event

	claimErr          error
	markPublishedErr  error
	scheduleRetryErr  error
	markDeadLetterErr error

	publishedEventID  string
	retryEventID      string
	deadLetterEventID string
	retryDelay        time.Duration
	retryAvailableAt  time.Time
	lastError         string

	claimCalls      atomic.Int32
	claimSignal     chan struct{}
	claimSignalOnce sync.Once
}

func (s *fakeStore) ClaimNext(
	_ context.Context,
	_ string,
	_ time.Duration,
) (Event, bool, error) {
	s.claimCalls.Add(1)

	if s.claimSignal != nil {
		s.claimSignalOnce.Do(func() {
			close(s.claimSignal)
		})
	}

	if s.claimErr != nil {
		return Event{}, false, s.claimErr
	}

	if len(s.claimed) == 0 {
		return Event{}, false, nil
	}

	event := s.claimed[0]
	s.claimed = s.claimed[1:]

	return event, true, nil
}

func (s *fakeStore) MarkPublished(_ context.Context, _ string, eventID string) error {
	s.publishedEventID = eventID

	if s.markPublishedErr != nil {
		return s.markPublishedErr
	}

	return nil
}

func (s *fakeStore) ScheduleRetry(
	_ context.Context,
	_ string,
	eventID string,
	retryDelay time.Duration,
	lastError string,
) (time.Time, error) {
	s.retryEventID = eventID
	s.retryDelay = retryDelay
	s.lastError = lastError

	if s.scheduleRetryErr != nil {
		return time.Time{}, s.scheduleRetryErr
	}

	return s.retryAvailableAt, nil
}

func (s *fakeStore) MarkDeadLetter(
	_ context.Context,
	_ string,
	eventID string,
	lastError string,
) error {
	s.deadLetterEventID = eventID
	s.lastError = lastError

	if s.markDeadLetterErr != nil {
		return s.markDeadLetterErr
	}

	return nil
}

type fakePublisher struct {
	err       error
	published []Event
}

func (p *fakePublisher) Publish(_ context.Context, event Event) error {
	if p.err != nil {
		return p.err
	}

	p.published = append(p.published, event)

	return nil
}

type blockingPublisher struct {
	started     chan struct{}
	startedOnce sync.Once
}

func (p *blockingPublisher) Publish(ctx context.Context, _ Event) error {
	if p.started != nil {
		p.startedOnce.Do(func() {
			close(p.started)
		})
	}

	<-ctx.Done()

	return ctx.Err()
}
