package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLivezReturnsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeLivez, nil)

	newRouter(testLogger(), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	assertJSONContentType(t, rec)

	var body healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.Status != "ok" {
		t.Fatalf("expected status ok, got %q", body.Status)
	}
}

func TestReadyzReturnsReadyWithoutChecks(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeReadyz, nil)

	newRouter(testLogger(), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	assertJSONContentType(t, rec)

	var body readinessResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.Status != "ready" {
		t.Fatalf("expected status ready, got %q", body.Status)
	}

	if body.Checks == nil {
		t.Fatal("expected checks to be present")
	}

	if len(body.Checks) != 0 {
		t.Fatalf("expected no readiness checks at this stage, got %d", len(body.Checks))
	}
}

func TestReadyzReturnsReadyWhenChecksPass(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeReadyz, nil)

	newRouter(testLogger(), nil, ReadinessCheck{
		Name: "postgres",
		Check: func(context.Context) error {
			return nil
		},
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	assertJSONContentType(t, rec)

	var body readinessResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.Status != "ready" {
		t.Fatalf("expected status ready, got %q", body.Status)
	}

	if body.Checks["postgres"] != "ok" {
		t.Fatalf("expected postgres check ok, got %q", body.Checks["postgres"])
	}
}

func TestReadyzReturnsServiceUnavailableWhenCheckFails(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeReadyz, nil)

	newRouter(testLogger(), nil, ReadinessCheck{
		Name: "postgres",
		Check: func(context.Context) error {
			return errors.New("postgres down")
		},
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", rec.Code)
	}

	assertJSONContentType(t, rec)

	var body readinessResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.Status != "not_ready" {
		t.Fatalf("expected status not_ready, got %q", body.Status)
	}

	if body.Checks["postgres"] != "error" {
		t.Fatalf("expected postgres check error, got %q", body.Checks["postgres"])
	}
}

func TestUnknownRouteReturnsJSONNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)

	newRouter(testLogger(), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}

	assertJSONContentType(t, rec)
	assertErrorResponse(t, rec, errorCodeNotFound, "Not found")
}

func TestLivezRejectsNonGETMethods(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, routeLivez, nil)

	newRouter(testLogger(), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rec.Code)
	}

	assertAllowHeader(t, rec, http.MethodGet)
	assertJSONContentType(t, rec)
	assertErrorResponse(t, rec, errorCodeMethodNotAllowed, "Method not allowed")
}

func TestReadyzRejectsNonGETMethods(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, routeReadyz, nil)

	newRouter(testLogger(), nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rec.Code)
	}

	assertAllowHeader(t, rec, http.MethodGet)
	assertJSONContentType(t, rec)
	assertErrorResponse(t, rec, errorCodeMethodNotAllowed, "Method not allowed")
}

func assertAllowHeader(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()

	if got := rec.Header().Get("Allow"); got != want {
		t.Fatalf("expected Allow header %q, got %q", want, got)
	}
}

func assertJSONContentType(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	got := rec.Header().Get("Content-Type")
	if got != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", got)
	}
}

func assertErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, wantCode string, wantMessage string) {
	t.Helper()

	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	if body.Error.Code != wantCode {
		t.Fatalf("expected error code %q, got %q", wantCode, body.Error.Code)
	}

	if body.Error.Message != wantMessage {
		t.Fatalf("expected error message %q, got %q", wantMessage, body.Error.Message)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
