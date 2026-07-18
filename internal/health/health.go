// Package health provides process liveness and dependency readiness handlers.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

const (
	StatusOK       = "ok"
	StatusReady    = "ready"
	StatusNotReady = "not_ready"
)

// Response is returned by the liveness endpoint.
type Response struct {
	Status string `json:"status"`
}

// Check is a named dependency or component readiness check.
type Check struct {
	Name  string
	Check func(context.Context) error
}

// ReadinessResponse is returned by the readiness endpoint.
type ReadinessResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// LiveHandler reports process liveness only.
func LiveHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}

		writeJSON(w, http.StatusOK, Response{Status: StatusOK})
	})
}

// ReadyHandler reports readiness for all configured checks without exposing raw errors.
func ReadyHandler(checks ...Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}

		statusCode := http.StatusOK
		status := StatusReady
		results := make(map[string]string, len(checks))

		for _, check := range checks {
			checkStatus := StatusOK
			if check.Check == nil || check.Check(r.Context()) != nil {
				checkStatus = "error"
				statusCode = http.StatusServiceUnavailable
				status = StatusNotReady
			}

			results[check.Name] = checkStatus
		}

		writeJSON(w, statusCode, ReadinessResponse{
			Status: status,
			Checks: results,
		})
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeMethodNotAllowed(w http.ResponseWriter, allowedMethods ...string) {
	w.Header().Set("Allow", strings.Join(allowedMethods, ", "))
	writeJSON(w, http.StatusMethodNotAllowed, errorResponse{
		Error: errorBody{
			Code:    "method_not_allowed",
			Message: "Method not allowed",
		},
	})
}
