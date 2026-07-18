package observability

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sarapersson/game-rewards-service/internal/outbox"
	"github.com/sarapersson/game-rewards-service/internal/rewards"
)

func TestDurationBuckets(t *testing.T) {
	want := []float64{
		0.005, 0.010, 0.025, 0.050, 0.100, 0.250,
		0.500, 1.000, 2.500, 5.000, 10.000,
	}
	got := durationBuckets()
	if len(got) != len(want) {
		t.Fatalf("bucket count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bucket[%d] = %v, want %v", i, got[i], want[i])
		}
	}

	got[0] = 99
	if durationBuckets()[0] != want[0] {
		t.Fatal("durationBuckets returned shared mutable state")
	}
}

func TestRegistryIsProcessLocal(t *testing.T) {
	first, err := NewRegistry()
	if err != nil {
		t.Fatalf("create first registry: %v", err)
	}
	second, err := NewRegistry()
	if err != nil {
		t.Fatalf("create second registry: %v", err)
	}

	firstMetrics, err := NewHTTPMetrics(first)
	if err != nil {
		t.Fatalf("register first metrics: %v", err)
	}
	if _, err := NewHTTPMetrics(second); err != nil {
		t.Fatalf("register second metrics: %v", err)
	}

	firstMetrics.ObserveRequest("/livez", http.MethodGet, http.StatusOK, 10*time.Millisecond)

	if got := scrape(t, first); !strings.Contains(got, `game_rewards_http_requests_total{method="GET",route="/livez",status_code="200"} 1`) {
		t.Fatalf("first registry did not contain observation:\n%s", got)
	}
	if got := scrape(t, second); strings.Contains(got, "game_rewards_http_requests_total") {
		t.Fatalf("second registry contained first registry state:\n%s", got)
	}
}

func TestAPIAndWorkerRegistriesExposeOnlyTheirOwnMetricFamilies(t *testing.T) {
	apiRegistry, err := NewRegistry()
	if err != nil {
		t.Fatalf("create API registry: %v", err)
	}
	if _, err := NewHTTPMetrics(apiRegistry); err != nil {
		t.Fatalf("register API HTTP metrics: %v", err)
	}
	apiRewardMetrics, err := NewRewardMetrics(apiRegistry)
	if err != nil {
		t.Fatalf("register API reward metrics: %v", err)
	}
	apiRewardMetrics.ObserveRewardClaim(rewards.CreateClaimResult{StatusCode: http.StatusCreated}, nil)

	workerRegistry, err := NewRegistry()
	if err != nil {
		t.Fatalf("create worker registry: %v", err)
	}
	if _, err := NewHTTPMetrics(workerRegistry); err != nil {
		t.Fatalf("register worker HTTP metrics: %v", err)
	}
	workerOutboxMetrics, err := NewWorkerMetrics(workerRegistry)
	if err != nil {
		t.Fatalf("register worker metrics: %v", err)
	}
	workerOutboxMetrics.ObserveClaim(outbox.ClaimOutcomeEmpty)

	apiMetrics := scrape(t, apiRegistry)
	workerMetrics := scrape(t, workerRegistry)
	if !strings.Contains(apiMetrics, "game_rewards_reward_claim_operations_total") {
		t.Fatalf("API registry did not expose reward metrics:\n%s", apiMetrics)
	}
	if strings.Contains(apiMetrics, "game_rewards_outbox_") {
		t.Fatalf("API registry exposed worker metrics:\n%s", apiMetrics)
	}
	if !strings.Contains(workerMetrics, "game_rewards_outbox_claim_attempts_total") {
		t.Fatalf("worker registry did not expose outbox metrics:\n%s", workerMetrics)
	}
	if strings.Contains(workerMetrics, "game_rewards_reward_claim_") || strings.Contains(workerMetrics, "game_rewards_idempotency_") {
		t.Fatalf("worker registry exposed API domain metrics:\n%s", workerMetrics)
	}
}

func TestDuplicateRegistrationReturnsError(t *testing.T) {
	registry := prometheus.NewRegistry()
	if _, err := NewHTTPMetrics(registry); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if _, err := NewHTTPMetrics(registry); err == nil {
		t.Fatal("expected duplicate registration error")
	}
}

func TestHTTPMetricsNormalizeUnboundedValues(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewHTTPMetrics(registry)
	if err != nil {
		t.Fatalf("register metrics: %v", err)
	}

	metrics.ObserveRequest("/players/private-value", http.MethodPut, 999, time.Second)
	got := scrape(t, registry)

	want := `game_rewards_http_requests_total{method="other",route="unknown",status_code="unknown"} 1`
	if !strings.Contains(got, want) {
		t.Fatalf("metrics did not normalize values, want %q in:\n%s", want, got)
	}

	durationWant := `game_rewards_http_request_duration_seconds_count{method="other",route="unknown"} 1`
	if !strings.Contains(got, durationWant) {
		t.Fatalf("HTTP duration histogram was not observed, want %q in:\n%s", durationWant, got)
	}

	if strings.Contains(got, "/players/private-value") {
		t.Fatal("metrics exposed raw request path")
	}
}

