package rewards

import (
	"errors"
	"fmt"
	"testing"
)

func TestInvalidInputError(t *testing.T) {
	err := &InvalidInputError{Message: "player_id is required"}

	if got := err.Error(); got != "player_id is required" {
		t.Fatalf("Error() = %q, want %q", got, "player_id is required")
	}
}

func TestInvalidInputErrorCanBeExtractedWhenWrapped(t *testing.T) {
	err := fmt.Errorf("validate request: %w", &InvalidInputError{Message: "player_id is required"})

	var invalidInputErr *InvalidInputError
	if !errors.As(err, &invalidInputErr) {
		t.Fatalf("wrapped error = %v, want *InvalidInputError", err)
	}
	if invalidInputErr.Message != "player_id is required" {
		t.Fatalf("InvalidInputError.Message = %q, want %q", invalidInputErr.Message, "player_id is required")
	}
}
