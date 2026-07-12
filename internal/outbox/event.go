// Package outbox contains transactional outbox worker primitives.
package outbox

import (
	"encoding/json"
	"time"
)

const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusPublished  = "published"
	StatusDeadLetter = "dead_letter"
)

type Event struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       json.RawMessage
	Status        string
	Attempts      int
	AvailableAt   time.Time
	CreatedAt     time.Time
}
