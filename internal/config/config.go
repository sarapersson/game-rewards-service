// Package config loads and validates process runtime configuration.
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
	defaultHTTPAddr              = "127.0.0.1:8080"
	defaultHTTPReadTimeout       = 5 * time.Second
	defaultHTTPReadHeaderTimeout = 2 * time.Second
	defaultHTTPWriteTimeout      = 10 * time.Second
	defaultHTTPIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout       = 10 * time.Second
	defaultLogLevel              = slog.LevelInfo
	defaultDatabaseURL           = "postgres://game_rewards:game_rewards_dev_password@localhost:5432/game_rewards?sslmode=disable"
	defaultDBPingTimeout         = 2 * time.Second
	defaultDBQueryTimeout        = 2 * time.Second
	defaultWorkerAdminAddr       = "127.0.0.1:8081"
	defaultWorkerPollInterval    = 1 * time.Second
	defaultOutboxLockTTL         = 30 * time.Second
	defaultOutboxPublishTimeout  = 5 * time.Second
	defaultOutboxMaxAttempts     = 5
	defaultOutboxBaseBackoff     = 1 * time.Second
	defaultOutboxMaxBackoff      = 1 * time.Minute
)

// APIConfig contains runtime configuration owned by the API process.
type APIConfig struct {
	AppEnv          string
	ServiceName     string
	HTTP            HTTPConfig
	Database        DatabaseConfig
	Log             LogConfig
	ShutdownTimeout time.Duration
}

