package httpapi

import (
	"log/slog"
	"net/http"
)

const (
	routeLivez   = "/livez"
	routeReadyz  = "/readyz"
	routeMetrics = "/metrics"
)

func newRouter(logger *slog.Logger, rewardClaims rewardClaimCreator, readinessChecks ...ReadinessCheck) http.Handler {
	return newRouterWithObservability(logger, rewardClaims, ServerObservability{}, readinessChecks...)
}

func newRouterWithObservability(
	logger *slog.Logger,
	rewardClaims rewardClaimCreator,
	observability ServerObservability,
	readinessChecks ...ReadinessCheck,
) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc(routeLivez, livezHandler)
	mux.HandleFunc(routeReadyz, readyzHandler(readinessChecks...))
	if observability.MetricsHandler != nil {
		mux.Handle(routeMetrics, observability.MetricsHandler)
	}
	mux.HandleFunc(
		routeRewardClaims,
		rewardClaimsHandlerWithLogger(logger, rewardClaims, observability.RewardClaimObserver),
	)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "Not found")
	})

	return withMiddleware(mux, logger, observability.RequestObserver)
}
