CREATE SCHEMA IF NOT EXISTS events;

CREATE TABLE IF NOT EXISTS events.processed (
    event_id VARCHAR(100) NOT NULL,
    consumer_name VARCHAR(150) NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- NIAGA-357: NULL while a claim is in flight, set once the handler succeeded.
    -- infra-database owns the live schema; this copy is kept in step with it.
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (event_id, consumer_name)
);

CREATE INDEX IF NOT EXISTS idx_events_processed_consumer_processed_at
    ON events.processed (consumer_name, processed_at DESC);

CREATE TABLE IF NOT EXISTS events.failed (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id VARCHAR(100),
    consumer_name VARCHAR(150) NOT NULL,
    subject VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,
    headers JSONB,
    error TEXT NOT NULL,
    stream VARCHAR(150),
    consumer VARCHAR(150),
    stream_sequence BIGINT,
    consumer_sequence BIGINT,
    delivered_count BIGINT,
    failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_events_failed_consumer_failed_at
    ON events.failed (consumer_name, failed_at DESC);

CREATE INDEX IF NOT EXISTS idx_events_failed_event_id
    ON events.failed (event_id);
