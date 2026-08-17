package rewards

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type rollbackTestTx struct {
	pgx.Tx
	rollbackFn func(context.Context) error
}

func (tx *rollbackTestTx) Rollback(ctx context.Context) error {
	return tx.rollbackFn(ctx)
}

func TestServiceRollbackTransactionIsBounded(t *testing.T) {
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

	service := &Service{queryTimeout: timeout}
	done := make(chan struct{})
	go func() {
		service.rollbackTransaction(tx)
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

func TestMapPostgresErrorUniqueViolationIsInternal(t *testing.T) {
	const (
		uniqueViolationSQLState = "23505"
		sensitiveMessage        = "connection failed with password super-secret"
	)

	for _, constraint := range []string{
		"reward_claims_player_campaign_reward_uniq",
		"some_other_constraint",
	} {
		t.Run(constraint, func(t *testing.T) {
			err := mapPostgresError(context.Background(), &pgconn.PgError{
				Code:           uniqueViolationSQLState,
				ConstraintName: constraint,
				Message:        sensitiveMessage,
			})

			if !errors.Is(err, ErrInternal) {
				t.Fatalf("mapPostgresError() = %v, want ErrInternal", err)
			}
			if errors.Is(err, ErrUnavailable) {
				t.Fatalf("mapPostgresError() = %v, did not want ErrUnavailable", err)
			}
			if strings.Contains(err.Error(), sensitiveMessage) {
				t.Fatal("mapPostgresError() exposed raw PostgreSQL error details")
			}
		})
	}
}

func TestMapPostgresErrorMapsSharedAvailabilityToUnavailable(t *testing.T) {
	const sensitiveMessage = "internal-db.example:5432 super-secret"

	err := mapPostgresError(context.Background(), &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: errors.New(sensitiveMessage),
	})

	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mapPostgresError() = %v, want ErrUnavailable", err)
	}
	if errors.Is(err, ErrInternal) {
		t.Fatalf("mapPostgresError() = %v, did not want ErrInternal", err)
	}
	if strings.Contains(err.Error(), sensitiveMessage) {
		t.Fatal("mapPostgresError() exposed raw dependency error details")
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

func TestMapPostgresErrorMapsServiceQueryTimeoutToUnavailable(t *testing.T) {
	service := &Service{queryTimeout: time.Nanosecond}
	queryCtx, cancel := service.queryContext(context.Background())
	defer cancel()
	<-queryCtx.Done()

	err := mapPostgresError(queryCtx, queryCtx.Err())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mapPostgresError() = %v, want ErrUnavailable", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("mapPostgresError() = %v, did not want caller deadline", err)
	}
}

func TestMapPostgresErrorMapsUnexpectedFailuresToInternal(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "no rows", err: pgx.ErrNoRows},
		{name: "generic error", err: errors.New("unexpected database failure")},
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
		})
	}
}
