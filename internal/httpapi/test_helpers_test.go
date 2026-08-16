package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/sarapersson/game-rewards-service/internal/config"
	"github.com/sarapersson/game-rewards-service/internal/rewards"
)

type stubRewardClaimService struct{}

func (stubRewardClaimService) CreateClaim(context.Context, rewards.CreateClaimCommand) (rewards.CreateClaimResult, error) {
	return rewards.CreateClaimResult{}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestRouter(readinessChecks ...ReadinessCheck) http.Handler {
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
	readinessChecks ...ReadinessCheck,
) *http.Server {
	t.Helper()

	server, err := NewServer(cfg, testLogger(), stubRewardClaimService{}, observability, readinessChecks...)
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}

	return server
}
