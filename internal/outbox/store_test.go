package outbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNewPostgresStoreValidatesConstruction(t *testing.T) {
	tests := []struct {
		name         string
		pool         *pgxpool.Pool
		queryTimeout time.Duration
	}{
		{name: "missing pool", queryTimeout: time.Second},
		{name: "non-positive query timeout", pool: new(pgxpool.Pool)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewPostgresStore(tt.pool, tt.queryTimeout)
			if err == nil {
				t.Fatal("NewPostgresStore returned nil error")
			}
			if store != nil {
				t.Fatalf("NewPostgresStore store = %#v, want nil", store)
			}
		})
	}
}

func TestMapPostgresErrorClassifiesFailures(t *testing.T) {
	const sensitiveMessage = "connection failed with password super-secret"

	tests := []struct {
		name string
		err  error
		want error
	}{
		{
			name: "availability SQLSTATE",
			err: &pgconn.PgError{
				Code:    postgresSQLStateAdminShutdown,
				Message: sensitiveMessage,
			},
			want: errStoreUnavailable,
		},
		{
			name: "schema SQLSTATE",
			err: &pgconn.PgError{
				Code:    "42P01",
				Message: sensitiveMessage,
			},
			want: errStoreInternal,
		},
		{
			name: "permission SQLSTATE",
			err: &pgconn.PgError{
				Code:    "42501",
				Message: sensitiveMessage,
			},
			want: errStoreInternal,
		},
		{
			name: "network operation",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: errors.New(sensitiveMessage),
			},
			want: errStoreUnavailable,
		},
		{
			name: "unknown error",
			err:  errors.New(sensitiveMessage),
			want: errStoreInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mapPostgresError(context.Background(), tt.err)
			if !errors.Is(err, tt.want) {
				t.Fatalf("mapPostgresError() = %v, want %v", err, tt.want)
			}
			if strings.Contains(err.Error(), sensitiveMessage) {
				t.Fatal("mapPostgresError exposed raw dependency error details")
			}
		})
	}
}

func TestMapPostgresErrorPreservesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := mapPostgresError(ctx, fmt.Errorf("query failed: %w", context.Canceled))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mapPostgresError() = %v, want context.Canceled", err)
	}
	if errors.Is(err, errStoreUnavailable) || errors.Is(err, errStoreInternal) {
		t.Fatalf("mapPostgresError() = %v, did not want store classification", err)
	}
}

func TestMapPostgresErrorDoesNotOverrideConcreteFailureWithLateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := mapPostgresError(ctx, pgconn.ErrConnClosed)
	if !errors.Is(err, errStoreUnavailable) {
		t.Fatalf("mapPostgresError() = %v, want errStoreUnavailable", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("mapPostgresError() = %v, did not want late caller cancellation", err)
	}
}

func TestMapPostgresErrorMapsStoreQueryTimeoutToUnavailable(t *testing.T) {
	queryCtx, cancel := context.WithTimeoutCause(
		context.Background(),
		time.Nanosecond,
		errPostgresQueryTimeout,
	)
	defer cancel()
	<-queryCtx.Done()

	err := mapPostgresError(queryCtx, queryCtx.Err())
	if !errors.Is(err, errStoreUnavailable) {
		t.Fatalf("mapPostgresError() = %v, want errStoreUnavailable", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("mapPostgresError() = %v, did not want caller deadline", err)
	}
}
