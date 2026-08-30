# webhookdispatcher

A webhook delivery service in Go. It accepts events from internal systems over a
REST API, stores them idempotently, and delivers them to subscribers with
retries, HMAC-SHA256 payload signatures and per-host rate limiting.

## Architecture

The project follows strict hexagonal architecture. Dependencies point inwards:

```
cmd/webhookdispatcher/        composition root: config, wiring, signal handling
internal/application/         the domain; imports only the stdlib and google/uuid
  entity/                     Subscription, Event, Delivery, state machine, backoff
  errs/                       typed domain errors (ErrNotFound, ErrConflict, ...)
  ports/                      driver ports (use cases) and driven ports (adapters)
  usecase/                    CreateSubscription, PublishEvent, GetDelivery,
                              ProcessDelivery, ClaimDeliveries, ReleaseStaleDeliveries
  instruction/                shared steps: SignPayload, ScheduleRetry
internal/adapter/driver/      inbound: http (REST API), worker (delivery pool)
internal/adapter/driven/      outbound: postgres, httpsender, ratelimiter, clock
internal/architecture/        guard tests that keep the layering honest
```

Use cases and instructions are named after the action they perform and are
called through `Invoke(ctx, ...)`. Every port method takes `ctx context.Context`
as its first argument.

## API

### `POST /api/v1/subscriptions`

Registers a subscriber.

```bash
curl -X POST localhost:8080/api/v1/subscriptions \
  -d '{"url":"https://example.com/hook","secret":"s3cr3t","events":["order.created"],"max_rps":10}'
```

`201 Created` returns the subscription without its secret. Invalid input
(non-HTTP url, empty secret, empty event list, `max_rps <= 0`) returns `400`.

### `POST /api/v1/events`

Publishes an event. The `Idempotency-Key` header is **required**.

```bash
curl -X POST localhost:8080/api/v1/events \
  -H 'Idempotency-Key: order-42-created' \
  -d '{"type":"order.created","payload":{"order_id":42}}'
```

`201 Created` returns `{"event_id": ..., "deduplicated": false, "deliveries": N}`.
Repeating the same key returns the original `event_id` with
`"deduplicated": true` and creates no additional deliveries. A missing or empty
`Idempotency-Key` returns `400`.

### `GET /api/v1/deliveries/{id}`

`{id}` is a **delivery** id. Returns the delivery status, attempt count, next
attempt time and the last response code, or `404` if it does not exist.

### `GET /healthz`

Liveness probe.

### Error mapping

| Domain error       | HTTP status |
|--------------------|-------------|
| `ErrNotFound`      | 404         |
| `ErrConflict`, `ErrAlreadyExists` | 409 |
| `ErrInvalidInput`  | 400         |
| anything else      | 500         |

`500` bodies carry a fixed message; the detail goes to the log only.

## Delivery semantics

* Lifecycle: `PENDING → SENDING → DELIVERED | RETRYING → DEAD_LETTER`.
  `DELIVERED` and `DEAD_LETTER` are terminal.
* Retry delay: `1s × 2^attempt` with ±50% jitter, at most 5 attempts, then
  `DEAD_LETTER`.
* `2xx` succeeds. `429`, `5xx`, timeouts and transport failures are retried.
  Every other status (other `4xx`, `3xx`) goes straight to `DEAD_LETTER`.
* Each outbound request carries `X-Signature: sha256=<hex_digest>` — HMAC-SHA256
  over the exact request body keyed with the subscription secret — plus
  `Content-Type: application/json` and the service `User-Agent`. One attempt is
  bounded by a 5 second context timeout.
* Outbound requests are throttled per target host using the subscription's
  `max_rps`. The limiter lives in the process, so running several instances
  multiplies the effective per-host rate.

**Delivery is at-least-once.** A worker that dies mid-attempt leaves the
delivery in `SENDING`; the reaper returns it to the queue after
`WD_STALE_THRESHOLD`, and the subscriber may then receive the same event twice.
The body carries a stable `event_id` and `delivery_id` — deduplicate on
`event_id`.

## Configuration

All configuration comes from the environment. `WD_POSTGRES_DSN` is required;
everything else has a default.

| Variable | Default | Meaning |
|----------|---------|---------|
| `WD_POSTGRES_DSN` | — (required) | PostgreSQL connection string |
| `WD_HTTP_ADDR` | `:8080` | REST API listen address |
| `WD_WORKER_POOL_SIZE` | `8` | concurrent delivery workers |
| `WD_BATCH_SIZE` | `50` | deliveries claimed per poll |
| `WD_POLL_INTERVAL` | `1s` | pause when there is no work |
| `WD_STALE_THRESHOLD` | `1m` | how long a claimed delivery may stay in `SENDING` |
| `WD_RELEASE_INTERVAL` | `30s` | how often the stale reaper runs |
| `WD_SEND_TIMEOUT` | `5s` | per-attempt timeout |
| `WD_SHUTDOWN_TIMEOUT` | `15s` | graceful shutdown budget |
| `WD_USER_AGENT` | `webhookdispatcher/1.0` | outbound `User-Agent` |

## Running from scratch

```bash
# 1. a database
docker run -d --name wd-pg -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=webhooks \
  -p 5432:5432 postgres:16-alpine

# 2. the service — migrations in internal/adapter/driven/postgres/migrations are
#    embedded and applied automatically at start-up, and are safe to re-run
export WD_POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:5432/webhooks?sslmode=disable'
go run ./cmd/webhookdispatcher
```

Shutdown is graceful: on `SIGINT`/`SIGTERM` the HTTP server stops accepting
requests, the workers stop claiming new deliveries, attempts already in flight
are given `WD_SHUTDOWN_TIMEOUT` to finish and persist their outcome, and only
then does the process exit.

## Tests

```bash
go test -race -short ./...   # no infrastructure needed
WD_TEST_POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:5432/webhooks?sslmode=disable' \
  go test -race ./...        # including the PostgreSQL integration tests
```

PostgreSQL tests skip themselves under `-short` or when
`WD_TEST_POSTGRES_DSN` is unset. `internal/architecture` holds guard tests that
fail if `internal/application` grows a dependency beyond the standard library
and `github.com/google/uuid`, or if one adapter starts importing another.
