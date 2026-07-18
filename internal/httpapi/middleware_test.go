package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMiddlewareSetsGeneratedRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeLivez, nil)

	newRouter(testLogger(), nil).ServeHTTP(rec, req)

	got := rec.Header().Get(headerRequestID)
	if got == "" {
		t.Fatal("expected generated request ID header")
	}

	if len(got) > maxRequestIDLen {
		t.Fatalf("expected request ID length <= %d, got %d", maxRequestIDLen, len(got))
	}
}

func TestMiddlewareReusesValidRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeLivez, nil)
	req.Header.Set(headerRequestID, "test-request-id")

	newRouter(testLogger(), nil).ServeHTTP(rec, req)

	got := rec.Header().Get(headerRequestID)
	if got != "test-request-id" {
		t.Fatalf("expected request ID to be reused, got %q", got)
	}
}

func TestMiddlewareRejectsTooLongRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeLivez, nil)

	tooLong := strings.Repeat("a", maxRequestIDLen+1)
	req.Header.Set(headerRequestID, tooLong)

	newRouter(testLogger(), nil).ServeHTTP(rec, req)

	got := rec.Header().Get(headerRequestID)
	if got == "" {
		t.Fatal("expected generated request ID header")
	}

	if got == tooLong {
		t.Fatal("expected too-long request ID to be replaced")
	}
}

func TestMiddlewareSetsSecureHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeLivez, nil)

	newRouter(testLogger(), nil).ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options nosniff, got %q", got)
	}

	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("expected Referrer-Policy no-referrer, got %q", got)
	}
}

func TestMiddlewareStoresRequestIDInContext(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(headerRequestID, "context-request-id")

	handler := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Seen-Request-ID", requestIDFromRequest(r))
		w.WriteHeader(http.StatusNoContent)
	}), testLogger())

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Seen-Request-ID"); got != "context-request-id" {
		t.Fatalf("expected request ID in context, got %q", got)
	}
}

func TestRecovererReturnsJSONInternalError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)

	handler := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		panic("boom")
	}), testLogger())

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}

	assertJSONContentType(t, rec)
	assertErrorResponse(t, rec, errorCodeInternal, "Internal server error")
}

func TestMiddlewareRejectsInvalidRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeLivez, nil)
	req.Header.Set(headerRequestID, "bad request id")

	newRouter(testLogger(), nil).ServeHTTP(rec, req)

	got := rec.Header().Get(headerRequestID)
	if got == "" {
		t.Fatal("expected generated request ID header")
	}

	if got == "bad request id" {
		t.Fatal("expected invalid request ID to be replaced")
	}
}

func TestRequestLoggerDoesNotLogRawRequestPath(t *testing.T) {
	const rawPath = "/tokens/do-not-log-this-value"

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	handler := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}), logger)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, rawPath, nil)

	handler.ServeHTTP(rec, req)

	logOutput := logs.String()

	if strings.Contains(logOutput, rawPath) {
		t.Fatal("request log exposed the raw request path")
	}

	if strings.Contains(logOutput, `"path"`) {
		t.Fatal("request log contained a path field")
	}

	if !strings.Contains(logOutput, `"route":"unknown"`) {
		t.Fatalf("request log = %q, want unknown route", logOutput)
	}
}

type recordingRequestObserver struct {
	route    string
	method   string
	status   int
	duration time.Duration
}

func (o *recordingRequestObserver) ObserveRequest(route, method string, status int, duration time.Duration) {
	o.route = route
	o.method = method
	o.status = status
	o.duration = duration
}

func TestMiddlewareObservesBoundedRouteAndStatus(t *testing.T) {
	observer := &recordingRequestObserver{}
	handler := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}), testLogger(), observer)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/private/raw/path", nil)
	handler.ServeHTTP(recorder, request)

	if observer.route != "unknown" {
		t.Fatalf("route = %q, want unknown", observer.route)
	}
	if observer.method != http.MethodGet || observer.status != http.StatusTeapot {
		t.Fatalf("unexpected observation: %#v", observer)
	}
	if observer.duration < 0 {
		t.Fatalf("duration = %s", observer.duration)
	}
}

func TestMiddlewareObservesRecoveredPanicAsInternalError(t *testing.T) {
	observer := &recordingRequestObserver{}
	handler := withMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), testLogger(), observer)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if observer.route != "unknown" || observer.status != http.StatusInternalServerError {
		t.Fatalf("unexpected observation: %#v", observer)
	}
}
