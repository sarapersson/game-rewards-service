package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sarapersson/game-rewards-service/internal/config"
)

func TestOpenPoolRedactsInvalidDatabaseConfiguration(t *testing.T) {
	const (
		secret      = "do-not-log-this-database-password"
		databaseURL = "postgres://game_rewards:" + secret + "@localhost:not-a-port/game_rewards?sslmode=disable"
	)

	pool, err := OpenPool(context.Background(), config.DatabaseConfig{URL: databaseURL})
	if err == nil {
		if pool != nil {
			pool.Close()
		}

		t.Fatal("expected error, got nil")
	}

	if pool != nil {
		pool.Close()
		t.Fatal("expected nil pool")
	}

	if !errors.Is(err, ErrInvalidDatabaseConfig) {
		t.Fatalf("expected ErrInvalidDatabaseConfig, got %v", err)
	}

	if strings.Contains(err.Error(), secret) {
		t.Fatal("database configuration error exposed the password")
	}

	if strings.Contains(err.Error(), databaseURL) {
		t.Fatal("database configuration error exposed the connection string")
	}

	const expectedError = "parse database config: invalid database configuration"
	if err.Error() != expectedError {
		t.Fatalf("error = %q, want %q", err.Error(), expectedError)
	}
}

func TestPingRejectsNilPool(t *testing.T) {
	err := Ping(context.Background(), nil, time.Second)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "postgres pool is nil") {
		t.Fatalf("expected nil pool error, got %v", err)
	}
}
