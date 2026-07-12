package rewards

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapPostgresErrorDuplicateClaim(t *testing.T) {
	err := mapPostgresError(&pgconn.PgError{
		Code:           "23505",
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
		Code:           "23505",
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
			name: "different postgres error",
			err: &pgconn.PgError{
				Code:    "42P01",
				Message: "relation does not exist",
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
