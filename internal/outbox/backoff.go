package outbox

import (
	"fmt"
	"time"
)

type BackoffPolicy struct {
	Base time.Duration
	Max  time.Duration
}

func (p BackoffPolicy) Duration(attempts int) (time.Duration, error) {
	if p.Base <= 0 {
		return 0, fmt.Errorf("base backoff must be greater than zero")
	}

	if p.Max <= 0 {
		return 0, fmt.Errorf("max backoff must be greater than zero")
	}

	if p.Max < p.Base {
		return 0, fmt.Errorf("max backoff must be greater than or equal to base backoff")
	}

	if attempts <= 0 {
		return p.Base, nil
	}

	backoff := p.Base
	for range attempts {
		if backoff > p.Max-backoff {
			return p.Max, nil
		}

		backoff *= 2
	}

	return backoff, nil
}
