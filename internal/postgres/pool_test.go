package postgres

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPingRejectsNilPool(t *testing.T) {
	err := Ping(context.Background(), nil, time.Second)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "postgres pool is nil") {
		t.Fatalf("expected nil pool error, got %v", err)
	}
}
