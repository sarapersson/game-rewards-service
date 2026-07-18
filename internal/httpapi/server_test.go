package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sarapersson/game-rewards-service/internal/config"
)

func TestNewServerUsesConfiguredSettings(t *testing.T) {
	cfg := config.Config{
		HTTP: config.HTTPConfig{
			Addr:              ":9090",
			ReadTimeout:       1 * time.Second,
			ReadHeaderTimeout: 2 * time.Second,
			WriteTimeout:      3 * time.Second,
			IdleTimeout:       4 * time.Second,
		},
	}

	server := NewServer(cfg, testLogger(), nil)

	if server.Addr != ":9090" {
		t.Fatalf("expected addr :9090, got %q", server.Addr)
	}

	if server.ReadTimeout != 1*time.Second {
		t.Fatalf("expected read timeout 1s, got %s", server.ReadTimeout)
	}

	if server.ReadHeaderTimeout != 2*time.Second {
		t.Fatalf("expected read header timeout 2s, got %s", server.ReadHeaderTimeout)
	}

	if server.WriteTimeout != 3*time.Second {
		t.Fatalf("expected write timeout 3s, got %s", server.WriteTimeout)
	}

	if server.IdleTimeout != 4*time.Second {
		t.Fatalf("expected idle timeout 4s, got %s", server.IdleTimeout)
	}

	if server.Handler == nil {
		t.Fatal("expected handler to be set")
	}
}

func TestNewServerWiresRoutesAndMiddleware(t *testing.T) {
	cfg := config.Config{
		HTTP: config.HTTPConfig{
			Addr: ":0",
		},
	}

	server := NewServer(cfg, testLogger(), nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, routeLivez, nil)

	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if got := rec.Header().Get(headerRequestID); got == "" {
		t.Fatal("expected request ID header from middleware")
	}
}

func TestNewServerWithObservabilityWiresMetricsRoute(t *testing.T) {
	observer := &recordingRequestObserver{}
	server := NewServerWithObservability(
		config.Config{HTTP: config.HTTPConfig{Addr: ":0"}},
		testLogger(),
		nil,
		ServerObservability{
			MetricsHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("metric 1\n"))
			}),
			RequestObserver: observer,
		},
	)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, routeMetrics, nil)
	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if recorder.Body.String() != "metric 1\n" {
		t.Fatalf("body = %q, want metric output", recorder.Body.String())
	}
	if observer.route != routeMetrics || observer.method != http.MethodGet || observer.status != http.StatusOK {
		t.Fatalf("unexpected metrics observation: %#v", observer)
	}
}