func TestRewardMetricsRecordDistinctIdempotencyOutcomes(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewRewardMetrics(registry)
	if err != nil {
		t.Fatalf("register metrics: %v", err)
	}

	metrics.ObserveRewardClaim(rewards.CreateClaimResult{StatusCode: http.StatusCreated}, nil)
	metrics.ObserveRewardClaim(rewards.CreateClaimResult{StatusCode: http.StatusConflict, Replayed: true}, nil)
	metrics.ObserveRewardClaim(rewards.CreateClaimResult{}, rewards.ErrIdempotencyKeyReused)
	metrics.ObserveRewardClaim(rewards.CreateClaimResult{}, errors.New("database credentials must not appear"))

	got := scrape(t, registry)
	for _, want := range []string{
		`game_rewards_reward_claim_operations_total{outcome="created"} 1`,
		`game_rewards_reward_claim_operations_total{outcome="replayed_already_claimed"} 1`,
		`game_rewards_reward_claim_operations_total{outcome="idempotency_key_reused"} 1`,
		`game_rewards_reward_claim_operations_total{outcome="internal_error"} 1`,
		`game_rewards_idempotency_operations_total{outcome="new"} 1`,
		`game_rewards_idempotency_operations_total{outcome="replayed"} 1`,
		`game_rewards_idempotency_operations_total{outcome="key_reused"} 1`,
		`game_rewards_idempotency_operations_total{outcome="failed"} 1`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("want %q in metrics:\n%s", want, got)
		}
	}
	if strings.Contains(got, "database credentials") {
		t.Fatal("metrics exposed raw error")
	}
}

func TestRewardMetricsTreatUnexpectedStoredStatusAsInternalError(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewRewardMetrics(registry)
	if err != nil {
		t.Fatalf("register metrics: %v", err)
	}

	metrics.ObserveRewardClaim(rewards.CreateClaimResult{StatusCode: http.StatusOK, Replayed: true}, nil)
	got := scrape(t, registry)

	if !strings.Contains(got, `game_rewards_reward_claim_operations_total{outcome="internal_error"} 1`) {
		t.Fatalf("unexpected status was not classified as internal_error:\n%s", got)
	}
	if !strings.Contains(got, `game_rewards_idempotency_operations_total{outcome="replayed"} 1`) {
		t.Fatalf("replay path was not retained:\n%s", got)
	}
}

func TestWorkerMetricsNormalizeEventTypesAndRecordTransitions(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewWorkerMetrics(registry)
	if err != nil {
		t.Fatalf("register metrics: %v", err)
	}

	metrics.ObserveClaim(outbox.ClaimOutcomeClaimed)
	metrics.ObservePublish("RewardClaimed", outbox.PublishOutcomeSuccess, 25*time.Millisecond)
	metrics.ObservePublished("RewardClaimed")
	metrics.ObserveRetry("unexpected-user-value", "raw downstream error")
	metrics.ObserveDeadLetter("RewardClaimed", "publish_timeout")
	metrics.ObserveLeaseLoss("RewardClaimed", outbox.OperationMarkPublished)
	metrics.ObserveOperationError(outbox.OperationMarkPublished)

	got := scrape(t, registry)
	for _, want := range []string{
		`game_rewards_outbox_claim_attempts_total{outcome="claimed"} 1`,
		`game_rewards_outbox_publish_attempts_total{event_type="reward_claimed",outcome="success"} 1`,
		`game_rewards_outbox_publish_duration_seconds_count{event_type="reward_claimed",outcome="success"} 1`,
		`game_rewards_outbox_events_published_total{event_type="reward_claimed"} 1`,
		`game_rewards_outbox_retries_scheduled_total{event_type="unknown",failure_reason="unknown"} 1`,
		`game_rewards_outbox_events_dead_lettered_total{event_type="reward_claimed",failure_reason="publish_timeout"} 1`,
		`game_rewards_outbox_lease_losses_total{event_type="reward_claimed",operation="mark_published"} 1`,
		`game_rewards_outbox_operation_errors_total{operation="mark_published"} 1`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("want %q in metrics:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unexpected-user-value") || strings.Contains(got, "raw downstream error") {
		t.Fatal("worker metrics exposed unbounded values")
	}
}

func TestMetricConstructorsRejectNilRegisterer(t *testing.T) {
	constructors := map[string]func(prometheus.Registerer) error{
		"http": func(registerer prometheus.Registerer) error {
			_, err := NewHTTPMetrics(registerer)
			return err
		},
		"rewards": func(registerer prometheus.Registerer) error {
			_, err := NewRewardMetrics(registerer)
			return err
		},
		"worker": func(registerer prometheus.Registerer) error {
			_, err := NewWorkerMetrics(registerer)
			return err
		},
	}

	for name, constructor := range constructors {
		t.Run(name, func(t *testing.T) {
			if err := constructor(nil); err == nil {
				t.Fatal("expected nil registerer error")
			}
		})
	}
}

func TestHandlerRejectsNonGET(t *testing.T) {
	registry := prometheus.NewRegistry()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/metrics", nil)

	Handler(registry).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
	if got := recorder.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", got)
	}
}

func scrape(t *testing.T, registry *prometheus.Registry) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	Handler(registry).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	return recorder.Body.String()
}
