package adminhttp

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sarapersson/game-rewards-service/internal/config"
	"github.com/sarapersson/game-rewards-service/internal/health"
)

type recordingObserver struct {
	route  string
	method string
	status int
}

func (o *recordingObserver) ObserveRequest(route, method string, status int, _ time.Duration) {
	o.route, o.method, o.status = route, method, status
}

func TestNewServerWiresWorkerAdminEndpoints(t *testing.T) {
	observer := &recordingObserver{}
	server := NewServer(
		config.HTTPConfig{
			Addr:              ":9191",
			ReadTimeout:       time.Second,
			ReadHeaderTimeout: time.Second,
			WriteTimeout:      time.Second,
			IdleTimeout:       time.Second,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		observer,
		health.Check{Name: "worker", Check: func(context.Context) error { return nil }},
	)

	if server.Addr != ":9191" {
		t.Fatalf("addr = %q, want :9191", server.Addr)
	}
	const wantMaxHeaderBytes = 32 * 1024
	if server.MaxHeaderBytes != wantMaxHeaderBytes {
		t.Fatalf("max header bytes = %d, want %d", server.MaxHeaderBytes, wantMaxHeaderBytes)
	}
	const wantMaxHeaderValueCount = 500
	if server.MaxHeaderValueCount != wantMaxHeaderValueCount {
		t.Fatalf("max header value count = %d, want %d", server.MaxHeaderValueCount, wantMaxHeaderValueCount)
	}

	for _, path := range []string{routeLivez, routeReadyz, routeMetrics} {
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, recorder.Code)
		}
		if observer.route != path || observer.method != http.MethodGet || observer.status != http.StatusOK {
			t.Fatalf("unexpected observation for %s: %#v", path, observer)
		}
		if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s X-Content-Type-Options = %q", path, got)
		}
	}
}

func TestNewServerRoutesServerErrorsThroughLogger(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(
		&logs,
		&slog.HandlerOptions{Level: slog.LevelError},
	)).With(slog.String("component", "adminhttp-test"))

	server := NewServer(
		config.HTTPConfig{Addr: ":0"},
		logger,
		nil,
		nil,
	)
	if server.ErrorLog == nil {
		t.Fatal("expected error logger to be set")
	}

	server.ErrorLog.Print("transport failure")

	output := logs.String()
	if !strings.Contains(output, "transport failure") {
		t.Fatalf("error log output = %q, want transport failure message", output)
	}
	if !strings.Contains(output, "level=ERROR") {
		t.Fatalf("error log output = %q, want ERROR level", output)
	}
	if !strings.Contains(output, "component=adminhttp-test") {
		t.Fatalf("error log output = %q, want injected logger attributes", output)
	}
}

func TestUnknownRouteIsBounded(t *testing.T) {
	observer := &recordingObserver{}
	server := NewServer(
		config.HTTPConfig{Addr: ":0"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		observer,
	)

	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/private/raw/path", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if observer.route != "unknown" {
		t.Fatalf("route = %q, want unknown", observer.route)
	}
}

func TestServerObservesRecoveredPanicAsInternalError(t *testing.T) {
	observer := &recordingObserver{}
	server := NewServer(
		config.HTTPConfig{Addr: ":0"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }),
		observer,
	)

	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, routeMetrics, nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if observer.route != routeMetrics || observer.status != http.StatusInternalServerError {
		t.Fatalf("unexpected observation: %#v", observer)
	}
}

func TestServerClearsStaleContentLengthForPanic(t *testing.T) {
	server := NewServer(
		config.HTTPConfig{Addr: ":0"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "1")
			panic("boom")
		}),
		nil,
	)

	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, routeMetrics, nil))

	if got := recorder.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length = %q, want cleared", got)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("response body = %q, want internal error envelope", recorder.Body.String())
	}
}

func TestServerPreservesErrAbortHandler(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	observer := &recordingObserver{}
	server := NewServer(
		config.HTTPConfig{Addr: ":0"},
		logger,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }),
		observer,
	)

	defer func() {
		recovered := recover()
		if recovered != http.ErrAbortHandler {
			t.Fatalf("recovered panic = %v, want http.ErrAbortHandler", recovered)
		}
		if observer.status != 0 {
			t.Fatalf("observed status = %d, want 0", observer.status)
		}
		if strings.Contains(logs.String(), "worker admin panic recovered") {
			t.Fatalf("logs = %q, want no recovered panic log", logs.String())
		}
	}()

	server.Handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, routeMetrics, nil),
	)
	t.Fatal("expected http.ErrAbortHandler panic")
}

func TestServerAbortsAfterResponseWrite(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	observer := &recordingObserver{}
	recorder := httptest.NewRecorder()
	server := NewServer(
		config.HTTPConfig{Addr: ":0"},
		logger,
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("partial"))
			panic("boom")
		}),
		observer,
	)

	defer func() {
		recovered := recover()
		if recovered != http.ErrAbortHandler {
			t.Fatalf("recovered panic = %v, want http.ErrAbortHandler", recovered)
		}
		if recorder.Code != http.StatusAccepted || recorder.Body.String() != "partial" {
			t.Fatalf("response = (%d, %q), want (202, partial)", recorder.Code, recorder.Body.String())
		}
		if observer.status != 0 {
			t.Fatalf("observed status = %d, want 0", observer.status)
		}
		if !strings.Contains(logs.String(), "worker admin panic recovered") {
			t.Fatalf("logs = %q, want recovered panic log", logs.String())
		}
	}()

	server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, routeMetrics, nil))
	t.Fatal("expected http.ErrAbortHandler panic")
}
