package main

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestNewWorkerID(t *testing.T) {
	const serviceName = "game-rewards-service"
	const prefix = serviceName + "-worker-"

	firstID, err := newWorkerID(serviceName)
	if err != nil {
		t.Fatalf("newWorkerID returned error: %v", err)
	}

	secondID, err := newWorkerID(serviceName)
	if err != nil {
		t.Fatalf("newWorkerID returned error: %v", err)
	}

	if firstID == secondID {
		t.Fatalf(
			"expected unique worker IDs, both were %q",
			firstID,
		)
	}

	for _, workerID := range []string{firstID, secondID} {
		if !strings.HasPrefix(workerID, prefix) {
			t.Fatalf(
				"worker ID %q does not have prefix %q",
				workerID,
				prefix,
			)
		}

		if len(workerID) > maxWorkerIDLength {
			t.Fatalf(
				"worker ID length = %d, maximum = %d",
				len(workerID),
				maxWorkerIDLength,
			)
		}

		randomPart := strings.TrimPrefix(workerID, prefix)

		decoded, err := hex.DecodeString(randomPart)
		if err != nil {
			t.Fatalf(
				"worker ID random component %q is not hexadecimal: %v",
				randomPart,
				err,
			)
		}

		if len(decoded) != workerInstanceIDBytes {
			t.Fatalf(
				"random component length = %d bytes, want %d",
				len(decoded),
				workerInstanceIDBytes,
			)
		}
	}
}

func TestNewWorkerIDRejectsIdentifierThatIsTooLong(t *testing.T) {
	serviceName := strings.Repeat("s", maxWorkerIDLength)

	workerID, err := newWorkerID(serviceName)
	if err == nil {
		t.Fatalf(
			"expected error, got worker ID %q",
			workerID,
		)
	}
}
