package rewards

import (
	"fmt"
	"testing"
)

func TestIsValidationError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "value",
			err:  ValidationError{Field: "player_id", Message: "is required"},
			want: true,
		},
		{
			name: "pointer",
			err:  &ValidationError{Field: "player_id", Message: "is required"},
			want: true,
		},
		{
			name: "wrapped value",
			err:  fmt.Errorf("validate request: %w", ValidationError{Field: "player_id", Message: "is required"}),
			want: true,
		},
		{
			name: "wrapped pointer",
			err:  fmt.Errorf("validate request: %w", &ValidationError{Field: "player_id", Message: "is required"}),
			want: true,
		},
		{
			name: "different error",
			err:  ErrInternal,
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidationError(tt.err)
			if got != tt.want {
				t.Fatalf("IsValidationError() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestValidationErrorError(t *testing.T) {
	tests := []struct {
		name string
		err  ValidationError
		want string
	}{
		{
			name: "field and message",
			err:  ValidationError{Field: "player_id", Message: "is required"},
			want: "player_id: is required",
		},
		{
			name: "message only",
			err:  ValidationError{Message: "invalid request"},
			want: "invalid request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}
