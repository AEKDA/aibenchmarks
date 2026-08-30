-- Initial schema: subscribers, published events and the delivery outbox.

CREATE TABLE IF NOT EXISTS subscriptions (
    id         uuid PRIMARY KEY,
    url        text        NOT NULL,
    secret     text        NOT NULL,
    events     text[]      NOT NULL,
    max_rps    integer     NOT NULL CHECK (max_rps > 0),
    active     boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL
);

-- Fan-out looks subscriptions up by the event types they listen to.
CREATE INDEX IF NOT EXISTS subscriptions_events_idx ON subscriptions USING gin (events);

CREATE TABLE IF NOT EXISTS events (
    id              uuid PRIMARY KEY,
    idempotency_key text        NOT NULL,
    type            text        NOT NULL,
    payload         jsonb       NOT NULL,
    created_at      timestamptz NOT NULL
);

-- The single arbiter of idempotency: a repeated key can never insert twice.
CREATE UNIQUE INDEX IF NOT EXISTS events_idempotency_key_idx ON events (idempotency_key);

CREATE TABLE IF NOT EXISTS deliveries (
    id               uuid PRIMARY KEY,
    event_id         uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    subscription_id  uuid        NOT NULL REFERENCES subscriptions (id) ON DELETE CASCADE,
    status           text        NOT NULL,
    attempt_count    integer     NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz,
    last_status_code integer,
    last_error       text        NOT NULL DEFAULT '',
    locked_at        timestamptz,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL
);

-- Workers poll for PENDING rows and for RETRYING rows that have come due.
CREATE INDEX IF NOT EXISTS deliveries_ready_idx ON deliveries (status, next_attempt_at);
