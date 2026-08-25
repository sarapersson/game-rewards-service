package rewards

import "errors"

var (
	ErrUnavailable          = errors.New("reward claims unavailable")
	ErrInternal             = errors.New("reward claims internal error")
	ErrIdempotencyKeyReused = errors.New("idempotency key reused with different request payload")
)

type InvalidInputError struct {
	Message string
}

func (e *InvalidInputError) Error() string {
	return e.Message
}
