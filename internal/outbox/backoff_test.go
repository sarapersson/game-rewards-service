package outbox

import (
	"testing"
	"time"
)

func TestBackoffPolicyDuration(t *testing.T) {
	policy := BackoffPolicy{
		Base: time.Second,
		Max:  time.Minute,
	}

	tests := []struct {
		name     string
		attempts int
		want     time.Duration
	}{
		{name: "negative attempts uses base", attempts: -1, want: time.Second},
		{name: "first retry uses base", attempts: 0, want: time.Second},
		{name: "second retry doubles base", attempts: 1, want: 2 * time.Second},
		{name: "third retry doubles again", attempts: 2, want: 4 * time.Second},
		{name: "caps at max", attempts: 20, want: time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := policy.Duration(tt.attempts)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}

func TestBackoffPolicyReturnsExactMaximum(t *testing.T) {
	policy := BackoffPolicy{
		Base: time.Second,
		Max:  8 * time.Second,
	}

	got, err := policy.Duration(3)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got != policy.Max {
		t.Fatalf("expected %s, got %s", policy.Max, got)
	}
}

func TestBackoffPolicyRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		policy BackoffPolicy
	}{
		{name: "zero base", policy: BackoffPolicy{Base: 0, Max: time.Second}},
		{name: "zero max", policy: BackoffPolicy{Base: time.Second, Max: 0}},
		{name: "max less than base", policy: BackoffPolicy{Base: time.Minute, Max: time.Second}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.policy.Duration(1)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestBackoffPolicyDoesNotCapBeforeOddMaximum(t *testing.T) {
	policy := BackoffPolicy{
		Base: time.Nanosecond,
		Max:  3 * time.Nanosecond,
	}

	got, err := policy.Duration(1)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got != 2*time.Nanosecond {
		t.Fatalf("expected 2ns, got %s", got)
	}
}
