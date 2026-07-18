package main

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestStopComponentsGracefullyStopsBothComponents(t *testing.T) {
	var ready atomic.Bool
	ready.Store(true)
	var canceled atomic.Bool

	results := make(chan componentResult, 2)
	results <- componentResult{name: "worker"}
	results <- componentResult{name: "admin_server"}

	err := stopComponents(
		time.Second,
		&http.Server{},
		&ready,
		func() { canceled.Store(true) },
		results,
		2,
	)
	if err != nil {
		t.Fatalf("stopComponents returned error: %v", err)
	}
	if ready.Load() {
		t.Fatal("worker readiness remained enabled")
	}
	if !canceled.Load() {
		t.Fatal("worker context was not canceled")
	}
}

func TestStopComponentsShutsDownRunningAdminServerAndCancelsWorker(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	adminServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	results := make(chan componentResult, 2)
	go func() {
		err := adminServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		results <- componentResult{name: "admin_server", err: err}
	}()

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	go func() {
		<-workerCtx.Done()
		results <- componentResult{name: "worker"}
	}()

	var ready atomic.Bool
	ready.Store(true)

	if err := stopComponents(time.Second, adminServer, &ready, cancelWorker, results, 2); err != nil {
		t.Fatalf("stopComponents returned error: %v", err)
	}
	if ready.Load() {
		t.Fatal("worker readiness remained enabled")
	}
	if !errors.Is(workerCtx.Err(), context.Canceled) {
		t.Fatalf("worker context error = %v, want context canceled", workerCtx.Err())
	}
}

func TestStopComponentsReturnsComponentError(t *testing.T) {
	componentErr := errors.New("worker failed")
	results := make(chan componentResult, 1)
	results <- componentResult{name: "worker", err: componentErr}

	err := stopComponents(
		time.Second,
		&http.Server{},
		&atomic.Bool{},
		func() {},
		results,
		1,
	)
	if !errors.Is(err, componentErr) {
		t.Fatalf("stopComponents error = %v, want component error", err)
	}
}

func TestStopComponentsTimesOutWaitingForComponent(t *testing.T) {
	err := stopComponents(
		10*time.Millisecond,
		&http.Server{},
		&atomic.Bool{},
		func() {},
		make(chan componentResult),
		1,
	)
	if err == nil {
		t.Fatal("expected shutdown timeout")
	}
	if !strings.Contains(err.Error(), "shutdown timed out") {
		t.Fatalf("stopComponents error = %v, want shutdown timeout", err)
	}
}
