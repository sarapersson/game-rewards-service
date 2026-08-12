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
	"github.com/jackc/pgx/v5/pgxpool"
)

type rollbackTestTx struct {
	pgx.Tx
	rollbackFn func(context.Context) error
}

func (tx *rollbackTestTx) Rollback(ctx context.Context) error {
	return tx.rollbackFn(ctx)
}

func TestPostgresStoreRollbackTransactionIsBounded(t *testing.T) {
	const timeout = 20 * time.Millisecond

	type rollbackObservation struct {
		hasDeadline bool
		err         error
	}

	observation := make(chan rollbackObservation, 1)
	tx := &rollbackTestTx{
		rollbackFn: func(ctx context.Context) error {
			_, hasDeadline := ctx.Deadline()
			<-ctx.Done()

			observation <- rollbackObservation{
				hasDeadline: hasDeadline,
				err:         ctx.Err(),
			}
			return ctx.Err()
		},
	}

	store := &PostgresStore{queryTimeout: timeout}
	done := make(chan struct{})
	go func() {
		store.rollbackTransaction(tx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("rollback did not finish within the bounded test window")
	}

	got := <-observation
	if !got.hasDeadline {
		t.Fatal("rollback context did not have a deadline")
	}
	if !errors.Is(got.err, context.DeadlineExceeded) {
		t.Fatalf("rollback context error = %v, want deadline exceeded", got.err)
	}
}

func TestMapPostgresErrorDuplicateClaim(t *testing.T) {
	err := mapPostgresError(context.Background(), &pgconn.PgError{
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

	err := mapPostgresError(context.Background(), &pgconn.PgError{
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
			name: "driver deadline exceeded",
			err:  context.DeadlineExceeded,
		},
		{
			name: "wrapped driver deadline exceeded",
			err:  fmt.Errorf("query failed: %w", context.DeadlineExceeded),
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
			err := mapPostgresError(context.Background(), tt.err)

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

func TestMapPostgresErrorPreservesCallerContext(t *testing.T) {
	canceledCtx, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()

	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancelDeadline()

	tests := []struct {
		name   string
		parent context.Context
		want   error
	}{
		{name: "caller canceled", parent: canceledCtx, want: context.Canceled},
		{name: "caller deadline exceeded", parent: deadlineCtx, want: context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queryCtx, cancelQuery := context.WithTimeoutCause(tt.parent, time.Minute, errPostgresQueryTimeout)
			defer cancelQuery()
			<-queryCtx.Done()

			err := mapPostgresError(queryCtx, fmt.Errorf("query failed: %w", queryCtx.Err()))
			if !errors.Is(err, tt.want) {
				t.Fatalf("mapPostgresError() = %v, want %v", err, tt.want)
			}
			if errors.Is(err, ErrUnavailable) {
				t.Fatalf("mapPostgresError() = %v, did not want ErrUnavailable", err)
			}
			if errors.Is(err, ErrInternal) {
				t.Fatalf("mapPostgresError() = %v, did not want ErrInternal", err)
			}
		})
	}
}

func TestMapPostgresErrorDoesNotOverrideConcreteFailureWithLateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := mapPostgresError(ctx, pgconn.ErrConnClosed)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mapPostgresError() = %v, want ErrUnavailable", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("mapPostgresError() = %v, did not want late caller cancellation", err)
	}
}

func TestMapPostgresErrorMapsStoreQueryTimeoutToUnavailable(t *testing.T) {
	store := NewPostgresStore(new(pgxpool.Pool), time.Nanosecond)
	queryCtx, cancel, err := store.queryContext(context.Background())
	if err != nil {
		t.Fatalf("queryContext() error = %v", err)
	}
	defer cancel()
	<-queryCtx.Done()

	err = mapPostgresError(queryCtx, queryCtx.Err())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mapPostgresError() = %v, want ErrUnavailable", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("mapPostgresError() = %v, did not want caller deadline", err)
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
			err := mapPostgresError(context.Background(), fmt.Errorf("wrapped postgres error: %w", pgErr))

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
			name: "context canceled without canceled query context",
			err:  context.Canceled,
		},
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
			err := mapPostgresError(context.Background(), tt.err)

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

	err := mapPostgresError(context.Background(), &net.OpError{
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
