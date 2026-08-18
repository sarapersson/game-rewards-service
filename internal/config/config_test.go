package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadAPIWithLookupUsesDefaults(t *testing.T) {
	cfg, err := loadAPIWithLookup(mapLookup(nil))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.AppEnv != "local" {
		t.Fatalf("expected default app env local, got %q", cfg.AppEnv)
	}
	if cfg.ServiceName != "game-rewards-service" {
		t.Fatalf("expected default service name, got %q", cfg.ServiceName)
	}
	if cfg.HTTP.Addr != "127.0.0.1:8080" {
		t.Fatalf("expected default HTTP addr 127.0.0.1:8080, got %q", cfg.HTTP.Addr)
	}
	assertDefaultSharedSettings(t, cfg.HTTP, cfg.Database, cfg.Log, cfg.ShutdownTimeout)
}

func TestLoadWorkerWithLookupUsesDefaults(t *testing.T) {
	cfg, err := loadWorkerWithLookup(mapLookup(nil))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.AppEnv != "local" {
		t.Fatalf("expected default app env local, got %q", cfg.AppEnv)
	}
	if cfg.ServiceName != "game-rewards-service" {
		t.Fatalf("expected default service name, got %q", cfg.ServiceName)
	}
	if cfg.AdminHTTP.Addr != "127.0.0.1:8081" {
		t.Fatalf("expected default worker admin addr 127.0.0.1:8081, got %q", cfg.AdminHTTP.Addr)
	}
	assertDefaultSharedSettings(t, cfg.AdminHTTP, cfg.Database, cfg.Log, cfg.ShutdownTimeout)

	if cfg.Outbox.PollInterval != time.Second {
		t.Fatalf("expected worker poll interval 1s, got %s", cfg.Outbox.PollInterval)
	}
	if cfg.Outbox.LockTTL != 30*time.Second {
		t.Fatalf("expected outbox lock TTL 30s, got %s", cfg.Outbox.LockTTL)
	}
	if cfg.Outbox.PublishTimeout != 5*time.Second {
		t.Fatalf("expected outbox publish timeout 5s, got %s", cfg.Outbox.PublishTimeout)
	}
	if cfg.Outbox.MaxFailures != 5 {
		t.Fatalf("expected outbox max failures 5, got %d", cfg.Outbox.MaxFailures)
	}
	if cfg.Outbox.BaseBackoff != time.Second {
		t.Fatalf("expected outbox base backoff 1s, got %s", cfg.Outbox.BaseBackoff)
	}
	if cfg.Outbox.MaxBackoff != time.Minute {
		t.Fatalf("expected outbox max backoff 1m, got %s", cfg.Outbox.MaxBackoff)
	}
}

