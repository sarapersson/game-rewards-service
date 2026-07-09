CREATE UNIQUE INDEX outbox_events_aggregate_event_type_unique
    ON outbox_events (aggregate_type, aggregate_id, event_type);
