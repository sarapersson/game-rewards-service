package outbox

import (
	"testing"
	"time"
)

func TestBackoffPolicyRetryDelay(t *testing.T) {
	policy, err := newBackoffPolicy(time.Second, time.Minute)
	if err != nil {
		t.Fatalf("newBackoffPolicy returned error: %v", err)
	}

	tests := []struct {
		name           string
		failedAttempts int
		want           time.Duration
	}{
		{name: "negative failed attempts uses base", failedAttempts: -1, want: time.Second},
		{name: "zero failed attempts uses base", failedAttempts: 0, want: time.Second},
		{name: "first failed attempt uses base", failedAttempts: 1, want: time.Second},
		{name: "second failed attempt doubles base", failedAttempts: 2, want: 2 * time.Second},
		{name: "third failed attempt doubles again", failedAttempts: 3, want: 4 * time.Second},
		{name: "caps at max", failedAttempts: 21, want: time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policy.retryDelay(tt.failedAttempts)

			if got != tt.want {
				t.Fatalf("retryDelay(%d) = %s, want %s", tt.failedAttempts, got, tt.want)
			}
		})
	}
}

func TestBackoffPolicyRetryDelayReturnsExactMaximum(t *testing.T) {
	policy, err := newBackoffPolicy(time.Second, 8*time.Second)
	if err != nil {
		t.Fatalf("newBackoffPolicy returned error: %v", err)
	}

	got := policy.retryDelay(4)
	if got != policy.max {
		t.Fatalf("retryDelay(4) = %s, want %s", got, policy.max)
	}
}

func TestNewBackoffPolicyRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		base time.Duration
		max  time.Duration
	}{
		{name: "zero base", base: 0, max: time.Second},
		{name: "zero max", base: time.Second, max: 0},
		{name: "max less than base", base: time.Minute, max: time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newBackoffPolicy(tt.base, tt.max)
			if err == nil {
				t.Fatal("newBackoffPolicy returned nil error")
			}
		})
	}
}

func TestBackoffPolicyRetryDelayDoesNotCapBeforeOddMaximum(t *testing.T) {
	policy, err := newBackoffPolicy(time.Nanosecond, 3*time.Nanosecond)
	if err != nil {
		t.Fatalf("newBackoffPolicy returned error: %v", err)
	}

	got := policy.retryDelay(2)
	if got != 2*time.Nanosecond {
		t.Fatalf("retryDelay(2) = %s, want 2ns", got)
	}
}
