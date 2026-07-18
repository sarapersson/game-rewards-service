// Package config loads and validates runtime configuration for the service.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAppEnv                = "local"
	defaultServiceName           = "game-rewards-service"
	defaultHTTPAddr              = ":8080"
	defaultHTTPReadTimeout       = 5 * time.Second
	defaultHTTPReadHeaderTimeout = 2 * time.Second
	defaultHTTPWriteTimeout      = 10 * time.Second
	defaultHTTPIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout       = 10 * time.Second
	defaultLogLevel              = slog.LevelInfo
	defaultDatabaseURL           = "postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable"
	defaultDBPingTimeout         = 2 * time.Second
	defaultDBQueryTimeout        = 2 * time.Second
	defaultWorkerAdminAddr       = ":8081"
	defaultWorkerPollInterval    = 1 * time.Second
	defaultOutboxLockTTL         = 30 * time.Second
	defaultOutboxPublishTimeout  = 5 * time.Second
	defaultOutboxMaxAttempts     = 5
	defaultOutboxBaseBackoff     = 1 * time.Second
	defaultOutboxMaxBackoff      = 1 * time.Minute
)

// Config contains all runtime configuration needed by the service.
type Config struct {
	AppEnv          string
	ServiceName     string
	HTTP            HTTPConfig
	Database        DatabaseConfig
	Worker          WorkerConfig
	Log             LogConfig
	ShutdownTimeout time.Duration
}

// HTTPConfig contains HTTP server settings.
type HTTPConfig struct {
	Addr              string
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

// DatabaseConfig contains PostgreSQL connection settings.
type DatabaseConfig struct {
	URL          string
	PingTimeout  time.Duration
	QueryTimeout time.Duration
}

// WorkerConfig contains outbox worker settings.
type WorkerConfig struct {
	AdminAddr      string
	PollInterval   time.Duration
	OutboxLockTTL  time.Duration
	PublishTimeout time.Duration
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
}

// LogConfig contains structured logging settings.
type LogConfig struct {
	Level slog.Level
}

// Load reads configuration from process environment variables.
func Load() (Config, error) {
	return loadWithLookup(os.LookupEnv)
}

type lookupFunc func(string) (string, bool)

func loadWithLookup(lookup lookupFunc) (Config, error) {
	cfg := Config{
		AppEnv:      getString(lookup, "APP_ENV", defaultAppEnv),
		ServiceName: getString(lookup, "SERVICE_NAME", defaultServiceName),
		HTTP: HTTPConfig{
			Addr: getString(lookup, "HTTP_ADDR", defaultHTTPAddr),
		},
		Database: DatabaseConfig{
			URL: getString(lookup, "DATABASE_URL", defaultDatabaseURL),
		},
		Worker: WorkerConfig{
			AdminAddr:   getString(lookup, "WORKER_ADMIN_ADDR", defaultWorkerAdminAddr),
			MaxAttempts: defaultOutboxMaxAttempts,
		},
		ShutdownTimeout: defaultShutdownTimeout,
		Log: LogConfig{
			Level: defaultLogLevel,
		},
	}

	var err error

	cfg.HTTP.ReadTimeout, err = getDuration(lookup, "HTTP_READ_TIMEOUT", defaultHTTPReadTimeout)
	if err != nil {
		return Config{}, err
	}

	cfg.HTTP.ReadHeaderTimeout, err = getDuration(lookup, "HTTP_READ_HEADER_TIMEOUT", defaultHTTPReadHeaderTimeout)
	if err != nil {
		return Config{}, err
	}

	cfg.HTTP.WriteTimeout, err = getDuration(lookup, "HTTP_WRITE_TIMEOUT", defaultHTTPWriteTimeout)
	if err != nil {
		return Config{}, err
	}

	cfg.HTTP.IdleTimeout, err = getDuration(lookup, "HTTP_IDLE_TIMEOUT", defaultHTTPIdleTimeout)
	if err != nil {
		return Config{}, err
	}

	cfg.Database.PingTimeout, err = getDuration(lookup, "DB_PING_TIMEOUT", defaultDBPingTimeout)
	if err != nil {
		return Config{}, err
	}

	cfg.Database.QueryTimeout, err = getDuration(lookup, "DB_QUERY_TIMEOUT", defaultDBQueryTimeout)
	if err != nil {
		return Config{}, err
	}

	cfg.Worker.PollInterval, err = getDuration(lookup, "WORKER_POLL_INTERVAL", defaultWorkerPollInterval)
	if err != nil {
		return Config{}, err
	}

	cfg.Worker.OutboxLockTTL, err = getDuration(lookup, "OUTBOX_LOCK_TTL", defaultOutboxLockTTL)
	if err != nil {
		return Config{}, err
	}

	cfg.Worker.PublishTimeout, err = getDuration(lookup, "OUTBOX_PUBLISH_TIMEOUT", defaultOutboxPublishTimeout)
	if err != nil {
		return Config{}, err
	}

	cfg.Worker.MaxAttempts, err = getInt(lookup, "OUTBOX_MAX_ATTEMPTS", defaultOutboxMaxAttempts)
	if err != nil {
		return Config{}, err
	}

	cfg.Worker.BaseBackoff, err = getDuration(lookup, "OUTBOX_BASE_BACKOFF", defaultOutboxBaseBackoff)
	if err != nil {
		return Config{}, err
	}

	cfg.Worker.MaxBackoff, err = getDuration(lookup, "OUTBOX_MAX_BACKOFF", defaultOutboxMaxBackoff)
	if err != nil {
		return Config{}, err
	}

	cfg.ShutdownTimeout, err = getDuration(lookup, "SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}

	cfg.Log.Level, err = getLogLevel(lookup, "LOG_LEVEL", defaultLogLevel)
	if err != nil {
		return Config{}, err
	}

	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func getString(lookup lookupFunc, key string, defaultValue string) string {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return defaultValue
	}

	return strings.TrimSpace(value)
}

func getDuration(lookup lookupFunc, key string, defaultValue time.Duration) (time.Duration, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return defaultValue, nil
	}

	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}

	if duration <= 0 {
		return 0, fmt.Errorf("invalid %s: must be greater than zero", key)
	}

	return duration, nil
}

