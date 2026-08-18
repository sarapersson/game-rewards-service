// Package outbox contains transactional outbox worker primitives.
package outbox

import "encoding/json"

type Event struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       json.RawMessage

	// FailedAttempts is the number of publisher failures already persisted for this event.
	FailedAttempts int
}
