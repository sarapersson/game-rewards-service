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

	server := mustNewTestServer(t, cfg, ServerObservability{})

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

func TestNewServerRequiresCoreDependencies(t *testing.T) {
	cfg := config.Config{HTTP: config.HTTPConfig{Addr: ":0"}}

	tests := []struct {
		name         string
		logger       bool
		rewardClaims rewardClaimCreator
	}{
		{name: "logger", rewardClaims: stubRewardClaimService{}},
		{name: "reward claim service", logger: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logger = testLogger()
			if !tt.logger {
				logger = nil
			}

			server, err := NewServer(cfg, logger, tt.rewardClaims, ServerObservability{})
			if err == nil {
				t.Fatal("NewServer returned nil error")
			}
			if server != nil {
				t.Fatalf("NewServer server = %#v, want nil", server)
			}
		})
	}
}

func TestNewServerWiresRoutesMiddlewareAndMetrics(t *testing.T) {
	observer := &recordingRequestObserver{}
	server := mustNewTestServer(
		t,
		config.Config{HTTP: config.HTTPConfig{Addr: ":0"}},
		ServerObservability{
			MetricsHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("metric 1\n"))
			}),
			RequestObserver: observer,
		},
	)

	liveRecorder := httptest.NewRecorder()
	liveRequest := httptest.NewRequest(http.MethodGet, routeLivez, nil)
	server.Handler.ServeHTTP(liveRecorder, liveRequest)
	if liveRecorder.Code != http.StatusOK {
		t.Fatalf("live status = %d, want 200", liveRecorder.Code)
	}
	if got := liveRecorder.Header().Get(headerRequestID); got == "" {
		t.Fatal("expected request ID header from middleware")
	}

	metricsRecorder := httptest.NewRecorder()
	metricsRequest := httptest.NewRequest(http.MethodGet, routeMetrics, nil)
	server.Handler.ServeHTTP(metricsRecorder, metricsRequest)
	if metricsRecorder.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", metricsRecorder.Code)
	}
	if metricsRecorder.Body.String() != "metric 1\n" {
		t.Fatalf("metrics body = %q, want metric output", metricsRecorder.Body.String())
	}
	if observer.route != routeMetrics || observer.method != http.MethodGet || observer.status != http.StatusOK {
		t.Fatalf("unexpected metrics observation: %#v", observer)
	}
}
