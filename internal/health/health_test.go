package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sarapersson/game-rewards-service/internal/health"
)

type testHealthResponse struct {
	Status string `json:"status"`
}

type testReadinessResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

type testErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestLiveHandlerReturnsOK(t *testing.T) {
	recorder := httptest.NewRecorder()
	health.LiveHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/livez", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	assertJSONContentType(t, recorder)

	var body testHealthResponse
	decodeJSON(t, recorder, &body)
	if body.Status != "ok" {
		t.Fatalf("status = %q, want ok", body.Status)
	}
}

func TestReadyHandlerReturnsReadyWithoutChecks(t *testing.T) {
	recorder := httptest.NewRecorder()
	health.ReadyHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	assertJSONContentType(t, recorder)

	var body testReadinessResponse
	decodeJSON(t, recorder, &body)
	if body.Status != "ready" {
		t.Fatalf("status = %q, want ready", body.Status)
	}
	if body.Checks == nil {
		t.Fatal("checks = nil, want empty object")
	}
	if len(body.Checks) != 0 {
		t.Fatalf("checks = %#v, want empty", body.Checks)
	}
}

func TestReadyHandlerReturnsReadyWhenChecksPass(t *testing.T) {
	recorder := httptest.NewRecorder()
	health.ReadyHandler(health.Check{
		Name: "postgres",
		Check: func(context.Context) error {
			return nil
		},
	}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var body testReadinessResponse
	decodeJSON(t, recorder, &body)
	if body.Status != "ready" || body.Checks["postgres"] != "ok" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
}

func TestReadyHandlerReportsAllChecksWithoutRawErrors(t *testing.T) {
	const secret = "postgres://user:secret@database"
	recorder := httptest.NewRecorder()
	health.ReadyHandler(
		health.Check{Name: "postgres", Check: func(context.Context) error { return errors.New(secret) }},
		health.Check{Name: "publisher", Check: func(context.Context) error { return nil }},
	).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	assertJSONContentType(t, recorder)
	if strings.Contains(recorder.Body.String(), secret) {
		t.Fatal("readiness response exposed raw error")
	}

	var body testReadinessResponse
	decodeJSON(t, recorder, &body)
	if body.Status != "not_ready" || body.Checks["postgres"] != "error" || body.Checks["publisher"] != "ok" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
}

func TestReadyHandlerFailsClosedForNilCheck(t *testing.T) {
	recorder := httptest.NewRecorder()
	health.ReadyHandler(health.Check{Name: "postgres"}).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/readyz", nil),
	)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}

	var body testReadinessResponse
	decodeJSON(t, recorder, &body)
	if body.Status != "not_ready" || body.Checks["postgres"] != "error" {
		t.Fatalf("unexpected readiness response: %#v", body)
	}
}

func TestHandlersRejectNonGET(t *testing.T) {
	for name, handler := range map[string]http.Handler{
		"live":  health.LiveHandler(),
		"ready": health.ReadyHandler(),
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
			assertJSONContentType(t, recorder)

			var body testErrorResponse
			decodeJSON(t, recorder, &body)
			if body.Error.Code != "method_not_allowed" || body.Error.Message != "Method not allowed" {
				t.Fatalf("unexpected error response: %#v", body)
			}
		})
	}
}

func assertJSONContentType(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func decodeJSON(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(recorder.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
