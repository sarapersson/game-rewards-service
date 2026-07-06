package httpapi

import (
	"context"
	"net/http"
)

type healthResponse struct {
	Status string `json:"status"`
}

// ReadinessCheck is a named dependency check used by /readyz.
type ReadinessCheck struct {
	Name  string
	Check func(context.Context) error
}

type readinessResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func livezHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	writeJSON(w, http.StatusOK, healthResponse{
		Status: "ok",
	})
}

func readyzHandler(readinessChecks ...ReadinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}

		statusCode := http.StatusOK
		status := "ready"
		checks := make(map[string]string, len(readinessChecks))

		for _, readinessCheck := range readinessChecks {
			checkStatus := "ok"
			if readinessCheck.Check == nil || readinessCheck.Check(r.Context()) != nil {
				checkStatus = "error"
				statusCode = http.StatusServiceUnavailable
				status = "not_ready"
			}

			checks[readinessCheck.Name] = checkStatus
		}

		writeJSON(w, statusCode, readinessResponse{
			Status: status,
			Checks: checks,
		})
	}
}
