package rewards

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapPostgresErrorDuplicateClaim(t *testing.T) {
	err := mapPostgresError(&pgconn.PgError{
		Code:           postgresSQLStateUniqueViolation,
		ConstraintName: rewardClaimsPlayerCampaignRewardConstraint,
	})

	if !errors.Is(err, ErrDuplicateClaim) {
		t.Fatalf("mapPostgresError() = %v, want ErrDuplicateClaim", err)
	}

	if errors.Is(err, ErrInternal) {
		t.Fatalf("mapPostgresError() = %v, did not want ErrInternal", err)
	}

	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("mapPostgresError() = %v, did not want ErrUnavailable", err)
	}
}

func TestMapPostgresErrorUniqueViolationForDifferentConstraint(t *testing.T) {
	const sensitiveMessage = "connection failed with password super-secret"

	err := mapPostgresError(&pgconn.PgError{
		Code:           postgresSQLStateUniqueViolation,
		ConstraintName: "some_other_constraint",
		Message:        sensitiveMessage,
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("mapPostgresError() = %v, want ErrInternal", err)
	}

	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("mapPostgresError() = %v, did not want ErrUnavailable", err)
	}

	if errors.Is(err, ErrDuplicateClaim) {
		t.Fatalf("mapPostgresError() = %v, did not want ErrDuplicateClaim", err)
	}

	if strings.Contains(err.Error(), sensitiveMessage) {
		t.Fatal("mapPostgresError() exposed raw PostgreSQL error details")
	}
}

func TestMapPostgresErrorUnavailable(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "deadline exceeded",
			err:  context.DeadlineExceeded,
		},
		{
			name: "wrapped deadline exceeded",
			err:  fmt.Errorf("query failed: %w", context.DeadlineExceeded),
		},
		{
			name: "context canceled",
			err:  context.Canceled,
		},
		{
			name: "connection closed",
			err:  net.ErrClosed,
		},
		{
			name: "pgx connection closed",
			err:  fmt.Errorf("wrapped: %w", pgconn.ErrConnClosed),
		},
		{
			name: "eof",
			err:  io.EOF,
		},
		{
			name: "unexpected eof",
			err:  io.ErrUnexpectedEOF,
		},
		{
			name: "network timeout",
			err:  &net.DNSError{IsTimeout: true},
		},
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
			err := mapPostgresError(tt.err)

			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("mapPostgresError() = %v, want ErrUnavailable", err)
			}

			if errors.Is(err, ErrInternal) {
				t.Fatalf("mapPostgresError() = %v, did not want ErrInternal", err)
			}

			if errors.Is(err, ErrDuplicateClaim) {
				t.Fatalf("mapPostgresError() = %v, did not want ErrDuplicateClaim", err)
			}
		})
	}
}

func TestMapPostgresErrorUnavailableSQLStates(t *testing.T) {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgErr := &pgconn.PgError{Code: tt.code, Message: "sensitive postgres detail"}
			err := mapPostgresError(fmt.Errorf("wrapped postgres error: %w", pgErr))

			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("mapPostgresError() = %v, want ErrUnavailable", err)
			}

			if errors.Is(err, ErrInternal) {
				t.Fatalf("mapPostgresError() = %v, did not want ErrInternal", err)
			}

			if strings.Contains(err.Error(), "sensitive postgres detail") {
				t.Fatal("mapPostgresError() exposed raw PostgreSQL error details")
			}
		})
	}
}

func TestMapPostgresErrorInternal(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "no rows",
			err:  pgx.ErrNoRows,
		},
		{
			name: "generic error",
			err:  errors.New("unexpected database failure"),
		},
		{
			name: "undefined table",
			err: &pgconn.PgError{
				Code:    "42P01",
				Message: "relation does not exist",
			},
		},
		{
			name: "invalid password",
			err: &pgconn.PgError{
				Code:    "28P01",
				Message: "password authentication failed",
			},
		},
		{
			name: "protocol violation",
			err: &pgconn.PgError{
				Code:    "08P01",
				Message: "protocol violation",
			},
		},
		{
			name: "serialization failure",
			err: &pgconn.PgError{
				Code:    "40001",
				Message: "serialization failure",
			},
		},
		{
			name: "deadlock detected",
			err: &pgconn.PgError{
				Code:    "40P01",
				Message: "deadlock detected",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mapPostgresError(tt.err)

			if !errors.Is(err, ErrInternal) {
				t.Fatalf("mapPostgresError() = %v, want ErrInternal", err)
			}

			if errors.Is(err, ErrUnavailable) {
				t.Fatalf("mapPostgresError() = %v, did not want ErrUnavailable", err)
			}

			if errors.Is(err, ErrDuplicateClaim) {
				t.Fatalf("mapPostgresError() = %v, did not want ErrDuplicateClaim", err)
			}
		})
	}
}

func TestMapPostgresErrorSanitizesNetworkDetails(t *testing.T) {
	const sensitiveMessage = "internal-db.example:5432 super-secret"

	err := mapPostgresError(&net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: errors.New(sensitiveMessage),
	})

	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mapPostgresError() = %v, want ErrUnavailable", err)
	}

	if strings.Contains(err.Error(), sensitiveMessage) {
		t.Fatal("mapPostgresError() exposed raw network error details")
	}
}

func TestPostgresStoreQueryContextRejectsUnavailableStore(t *testing.T) {
	tests := []struct {
		name  string
		store *PostgresStore
	}{
		{
			name:  "nil store",
			store: nil,
		},
		{
			name: "nil pool",
			store: &PostgresStore{
				queryTimeout: time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel, err := tt.store.queryContext(context.Background())
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("queryContext() error = %v, want ErrUnavailable", err)
			}

			if ctx != nil {
				t.Fatal("queryContext() returned non-nil context")
			}

			if cancel != nil {
				t.Fatal("queryContext() returned non-nil cancel function")
			}
		})
	}
}
