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

	if cfg.Database.URL != "postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable" {
		t.Fatalf("expected default database URL, got %q", cfg.Database.URL)
	}

	if cfg.Database.PingTimeout != 2*time.Second {
		t.Fatalf("expected database ping timeout 2s, got %s", cfg.Database.PingTimeout)
	}

	if cfg.Database.QueryTimeout != 2*time.Second {
		t.Fatalf("expected database query timeout 2s, got %s", cfg.Database.QueryTimeout)
	}

	if cfg.Worker.AdminAddr != ":8081" {
		t.Fatalf("expected worker admin addr :8081, got %q", cfg.Worker.AdminAddr)
	}

	if cfg.Worker.PollInterval != time.Second {
		t.Fatalf("expected worker poll interval 1s, got %s", cfg.Worker.PollInterval)
	}

	if cfg.Worker.OutboxLockTTL != 30*time.Second {
		t.Fatalf("expected outbox lock TTL 30s, got %s", cfg.Worker.OutboxLockTTL)
	}

	if cfg.Worker.PublishTimeout != 5*time.Second {
		t.Fatalf("expected outbox publish timeout 5s, got %s", cfg.Worker.PublishTimeout)
	}

	if cfg.Worker.MaxAttempts != 5 {
		t.Fatalf("expected outbox max attempts 5, got %d", cfg.Worker.MaxAttempts)
	}

	if cfg.Worker.BaseBackoff != time.Second {
		t.Fatalf("expected outbox base backoff 1s, got %s", cfg.Worker.BaseBackoff)
	}

	if cfg.Worker.MaxBackoff != time.Minute {
		t.Fatalf("expected outbox max backoff 1m, got %s", cfg.Worker.MaxBackoff)
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
		"DATABASE_URL":             "postgres://custom:secret@localhost:5433/custom?sslmode=disable",
		"DB_PING_TIMEOUT":          "750ms",
		"DB_QUERY_TIMEOUT":         "900ms",
		"WORKER_ADMIN_ADDR":        ":9191",
		"WORKER_POLL_INTERVAL":     "250ms",
		"OUTBOX_LOCK_TTL":          "45s",
		"OUTBOX_PUBLISH_TIMEOUT":   "10s",
		"OUTBOX_MAX_ATTEMPTS":      "7",
		"OUTBOX_BASE_BACKOFF":      "2s",
		"OUTBOX_MAX_BACKOFF":       "2m",
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

	if cfg.Database.URL != "postgres://custom:secret@localhost:5433/custom?sslmode=disable" {
		t.Fatalf("expected custom database URL, got %q", cfg.Database.URL)
	}

	if cfg.Database.PingTimeout != 750*time.Millisecond {
		t.Fatalf("expected database ping timeout 750ms, got %s", cfg.Database.PingTimeout)
	}

	if cfg.Database.QueryTimeout != 900*time.Millisecond {
		t.Fatalf("expected database query timeout 900ms, got %s", cfg.Database.QueryTimeout)
	}

	if cfg.Worker.AdminAddr != ":9191" {
		t.Fatalf("expected worker admin addr :9191, got %q", cfg.Worker.AdminAddr)
	}

	if cfg.Worker.PollInterval != 250*time.Millisecond {
		t.Fatalf("expected worker poll interval 250ms, got %s", cfg.Worker.PollInterval)
	}

	if cfg.Worker.OutboxLockTTL != 45*time.Second {
		t.Fatalf("expected outbox lock TTL 45s, got %s", cfg.Worker.OutboxLockTTL)
	}

	if cfg.Worker.PublishTimeout != 10*time.Second {
		t.Fatalf("expected outbox publish timeout 10s, got %s", cfg.Worker.PublishTimeout)
	}

	if cfg.Worker.MaxAttempts != 7 {
		t.Fatalf("expected outbox max attempts 7, got %d", cfg.Worker.MaxAttempts)
	}

	if cfg.Worker.BaseBackoff != 2*time.Second {
		t.Fatalf("expected outbox base backoff 2s, got %s", cfg.Worker.BaseBackoff)
	}

	if cfg.Worker.MaxBackoff != 2*time.Minute {
		t.Fatalf("expected outbox max backoff 2m, got %s", cfg.Worker.MaxBackoff)
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
		"APP_ENV":           "   ",
		"SERVICE_NAME":      "",
		"HTTP_ADDR":         "\t",
		"WORKER_ADMIN_ADDR": " ",
		"DATABASE_URL":      "",
		"LOG_LEVEL":         "",
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

	if cfg.Worker.AdminAddr != ":8081" {
		t.Fatalf("expected blank WORKER_ADMIN_ADDR to fall back to default, got %q", cfg.Worker.AdminAddr)
	}

	if cfg.Database.URL != "postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable" {
		t.Fatalf("expected blank DATABASE_URL to fall back to default, got %q", cfg.Database.URL)
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

func TestLoadWithLookupRejectsInvalidDBPingTimeout(t *testing.T) {
	_, err := loadWithLookup(mapLookup(map[string]string{
		"DB_PING_TIMEOUT": "not-a-duration",
	}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "invalid DB_PING_TIMEOUT") {
		t.Fatalf("expected DB_PING_TIMEOUT error, got %v", err)
	}
}

func TestLoadWithLookupRejectsInvalidDBQueryTimeout(t *testing.T) {
	_, err := loadWithLookup(mapLookup(map[string]string{
		"DB_QUERY_TIMEOUT": "not-a-duration",
	}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "invalid DB_QUERY_TIMEOUT") {
		t.Fatalf("expected DB_QUERY_TIMEOUT error, got %v", err)
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

func TestLoadWithLookupRejectsInvalidInt(t *testing.T) {
	_, err := loadWithLookup(mapLookup(map[string]string{
		"OUTBOX_MAX_ATTEMPTS": "not-an-int",
	}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "invalid OUTBOX_MAX_ATTEMPTS") {
		t.Fatalf("expected OUTBOX_MAX_ATTEMPTS error, got %v", err)
	}
}

func TestLoadWithLookupRejectsNonPositiveInt(t *testing.T) {
	_, err := loadWithLookup(mapLookup(map[string]string{
		"OUTBOX_MAX_ATTEMPTS": "0",
	}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "OUTBOX_MAX_ATTEMPTS") {
		t.Fatalf("expected OUTBOX_MAX_ATTEMPTS error, got %v", err)
	}
}

func TestLoadWithLookupValidatesWorkerLeaseBudget(t *testing.T) {
	tests := []struct {
		name        string
		lockTTL     string
		publishTime string
		queryTime   string
		wantErr     bool
	}{
		{
			name:        "lock ttl shorter than publish timeout",
			lockTTL:     "4s",
			publishTime: "5s",
			queryTime:   "2s",
			wantErr:     true,
		},
		{
			name:        "lock ttl equals publish timeout",
			lockTTL:     "5s",
			publishTime: "5s",
			queryTime:   "2s",
			wantErr:     true,
		},
		{
			name:        "lock ttl shorter than timeout sum",
			lockTTL:     "6s",
			publishTime: "5s",
			queryTime:   "2s",
			wantErr:     true,
		},
		{
			name:        "lock ttl equals timeout sum",
			lockTTL:     "7s",
			publishTime: "5s",
			queryTime:   "2s",
			wantErr:     true,
		},
		{
			name:        "lock ttl greater than timeout sum",
			lockTTL:     "8s",
			publishTime: "5s",
			queryTime:   "2s",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadWithLookup(mapLookup(map[string]string{
				"OUTBOX_LOCK_TTL":        tt.lockTTL,
				"OUTBOX_PUBLISH_TIMEOUT": tt.publishTime,
				"DB_QUERY_TIMEOUT":       tt.queryTime,
			}))

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				if !strings.Contains(err.Error(), "OUTBOX_LOCK_TTL") {
					t.Fatalf("expected OUTBOX_LOCK_TTL error, got %v", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestLoadWithLookupRejectsInvalidWorkerBackoff(t *testing.T) {
	_, err := loadWithLookup(mapLookup(map[string]string{
		"OUTBOX_BASE_BACKOFF": "10s",
		"OUTBOX_MAX_BACKOFF":  "5s",
	}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "OUTBOX_MAX_BACKOFF") {
		t.Fatalf("expected OUTBOX_MAX_BACKOFF error, got %v", err)
	}
}

func mapLookup(values map[string]string) lookupFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
