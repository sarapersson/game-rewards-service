// Package httpapi contains HTTP routing, middleware, and response handling.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

const (
	errorCodeInternal         = "internal_error"
	errorCodeMethodNotAllowed = "method_not_allowed"
	errorCodeNotFound         = "not_found"
)

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, errorResponse{
		Error: errorBody{
			Code:    code,
			Message: message,
		},
	})
}

func writeMethodNotAllowed(w http.ResponseWriter, allowedMethods ...string) {
	w.Header().Set("Allow", strings.Join(allowedMethods, ", "))
	writeError(w, http.StatusMethodNotAllowed, errorCodeMethodNotAllowed, "Method not allowed")
}
