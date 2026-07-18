package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveHandler(t *testing.T) {
	recorder := httptest.NewRecorder()
	LiveHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/livez", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body Response
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != StatusOK {
		t.Fatalf("status = %q, want %q", body.Status, StatusOK)
	}
}

func TestReadyHandlerReportsAllChecksWithoutRawErrors(t *testing.T) {
	const secret = "postgres://user:secret@database"
	recorder := httptest.NewRecorder()
	ReadyHandler(
		Check{Name: "postgres", Check: func(context.Context) error { return errors.New(secret) }},
		Check{Name: "worker", Check: func(context.Context) error { return nil }},
	).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), secret) {
		t.Fatal("readiness response exposed raw error")
	}

	var body ReadinessResponse
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != StatusNotReady || body.Checks["postgres"] != "error" || body.Checks["worker"] != StatusOK {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
}

func TestHandlersRejectNonGET(t *testing.T) {
	for name, handler := range map[string]http.Handler{
		"live":  LiveHandler(),
		"ready": ReadyHandler(),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", nil))
			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", recorder.Code)
			}
			if got := recorder.Header().Get("Allow"); got != http.MethodGet {
				t.Fatalf("Allow = %q, want GET", got)
			}
		})
	}
}
