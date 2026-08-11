package rewards

import "errors"

var (
	ErrDuplicateClaim        = errors.New("reward already claimed")
	ErrUnavailable           = errors.New("reward claims store unavailable")
	ErrInternal              = errors.New("reward claims store internal error")
	ErrIdempotencyKeyReused  = errors.New("idempotency key reused with different request payload")
	ErrIdempotencyInProgress = errors.New("idempotency key is still processing")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

func IsValidationError(err error) bool {
	var value ValidationError
	if errors.As(err, &value) {
		return true
	}

	var pointer *ValidationError
	return errors.As(err, &pointer)
}
