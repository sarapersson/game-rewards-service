package httpapi

import (
	"log/slog"
	"net/http"
)

const (
	routeLivez  = "/livez"
	routeReadyz = "/readyz"
)

func newRouter(logger *slog.Logger, rewardClaims rewardClaimCreator, readinessChecks ...ReadinessCheck) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc(routeLivez, livezHandler)
	mux.HandleFunc(routeReadyz, readyzHandler(readinessChecks...))
	mux.HandleFunc(routeRewardClaims, rewardClaimsHandler(rewardClaims))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, errorCodeNotFound, "Not found")
	})

	return withMiddleware(mux, logger)
}
