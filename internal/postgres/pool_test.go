package postgres

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOpenPoolRedactsInvalidDatabaseConfiguration(t *testing.T) {
	const (
		secret      = "do-not-log-this-database-password"
		databaseURL = "postgres://game_rewards:" + secret + "@localhost:not-a-port/game_rewards?sslmode=disable"
	)

	pool, err := OpenPool(context.Background(), databaseURL)
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

	if strings.Contains(err.Error(), secret) {
		t.Fatal("database configuration error exposed the password")
	}

	if strings.Contains(err.Error(), databaseURL) {
		t.Fatal("database configuration error exposed the connection string")
	}

	if !strings.Contains(err.Error(), "invalid database configuration") {
		t.Fatalf("unexpected database configuration error: %v", err)
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
