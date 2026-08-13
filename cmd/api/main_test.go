package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRunStopsBeforeStartupWhenContextAlreadyCanceled(t *testing.T) {
	// If run reaches config loading, this invalid override makes the test fail.
	t.Setenv("APP_ENV", " ")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if exitCode := run(ctx); exitCode != 0 {
		t.Fatalf("run exit code = %d, want 0", exitCode)
	}
}

func TestStopHTTPServerGracefullyStopsRunningServer(t *testing.T) {
	server, serveResult, _ := startTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	if err := stopHTTPServer(time.Second, server, serveResult); err != nil {
		t.Fatalf("stopHTTPServer returned error: %v", err)
	}
}

func TestStopHTTPServerForceClosesAfterGracefulTimeout(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})

	server, serveResult, baseURL := startTestHTTPServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
		close(requestCanceled)
	}))

	client := &http.Client{Timeout: time.Second}
	requestDone := make(chan error, 1)
	go func() {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, baseURL, nil)
		if err != nil {
			requestDone <- err
			return
		}

		response, err := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request handler did not start")
	}

	err := stopHTTPServer(20*time.Millisecond, server, serveResult)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stopHTTPServer error = %v, want deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "graceful http server shutdown") {
		t.Fatalf("stopHTTPServer error = %v, want graceful shutdown context", err)
	}

	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("force close did not cancel the active request")
	}

	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("client request did not finish after force close")
	}
}

func startTestHTTPServer(
	t *testing.T,
	handler http.Handler,
) (*http.Server, <-chan error, string) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := &http.Server{Handler: handler}
	serveResult := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveResult <- err
	}()

	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})

	return server, serveResult, "http://" + listener.Addr().String()
}
