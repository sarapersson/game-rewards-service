package outbox

import (
	"context"
	"fmt"
	"log/slog"
)

// Publisher sends an outbox event to a downstream integration.
//
// Implementations must:
//   - honor context cancellation and deadlines;
//   - return promptly after ctx is canceled;
//   - support at-least-once delivery semantics;
//   - expect downstream consumers to deduplicate by Event.ID;
//   - avoid logging the event payload or other sensitive metadata.
type Publisher interface {
	Publish(ctx context.Context, event Event) error
}

// LoggingPublisher is a local development publisher that records a simulated
// successful publish without sending the event to an external system.
type LoggingPublisher struct {
	logger *slog.Logger
}

func NewLoggingPublisher(logger *slog.Logger) (*LoggingPublisher, error) {
	if logger == nil {
		return nil, fmt.Errorf("logger must not be nil")
	}

	return &LoggingPublisher{logger: logger}, nil
}

func (p *LoggingPublisher) Publish(ctx context.Context, event Event) error {
	if p == nil || p.logger == nil {
		return fmt.Errorf("logging publisher is not initialized")
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	p.logger.InfoContext(
		ctx,
		"outbox_event_publish_simulated",
		slog.String("event_id", event.ID),
		slog.String("event_type", event.EventType),
		slog.String("aggregate_type", event.AggregateType),
		slog.String("aggregate_id", event.AggregateID),
		slog.Int("attempt_number", event.Attempts+1),
		slog.Int("failed_attempts", event.Attempts),
	)

	return nil
}
