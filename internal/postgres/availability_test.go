package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsUnavailableSQLStates(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{name: "connection exception", code: postgresSQLStateConnectionException},
		{name: "connection does not exist", code: postgresSQLStateConnectionDoesNotExist},
		{name: "connection failure", code: postgresSQLStateConnectionFailure},
		{name: "unable to establish connection", code: postgresSQLStateUnableToEstablishConnection},
		{name: "server rejected connection", code: postgresSQLStateServerRejectedConnection},
		{name: "transaction resolution unknown", code: postgresSQLStateTransactionResolutionUnknown},
		{name: "too many connections", code: postgresSQLStateTooManyConnections},
		{name: "admin shutdown", code: postgresSQLStateAdminShutdown},
		{name: "crash shutdown", code: postgresSQLStateCrashShutdown},
		{name: "cannot connect now", code: postgresSQLStateCannotConnectNow},
		{name: "idle session timeout", code: postgresSQLStateIdleSessionTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("wrapped postgres error: %w", &pgconn.PgError{Code: tt.code})
			if !IsUnavailable(err) {
				t.Fatalf("IsUnavailable() = false for SQLSTATE %s", tt.code)
			}
		})
	}
}

func TestIsUnavailableTransportFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "connection closed", err: net.ErrClosed},
		{name: "pgx connection closed", err: fmt.Errorf("wrapped: %w", pgconn.ErrConnClosed)},
		{name: "eof", err: io.EOF},
		{name: "unexpected eof", err: io.ErrUnexpectedEOF},
		{name: "network timeout", err: &net.DNSError{IsTimeout: true}},
		{name: "dns resolution", err: fmt.Errorf("resolve postgres: %w", &net.DNSError{
			Err:        "no such host",
			Name:       "postgres",
			Server:     "127.0.0.11:53",
			IsNotFound: true,
		})},
		{
			name: "network operation",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: errors.New("connection refused"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !IsUnavailable(tt.err) {
				t.Fatalf("IsUnavailable(%T) = false", tt.err)
			}
		})
	}
}

func TestIsUnavailableRejectsNonAvailabilityFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "raw context cancellation", err: context.Canceled},
		{name: "wrapped context cancellation", err: fmt.Errorf("query failed: %w", context.Canceled)},
		{name: "raw context deadline", err: context.DeadlineExceeded},
		{name: "wrapped context deadline", err: fmt.Errorf("query failed: %w", context.DeadlineExceeded)},
		{name: "dns resolution wrapping context cancellation", err: &net.DNSError{
			UnwrapErr: context.Canceled,
			Err:       context.Canceled.Error(),
			Name:      "postgres",
		}},
		{name: "dns resolution wrapping context deadline", err: &net.DNSError{
			UnwrapErr: context.DeadlineExceeded,
			Err:       context.DeadlineExceeded.Error(),
			Name:      "postgres",
		}},
		{
			name: "network operation wrapping context deadline",
			err: &net.OpError{
				Op:  "read",
				Net: "tcp",
				Err: context.DeadlineExceeded,
			},
		},
		{name: "generic error", err: errors.New("unexpected database failure")},
		{name: "undefined table", err: &pgconn.PgError{Code: "42P01"}},
		{name: "invalid password", err: &pgconn.PgError{Code: "28P01"}},
		{name: "protocol violation", err: &pgconn.PgError{Code: "08P01"}},
		{name: "database dropped", err: &pgconn.PgError{Code: "57P04"}},
		{name: "serialization failure", err: &pgconn.PgError{Code: "40001"}},
		{name: "deadlock detected", err: &pgconn.PgError{Code: "40P01"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if IsUnavailable(tt.err) {
				t.Fatalf("IsUnavailable(%T) = true", tt.err)
			}
		})
	}
}
