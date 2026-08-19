package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/sarapersson/game-rewards-service/internal/health"
)

const (
	routeLivez   = "/livez"
	routeReadyz  = "/readyz"
	routeMetrics = "/metrics"
)

func newRouter(
	logger *slog.Logger,
	rewardClaims rewardClaimCreator,
	observability ServerObservability,
	readinessChecks ...health.Check,
) http.Handler {
	mux := http.NewServeMux()

	mux.Handle(routeLivez, health.LiveHandler())
	mux.Handle(routeReadyz, health.ReadyHandler(readinessChecks...))
	if observability.MetricsHandler != nil {
		mux.Handle(routeMetrics, observability.MetricsHandler)
	}
	mux.HandleFunc(
		routeRewardClaims,
		rewardClaimsHandler(logger, rewardClaims, observability.RewardClaimObserver),
	)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "Not found")
	})

	return withMiddleware(mux, logger, observability.RequestObserver)
}
