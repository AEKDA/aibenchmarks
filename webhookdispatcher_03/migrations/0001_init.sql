-- Инициализация схемы webhook-диспетчера: подписки, события с идемпотентностью,
-- доставки с индексом для конкурентного забора задач.

CREATE TABLE IF NOT EXISTS subscriptions (
    id        uuid PRIMARY KEY,
    url       text NOT NULL,
    secret    text NOT NULL,
    events    text[] NOT NULL,
    max_rps   int  NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS events (
    id          uuid PRIMARY KEY,
    type        text NOT NULL,
    payload     bytea NOT NULL,
    created_at  timestamptz NOT NULL,
    idem_key    text NOT NULL
);

-- Уникальность идемпотентного ключа гарантирует атомарность
-- "событие уже записано" независимо от гонок.
CREATE UNIQUE INDEX IF NOT EXISTS events_idem_key_idx ON events(idem_key);

CREATE TABLE IF NOT EXISTS deliveries (
    id                uuid PRIMARY KEY,
    event_id          uuid NOT NULL REFERENCES events(id),
    subscription_id   uuid NOT NULL REFERENCES subscriptions(id),
    status            text NOT NULL,
    attempt           int  NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz,
    payload           bytea NOT NULL,
    last_http_status  int
);

-- Индекс под забор задач: кандидаты на доставку — PENDING и готовые RETRYING.
CREATE INDEX IF NOT EXISTS deliveries_claim_idx
    ON deliveries (status, next_attempt_at)
    WHERE status IN ('PENDING','RETRYING');
