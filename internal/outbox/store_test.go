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
			name: "shared availability failure",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: errors.New(sensitiveMessage),
			},
			want: errStoreUnavailable,
		},
		{
			name: "schema failure",
			err: &pgconn.PgError{
				Code:    "42P01",
				Message: sensitiveMessage,
			},
			want: errStoreInternal,
		},
		{
			name: "unknown failure",
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
	canceledCtx, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()

	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancelDeadline()

	tests := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "caller canceled", ctx: canceledCtx, want: context.Canceled},
		{name: "caller deadline exceeded", ctx: deadlineCtx, want: context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queryCtx, cancelQuery := context.WithTimeoutCause(tt.ctx, time.Minute, errPostgresQueryTimeout)
			defer cancelQuery()
			<-queryCtx.Done()

			err := mapPostgresError(queryCtx, fmt.Errorf("query failed: %w", queryCtx.Err()))
			if !errors.Is(err, tt.want) {
				t.Fatalf("mapPostgresError() = %v, want %v", err, tt.want)
			}
			if errors.Is(err, errStoreUnavailable) || errors.Is(err, errStoreInternal) {
				t.Fatalf("mapPostgresError() = %v, did not want store classification", err)
			}
		})
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