func getInt(lookup lookupFunc, key string, defaultValue int) (int, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return defaultValue, nil
	}

	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}

	if parsed <= 0 {
		return 0, fmt.Errorf("invalid %s: must be greater than zero", key)
	}

	return parsed, nil
}

func getLogLevel(lookup lookupFunc, key string, defaultValue slog.Level) (slog.Level, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return defaultValue, nil
	}

	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid %s: supported values are debug, info, warn, error", key)
	}
}

func validate(cfg Config) error {
	if strings.TrimSpace(cfg.AppEnv) == "" {
		return fmt.Errorf("APP_ENV must not be empty")
	}

	if strings.TrimSpace(cfg.ServiceName) == "" {
		return fmt.Errorf("SERVICE_NAME must not be empty")
	}

	if strings.TrimSpace(cfg.HTTP.Addr) == "" {
		return fmt.Errorf("HTTP_ADDR must not be empty")
	}

	if strings.TrimSpace(cfg.Database.URL) == "" {
		return fmt.Errorf("DATABASE_URL must not be empty")
	}

	if strings.TrimSpace(cfg.Worker.AdminAddr) == "" {
		return fmt.Errorf("WORKER_ADMIN_ADDR must not be empty")
	}

	if cfg.Worker.OutboxLockTTL <= cfg.Worker.PublishTimeout {
		return fmt.Errorf(
			"OUTBOX_LOCK_TTL must be greater than OUTBOX_PUBLISH_TIMEOUT plus DB_QUERY_TIMEOUT",
		)
	}

	remainingLeaseTime := cfg.Worker.OutboxLockTTL - cfg.Worker.PublishTimeout
	if remainingLeaseTime <= cfg.Database.QueryTimeout {
		return fmt.Errorf(
			"OUTBOX_LOCK_TTL must be greater than OUTBOX_PUBLISH_TIMEOUT plus DB_QUERY_TIMEOUT",
		)
	}

	if cfg.Worker.MaxBackoff < cfg.Worker.BaseBackoff {
		return fmt.Errorf(
			"OUTBOX_MAX_BACKOFF must be greater than or equal to OUTBOX_BASE_BACKOFF",
		)
	}

	return nil
}
