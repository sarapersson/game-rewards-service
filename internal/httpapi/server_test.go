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

	server := NewServer(cfg, testLogger())

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

	server := NewServer(cfg, testLogger())

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
