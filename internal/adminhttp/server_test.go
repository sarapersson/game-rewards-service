package adminhttp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
		config.Config{
			HTTP: config.HTTPConfig{
				ReadTimeout:       time.Second,
				ReadHeaderTimeout: time.Second,
				WriteTimeout:      time.Second,
				IdleTimeout:       time.Second,
			},
			Worker: config.WorkerConfig{AdminAddr: ":9191"},
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		observer,
		health.Check{Name: "worker", Check: func(context.Context) error { return nil }},
	)

	if server.Addr != ":9191" {
		t.Fatalf("addr = %q, want :9191", server.Addr)
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

func TestUnknownRouteIsBounded(t *testing.T) {
	observer := &recordingObserver{}
	server := NewServer(
		config.Config{Worker: config.WorkerConfig{AdminAddr: ":0"}},
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
		config.Config{Worker: config.WorkerConfig{AdminAddr: ":0"}},
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
