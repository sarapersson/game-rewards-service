package rewards

import (
	"context"
	"errors"
	"testing"

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
}

func TestMapPostgresErrorUniqueViolationForDifferentConstraint(t *testing.T) {
	err := mapPostgresError(&pgconn.PgError{
		Code:           "23505",
		ConstraintName: "some_other_constraint",
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
}

func TestMapPostgresErrorContextDeadlineExceeded(t *testing.T) {
	err := mapPostgresError(context.DeadlineExceeded)

	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mapPostgresError() = %v, want ErrUnavailable", err)
	}
}