// WorkerConfig contains runtime configuration owned by the worker process.
type WorkerConfig struct {
	AppEnv          string
	ServiceName     string
	AdminHTTP       HTTPConfig
	Database        DatabaseConfig
	Outbox          OutboxConfig
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

// OutboxConfig contains outbox worker runtime settings.
type OutboxConfig struct {
	PollInterval   time.Duration
	LockTTL        time.Duration
	PublishTimeout time.Duration
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
}

// LogConfig contains structured logging settings.
type LogConfig struct {
	Level slog.Level
}

// LoadAPI reads and validates configuration owned by the API process.
func LoadAPI() (APIConfig, error) {
	return loadAPIWithLookup(os.LookupEnv)
}

// LoadWorker reads and validates configuration owned by the worker process.
func LoadWorker() (WorkerConfig, error) {
	return loadWorkerWithLookup(os.LookupEnv)
}

type lookupFunc func(string) (string, bool)

type commonConfig struct {
	AppEnv          string
	ServiceName     string
	Database        DatabaseConfig
	Log             LogConfig
	ShutdownTimeout time.Duration
}

func loadAPIWithLookup(lookup lookupFunc) (APIConfig, error) {
	common, err := loadCommon(lookup)
	if err != nil {
		return APIConfig{}, err
	}

	httpConfig, err := loadHTTPConfig(lookup, "HTTP_ADDR", defaultHTTPAddr)
	if err != nil {
		return APIConfig{}, err
	}

	return APIConfig{
		AppEnv:          common.AppEnv,
		ServiceName:     common.ServiceName,
		HTTP:            httpConfig,
		Database:        common.Database,
		Log:             common.Log,
		ShutdownTimeout: common.ShutdownTimeout,
	}, nil
}

func loadWorkerWithLookup(lookup lookupFunc) (WorkerConfig, error) {
	common, err := loadCommon(lookup)
	if err != nil {
		return WorkerConfig{}, err
	}

	adminHTTP, err := loadHTTPConfig(lookup, "WORKER_ADMIN_ADDR", defaultWorkerAdminAddr)
	if err != nil {
		return WorkerConfig{}, err
	}

	outbox, err := loadOutboxConfig(lookup)
	if err != nil {
		return WorkerConfig{}, err
	}

	if err := validateOutbox(outbox, common.Database.QueryTimeout); err != nil {
		return WorkerConfig{}, err
	}

	return WorkerConfig{
		AppEnv:          common.AppEnv,
		ServiceName:     common.ServiceName,
		AdminHTTP:       adminHTTP,
		Database:        common.Database,
		Outbox:          outbox,
		Log:             common.Log,
		ShutdownTimeout: common.ShutdownTimeout,
	}, nil
}

func loadCommon(lookup lookupFunc) (commonConfig, error) {
	var cfg commonConfig
	var err error

	cfg.AppEnv, err = getString(lookup, "APP_ENV", defaultAppEnv)
	if err != nil {
		return commonConfig{}, err
	}

	cfg.ServiceName, err = getString(lookup, "SERVICE_NAME", defaultServiceName)
	if err != nil {
		return commonConfig{}, err
	}

	cfg.Database, err = loadDatabaseConfig(lookup)
	if err != nil {
		return commonConfig{}, err
	}

	cfg.ShutdownTimeout, err = getDuration(lookup, "SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return commonConfig{}, err
	}

	cfg.Log.Level, err = getLogLevel(lookup, "LOG_LEVEL", defaultLogLevel)
	if err != nil {
		return commonConfig{}, err
	}

	return cfg, nil
}

func loadHTTPConfig(lookup lookupFunc, addrKey, defaultAddr string) (HTTPConfig, error) {
	var cfg HTTPConfig
	var err error

	cfg.Addr, err = getString(lookup, addrKey, defaultAddr)
	if err != nil {
		return HTTPConfig{}, err
	}

	cfg.ReadTimeout, err = getDuration(lookup, "HTTP_READ_TIMEOUT", defaultHTTPReadTimeout)
	if err != nil {
		return HTTPConfig{}, err
	}

	cfg.ReadHeaderTimeout, err = getDuration(lookup, "HTTP_READ_HEADER_TIMEOUT", defaultHTTPReadHeaderTimeout)
	if err != nil {
		return HTTPConfig{}, err
	}

	cfg.WriteTimeout, err = getDuration(lookup, "HTTP_WRITE_TIMEOUT", defaultHTTPWriteTimeout)
	if err != nil {
		return HTTPConfig{}, err
	}

	cfg.IdleTimeout, err = getDuration(lookup, "HTTP_IDLE_TIMEOUT", defaultHTTPIdleTimeout)
	if err != nil {
		return HTTPConfig{}, err
	}

	return cfg, nil
}

func loadDatabaseConfig(lookup lookupFunc) (DatabaseConfig, error) {
	var cfg DatabaseConfig
	var err error

	cfg.URL, err = getString(lookup, "DATABASE_URL", defaultDatabaseURL)
	if err != nil {
		return DatabaseConfig{}, err
	}

	cfg.PingTimeout, err = getDuration(lookup, "DB_PING_TIMEOUT", defaultDBPingTimeout)
	if err != nil {
		return DatabaseConfig{}, err
	}

	cfg.QueryTimeout, err = getDuration(lookup, "DB_QUERY_TIMEOUT", defaultDBQueryTimeout)
	if err != nil {
		return DatabaseConfig{}, err
	}

	return cfg, nil
}

func loadOutboxConfig(lookup lookupFunc) (OutboxConfig, error) {
	var cfg OutboxConfig
	var err error

	cfg.PollInterval, err = getDuration(lookup, "WORKER_POLL_INTERVAL", defaultWorkerPollInterval)
	if err != nil {
		return OutboxConfig{}, err
	}

	cfg.LockTTL, err = getDuration(lookup, "OUTBOX_LOCK_TTL", defaultOutboxLockTTL)
	if err != nil {
		return OutboxConfig{}, err
	}

	cfg.PublishTimeout, err = getDuration(lookup, "OUTBOX_PUBLISH_TIMEOUT", defaultOutboxPublishTimeout)
	if err != nil {
		return OutboxConfig{}, err
	}

	cfg.MaxAttempts, err = getInt(lookup, "OUTBOX_MAX_ATTEMPTS", defaultOutboxMaxAttempts)
	if err != nil {
		return OutboxConfig{}, err
	}

	cfg.BaseBackoff, err = getDuration(lookup, "OUTBOX_BASE_BACKOFF", defaultOutboxBaseBackoff)
	if err != nil {
		return OutboxConfig{}, err
	}

	cfg.MaxBackoff, err = getDuration(lookup, "OUTBOX_MAX_BACKOFF", defaultOutboxMaxBackoff)
	if err != nil {
		return OutboxConfig{}, err
	}

	return cfg, nil
}

func getString(lookup lookupFunc, key string, defaultValue string) (string, error) {
	value, ok := lookup(key)
	if !ok {
		return defaultValue, nil
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("invalid %s: must not be empty", key)
	}

	return value, nil
}

func getDuration(lookup lookupFunc, key string, defaultValue time.Duration) (time.Duration, error) {
	value, ok := lookup(key)
	if !ok {
		return defaultValue, nil
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("invalid %s: must not be empty", key)
	}

	duration, err := time.ParseDuration(value)
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
	if !ok {
		return defaultValue, nil
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("invalid %s: must not be empty", key)
	}

	parsed, err := strconv.Atoi(value)
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
	if !ok {
		return defaultValue, nil
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("invalid %s: must not be empty", key)
	}

	switch strings.ToLower(value) {
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

func validateOutbox(cfg OutboxConfig, queryTimeout time.Duration) error {
	if cfg.LockTTL <= cfg.PublishTimeout {
		return fmt.Errorf(
			"OUTBOX_LOCK_TTL must be greater than OUTBOX_PUBLISH_TIMEOUT plus DB_QUERY_TIMEOUT",
		)
	}

	remainingLeaseTime := cfg.LockTTL - cfg.PublishTimeout
	if remainingLeaseTime <= queryTimeout {
		return fmt.Errorf(
			"OUTBOX_LOCK_TTL must be greater than OUTBOX_PUBLISH_TIMEOUT plus DB_QUERY_TIMEOUT",
		)
	}

	if cfg.MaxBackoff < cfg.BaseBackoff {
		return fmt.Errorf(
			"OUTBOX_MAX_BACKOFF must be greater than or equal to OUTBOX_BASE_BACKOFF",
		)
	}

	return nil
}