func TestLoadAPIWithLookupUsesEnvironmentOverrides(t *testing.T) {
	cfg, err := loadAPIWithLookup(mapLookup(map[string]string{
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
		"SHUTDOWN_TIMEOUT":         "3s",
		"LOG_LEVEL":                "debug",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	assertCommonOverrides(t, cfg.AppEnv, cfg.ServiceName, cfg.Database, cfg.Log, cfg.ShutdownTimeout)
	assertHTTPOverrides(t, cfg.HTTP, ":9090")
}

func TestLoadWorkerWithLookupUsesEnvironmentOverrides(t *testing.T) {
	cfg, err := loadWorkerWithLookup(mapLookup(map[string]string{
		"APP_ENV":                  "test",
		"SERVICE_NAME":             "custom-service",
		"WORKER_ADMIN_ADDR":        ":9191",
		"HTTP_READ_TIMEOUT":        "1s",
		"HTTP_READ_HEADER_TIMEOUT": "500ms",
		"HTTP_WRITE_TIMEOUT":       "2s",
		"HTTP_IDLE_TIMEOUT":        "30s",
		"DATABASE_URL":             "postgres://custom:secret@localhost:5433/custom?sslmode=disable",
		"DB_PING_TIMEOUT":          "750ms",
		"DB_QUERY_TIMEOUT":         "900ms",
		"WORKER_POLL_INTERVAL":     "250ms",
		"OUTBOX_LOCK_TTL":          "45s",
		"OUTBOX_PUBLISH_TIMEOUT":   "10s",
		"OUTBOX_MAX_FAILURES":      "7",
		"OUTBOX_BASE_BACKOFF":      "2s",
		"OUTBOX_MAX_BACKOFF":       "2m",
		"SHUTDOWN_TIMEOUT":         "3s",
		"LOG_LEVEL":                "debug",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	assertCommonOverrides(t, cfg.AppEnv, cfg.ServiceName, cfg.Database, cfg.Log, cfg.ShutdownTimeout)
	assertHTTPOverrides(t, cfg.AdminHTTP, ":9191")

	if cfg.Outbox.PollInterval != 250*time.Millisecond {
		t.Fatalf("expected worker poll interval 250ms, got %s", cfg.Outbox.PollInterval)
	}
	if cfg.Outbox.LockTTL != 45*time.Second {
		t.Fatalf("expected outbox lock TTL 45s, got %s", cfg.Outbox.LockTTL)
	}
	if cfg.Outbox.PublishTimeout != 10*time.Second {
		t.Fatalf("expected outbox publish timeout 10s, got %s", cfg.Outbox.PublishTimeout)
	}
	if cfg.Outbox.MaxFailures != 7 {
		t.Fatalf("expected outbox max failures 7, got %d", cfg.Outbox.MaxFailures)
	}
	if cfg.Outbox.BaseBackoff != 2*time.Second {
		t.Fatalf("expected outbox base backoff 2s, got %s", cfg.Outbox.BaseBackoff)
	}
	if cfg.Outbox.MaxBackoff != 2*time.Minute {
		t.Fatalf("expected outbox max backoff 2m, got %s", cfg.Outbox.MaxBackoff)
	}
}

func TestLoadAPIIgnoresWorkerOnlyOverrides(t *testing.T) {
	_, err := loadAPIWithLookup(mapLookup(map[string]string{
		"WORKER_ADMIN_ADDR":      " ",
		"WORKER_POLL_INTERVAL":   "not-a-duration",
		"OUTBOX_LOCK_TTL":        "1s",
		"OUTBOX_PUBLISH_TIMEOUT": "5s",
		"OUTBOX_MAX_FAILURES":    "0",
		"OUTBOX_BASE_BACKOFF":    "10s",
		"OUTBOX_MAX_BACKOFF":     "5s",
	}))
	if err != nil {
		t.Fatalf("API config rejected worker-only overrides: %v", err)
	}
}

func TestLoadWorkerIgnoresAPIOnlyOverrides(t *testing.T) {
	_, err := loadWorkerWithLookup(mapLookup(map[string]string{
		"HTTP_ADDR": " ",
	}))
	if err != nil {
		t.Fatalf("worker config rejected API-only override: %v", err)
	}
}

func TestProcessLoadersRejectBlankOwnedOverrides(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		load func(lookupFunc) error
	}{
		{
			name: "api",
			keys: []string{
				"APP_ENV",
				"SERVICE_NAME",
				"HTTP_ADDR",
				"HTTP_READ_TIMEOUT",
				"HTTP_READ_HEADER_TIMEOUT",
				"HTTP_WRITE_TIMEOUT",
				"HTTP_IDLE_TIMEOUT",
				"DATABASE_URL",
				"DB_PING_TIMEOUT",
				"DB_QUERY_TIMEOUT",
				"SHUTDOWN_TIMEOUT",
				"LOG_LEVEL",
			},
			load: func(lookup lookupFunc) error {
				_, err := loadAPIWithLookup(lookup)
				return err
			},
		},
		{
			name: "worker",
			keys: []string{
				"APP_ENV",
				"SERVICE_NAME",
				"WORKER_ADMIN_ADDR",
				"HTTP_READ_TIMEOUT",
				"HTTP_READ_HEADER_TIMEOUT",
				"HTTP_WRITE_TIMEOUT",
				"HTTP_IDLE_TIMEOUT",
				"DATABASE_URL",
				"DB_PING_TIMEOUT",
				"DB_QUERY_TIMEOUT",
				"WORKER_POLL_INTERVAL",
				"OUTBOX_LOCK_TTL",
				"OUTBOX_PUBLISH_TIMEOUT",
				"OUTBOX_MAX_FAILURES",
				"OUTBOX_BASE_BACKOFF",
				"OUTBOX_MAX_BACKOFF",
				"SHUTDOWN_TIMEOUT",
				"LOG_LEVEL",
			},
			load: func(lookup lookupFunc) error {
				_, err := loadWorkerWithLookup(lookup)
				return err
			},
		},
	}

	for _, process := range tests {
		t.Run(process.name, func(t *testing.T) {
			for _, key := range process.keys {
				t.Run(key, func(t *testing.T) {
					err := process.load(mapLookup(map[string]string{key: " \t "}))
					if err == nil {
						t.Fatal("expected error, got nil")
					}

					want := "invalid " + key + ": must not be empty"
					if err.Error() != want {
						t.Fatalf("expected %q, got %q", want, err.Error())
					}
				})
			}
		})
	}
}

func TestLoadAPIWithLookupRejectsInvalidSharedValues(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]string
		wantKey string
	}{
		{name: "HTTP duration", values: map[string]string{"HTTP_READ_TIMEOUT": "not-a-duration"}, wantKey: "HTTP_READ_TIMEOUT"},
		{name: "database ping timeout", values: map[string]string{"DB_PING_TIMEOUT": "not-a-duration"}, wantKey: "DB_PING_TIMEOUT"},
		{name: "database query timeout", values: map[string]string{"DB_QUERY_TIMEOUT": "not-a-duration"}, wantKey: "DB_QUERY_TIMEOUT"},
		{name: "log level", values: map[string]string{"LOG_LEVEL": "verbose"}, wantKey: "LOG_LEVEL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadAPIWithLookup(mapLookup(tt.values))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Fatalf("expected %s error, got %v", tt.wantKey, err)
			}
		})
	}
}

func TestLoadWorkerWithLookupRejectsInvalidOutboxValues(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]string
		wantKey string
	}{
		{name: "poll interval", values: map[string]string{"WORKER_POLL_INTERVAL": "0s"}, wantKey: "WORKER_POLL_INTERVAL"},
		{name: "max failures format", values: map[string]string{"OUTBOX_MAX_FAILURES": "not-an-int"}, wantKey: "OUTBOX_MAX_FAILURES"},
		{name: "max failures value", values: map[string]string{"OUTBOX_MAX_FAILURES": "0"}, wantKey: "OUTBOX_MAX_FAILURES"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadWorkerWithLookup(mapLookup(tt.values))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Fatalf("expected %s error, got %v", tt.wantKey, err)
			}
		})
	}
}

