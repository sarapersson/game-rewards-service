package httpapi

import (
	"net/http"

	"github.com/sarapersson/game-rewards-service/internal/health"
)

type healthResponse = health.Response

// ReadinessCheck is a named dependency check used by /readyz.
type ReadinessCheck = health.Check

type readinessResponse = health.ReadinessResponse

func livezHandler(w http.ResponseWriter, r *http.Request) {
	health.LiveHandler().ServeHTTP(w, r)
}

func readyzHandler(readinessChecks ...ReadinessCheck) http.HandlerFunc {
	handler := health.ReadyHandler(readinessChecks...)
	return func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}
}
