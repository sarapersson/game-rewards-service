package outbox

import (
	"fmt"
	"time"
)

type backoffPolicy struct {
	base time.Duration
	max  time.Duration
}

func newBackoffPolicy(base, max time.Duration) (backoffPolicy, error) {
	if base <= 0 {
		return backoffPolicy{}, fmt.Errorf("base backoff must be greater than zero")
	}

	if max <= 0 {
		return backoffPolicy{}, fmt.Errorf("max backoff must be greater than zero")
	}

	if max < base {
		return backoffPolicy{}, fmt.Errorf("max backoff must be greater than or equal to base backoff")
	}

	return backoffPolicy{base: base, max: max}, nil
}

func (p backoffPolicy) retryDelay(failedAttempts int) time.Duration {
	if failedAttempts <= 1 {
		return p.base
	}

	delay := p.base
	for remaining := failedAttempts - 1; remaining > 0; remaining-- {
		if delay > p.max-delay {
			return p.max
		}

		delay *= 2
	}

	return delay
}