func TestLoadWorkerWithLookupValidatesLeaseBudget(t *testing.T) {
	tests := []struct {
		name        string
		lockTTL     string
		publishTime string
		queryTime   string
		wantErr     bool
	}{
		{name: "lock ttl shorter than publish timeout", lockTTL: "4s", publishTime: "5s", queryTime: "2s", wantErr: true},
		{name: "lock ttl equals publish timeout", lockTTL: "5s", publishTime: "5s", queryTime: "2s", wantErr: true},
		{name: "lock ttl shorter than publish plus one query", lockTTL: "6s", publishTime: "5s", queryTime: "2s", wantErr: true},
		{name: "lock ttl equals publish plus one query", lockTTL: "7s", publishTime: "5s", queryTime: "2s", wantErr: true},
		{name: "lock ttl covers one query but not two", lockTTL: "8s", publishTime: "5s", queryTime: "2s", wantErr: true},
		{name: "lock ttl equals publish plus two queries", lockTTL: "9s", publishTime: "5s", queryTime: "2s", wantErr: true},
		{name: "lock ttl exceeds publish plus two queries", lockTTL: "10s", publishTime: "5s", queryTime: "2s", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadWorkerWithLookup(mapLookup(map[string]string{
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

func TestValidateOutboxLeaseBudgetDoesNotOverflow(t *testing.T) {
	const maxDuration = time.Duration(1<<63 - 1)

	err := validateOutbox(
		OutboxConfig{
			LockTTL:        maxDuration,
			PublishTimeout: maxDuration - 3*time.Second,
		},
		2*time.Second,
	)
	if err == nil {
		t.Fatal("expected lease budget error, got nil")
	}
	if !strings.Contains(err.Error(), "OUTBOX_LOCK_TTL") {
		t.Fatalf("expected OUTBOX_LOCK_TTL error, got %v", err)
	}
}

func TestLoadWorkerWithLookupRejectsInvalidBackoff(t *testing.T) {
	_, err := loadWorkerWithLookup(mapLookup(map[string]string{
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

func assertDefaultSharedSettings(
	t *testing.T,
	httpConfig HTTPConfig,
	database DatabaseConfig,
	logConfig LogConfig,
	shutdownTimeout time.Duration,
) {
	t.Helper()

	if httpConfig.ReadTimeout != 5*time.Second {
		t.Fatalf("expected read timeout 5s, got %s", httpConfig.ReadTimeout)
	}
	if httpConfig.ReadHeaderTimeout != 2*time.Second {
		t.Fatalf("expected read header timeout 2s, got %s", httpConfig.ReadHeaderTimeout)
	}
	if httpConfig.WriteTimeout != 10*time.Second {
		t.Fatalf("expected write timeout 10s, got %s", httpConfig.WriteTimeout)
	}
	if httpConfig.IdleTimeout != 60*time.Second {
		t.Fatalf("expected idle timeout 60s, got %s", httpConfig.IdleTimeout)
	}
	if database.URL != "postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable" {
		t.Fatalf("expected default database URL, got %q", database.URL)
	}
	if database.PingTimeout != 2*time.Second {
		t.Fatalf("expected database ping timeout 2s, got %s", database.PingTimeout)
	}
	if database.QueryTimeout != 2*time.Second {
		t.Fatalf("expected database query timeout 2s, got %s", database.QueryTimeout)
	}
	if shutdownTimeout != 10*time.Second {
		t.Fatalf("expected shutdown timeout 10s, got %s", shutdownTimeout)
	}
	if logConfig.Level != slog.LevelInfo {
		t.Fatalf("expected info log level, got %s", logConfig.Level)
	}
}

func assertCommonOverrides(
	t *testing.T,
	appEnv string,
	serviceName string,
	database DatabaseConfig,
	logConfig LogConfig,
	shutdownTimeout time.Duration,
) {
	t.Helper()

	if appEnv != "test" {
		t.Fatalf("expected app env test, got %q", appEnv)
	}
	if serviceName != "custom-service" {
		t.Fatalf("expected custom service name, got %q", serviceName)
	}
	if database.URL != "postgres://custom:secret@localhost:5433/custom?sslmode=disable" {
		t.Fatalf("expected custom database URL, got %q", database.URL)
	}
	if database.PingTimeout != 750*time.Millisecond {
		t.Fatalf("expected database ping timeout 750ms, got %s", database.PingTimeout)
	}
	if database.QueryTimeout != 900*time.Millisecond {
		t.Fatalf("expected database query timeout 900ms, got %s", database.QueryTimeout)
	}
	if shutdownTimeout != 3*time.Second {
		t.Fatalf("expected shutdown timeout 3s, got %s", shutdownTimeout)
	}
	if logConfig.Level != slog.LevelDebug {
		t.Fatalf("expected debug log level, got %s", logConfig.Level)
	}
}

func assertHTTPOverrides(t *testing.T, cfg HTTPConfig, wantAddr string) {
	t.Helper()

	if cfg.Addr != wantAddr {
		t.Fatalf("expected HTTP addr %s, got %q", wantAddr, cfg.Addr)
	}
	if cfg.ReadTimeout != time.Second {
		t.Fatalf("expected read timeout 1s, got %s", cfg.ReadTimeout)
	}
	if cfg.ReadHeaderTimeout != 500*time.Millisecond {
		t.Fatalf("expected read header timeout 500ms, got %s", cfg.ReadHeaderTimeout)
	}
	if cfg.WriteTimeout != 2*time.Second {
		t.Fatalf("expected write timeout 2s, got %s", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 30*time.Second {
		t.Fatalf("expected idle timeout 30s, got %s", cfg.IdleTimeout)
	}
}

func mapLookup(values map[string]string) lookupFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
