package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sarapersson/game-rewards-service/internal/config"
	"github.com/sarapersson/game-rewards-service/internal/health"
	"github.com/sarapersson/game-rewards-service/internal/rewards"
)

type stubRewardClaimService struct{}

func (stubRewardClaimService) CreateClaim(context.Context, rewards.CreateClaimCommand) (rewards.CreateClaimResult, error) {
	return rewards.CreateClaimResult{}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestRouter(readinessChecks ...health.Check) http.Handler {
	return newRouter(testLogger(), stubRewardClaimService{}, ServerObservability{}, readinessChecks...)
}

func testRewardClaimsHandler(service rewardClaimCreator, observers ...RewardClaimObserver) http.HandlerFunc {
	var observer RewardClaimObserver
	if len(observers) > 0 {
		observer = observers[0]
	}

	return rewardClaimsHandler(testLogger(), service, observer)
}

func mustNewTestServer(
	t *testing.T,
	cfg config.HTTPConfig,
	observability ServerObservability,
	readinessChecks ...health.Check,
) *http.Server {
	t.Helper()

	server, err := NewServer(cfg, testLogger(), stubRewardClaimService{}, observability, readinessChecks...)
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}

	return server
}

func assertJSONContentType(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func assertErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, wantCode, wantMessage string) {
	t.Helper()

	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	if body.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q", body.Error.Code, wantCode)
	}
	if body.Error.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", body.Error.Message, wantMessage)
	}
}
