// Package config loads and validates runtime configuration for the service.
package config

import (
	"fmt"
	"log/slog"
	"os"
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
)

// Config contains all runtime configuration needed by the service.
type Config struct {
	AppEnv          string
	ServiceName     string
	HTTP            HTTPConfig
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

	return nil
}
