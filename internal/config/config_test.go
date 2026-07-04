package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadWithLookupUsesDefaults(t *testing.T) {
	cfg, err := loadWithLookup(mapLookup(nil))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.AppEnv != "local" {
		t.Fatalf("expected default app env local, got %q", cfg.AppEnv)
	}

	if cfg.ServiceName != "game-rewards-service" {
		t.Fatalf("expected default service name, got %q", cfg.ServiceName)
	}

	if cfg.HTTP.Addr != ":8080" {
		t.Fatalf("expected default HTTP addr :8080, got %q", cfg.HTTP.Addr)
	}

	if cfg.HTTP.ReadTimeout != 5*time.Second {
		t.Fatalf("expected read timeout 5s, got %s", cfg.HTTP.ReadTimeout)
	}

	if cfg.HTTP.ReadHeaderTimeout != 2*time.Second {
		t.Fatalf("expected read header timeout 2s, got %s", cfg.HTTP.ReadHeaderTimeout)
	}

	if cfg.HTTP.WriteTimeout != 10*time.Second {
		t.Fatalf("expected write timeout 10s, got %s", cfg.HTTP.WriteTimeout)
	}

	if cfg.HTTP.IdleTimeout != 60*time.Second {
		t.Fatalf("expected idle timeout 60s, got %s", cfg.HTTP.IdleTimeout)
	}

	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("expected shutdown timeout 10s, got %s", cfg.ShutdownTimeout)
	}

	if cfg.Log.Level != slog.LevelInfo {
		t.Fatalf("expected info log level, got %s", cfg.Log.Level)
	}
}

func TestLoadWithLookupUsesEnvironmentOverrides(t *testing.T) {
	cfg, err := loadWithLookup(mapLookup(map[string]string{
		"APP_ENV":                  "test",
		"SERVICE_NAME":             "custom-service",
		"HTTP_ADDR":                ":9090",
		"HTTP_READ_TIMEOUT":        "1s",
		"HTTP_READ_HEADER_TIMEOUT": "500ms",
		"HTTP_WRITE_TIMEOUT":       "2s",
		"HTTP_IDLE_TIMEOUT":        "30s",
		"SHUTDOWN_TIMEOUT":         "3s",
		"LOG_LEVEL":                "debug",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.AppEnv != "test" {
		t.Fatalf("expected app env test, got %q", cfg.AppEnv)
	}

	if cfg.ServiceName != "custom-service" {
		t.Fatalf("expected custom service name, got %q", cfg.ServiceName)
	}

	if cfg.HTTP.Addr != ":9090" {
		t.Fatalf("expected HTTP addr :9090, got %q", cfg.HTTP.Addr)
	}

	if cfg.HTTP.ReadTimeout != time.Second {
		t.Fatalf("expected read timeout 1s, got %s", cfg.HTTP.ReadTimeout)
	}

	if cfg.HTTP.ReadHeaderTimeout != 500*time.Millisecond {
		t.Fatalf("expected read header timeout 500ms, got %s", cfg.HTTP.ReadHeaderTimeout)
	}

	if cfg.HTTP.WriteTimeout != 2*time.Second {
		t.Fatalf("expected write timeout 2s, got %s", cfg.HTTP.WriteTimeout)
	}

	if cfg.HTTP.IdleTimeout != 30*time.Second {
		t.Fatalf("expected idle timeout 30s, got %s", cfg.HTTP.IdleTimeout)
	}

	if cfg.ShutdownTimeout != 3*time.Second {
		t.Fatalf("expected shutdown timeout 3s, got %s", cfg.ShutdownTimeout)
	}

	if cfg.Log.Level != slog.LevelDebug {
		t.Fatalf("expected debug log level, got %s", cfg.Log.Level)
	}
}

func TestLoadWithLookupFallsBackForBlankValues(t *testing.T) {
	cfg, err := loadWithLookup(mapLookup(map[string]string{
		"APP_ENV":      "   ",
		"SERVICE_NAME": "",
		"HTTP_ADDR":    "\t",
		"LOG_LEVEL":    "",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.AppEnv != "local" {
		t.Fatalf("expected blank APP_ENV to fall back to default, got %q", cfg.AppEnv)
	}

	if cfg.ServiceName != "game-rewards-service" {
		t.Fatalf("expected blank SERVICE_NAME to fall back to default, got %q", cfg.ServiceName)
	}

	if cfg.HTTP.Addr != ":8080" {
		t.Fatalf("expected blank HTTP_ADDR to fall back to default, got %q", cfg.HTTP.Addr)
	}

	if cfg.Log.Level != slog.LevelInfo {
		t.Fatalf("expected blank LOG_LEVEL to fall back to default, got %s", cfg.Log.Level)
	}
}

func TestLoadWithLookupRejectsInvalidDuration(t *testing.T) {
	_, err := loadWithLookup(mapLookup(map[string]string{
		"HTTP_READ_TIMEOUT": "not-a-duration",
	}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "invalid HTTP_READ_TIMEOUT") {
		t.Fatalf("expected HTTP_READ_TIMEOUT error, got %v", err)
	}
}

func TestLoadWithLookupRejectsNonPositiveDuration(t *testing.T) {
	_, err := loadWithLookup(mapLookup(map[string]string{
		"HTTP_WRITE_TIMEOUT": "0s",
	}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "HTTP_WRITE_TIMEOUT") {
		t.Fatalf("expected HTTP_WRITE_TIMEOUT error, got %v", err)
	}
}

func TestLoadWithLookupRejectsInvalidLogLevel(t *testing.T) {
	_, err := loadWithLookup(mapLookup(map[string]string{
		"LOG_LEVEL": "verbose",
	}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "invalid LOG_LEVEL") {
		t.Fatalf("expected LOG_LEVEL error, got %v", err)
	}
}

func mapLookup(values map[string]string) lookupFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
