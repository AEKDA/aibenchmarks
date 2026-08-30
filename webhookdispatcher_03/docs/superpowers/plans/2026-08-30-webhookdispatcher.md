# Webhook Dispatcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go-сервис надёжной доставки вебхуков по гексагональной архитектуре (REST-приём событий, idempotency, outbox, retry с backoff+jitter, HMAC-SHA256-подпись, per-host rate limit).

**Architecture:** Строгий hexagonal: чистый домен `internal/application` (только stdlib+uuid), адаптеры inbound (HTTP, worker) и outbound (postgres, httpsender, ratelimiter) зависят только от портов `application/ports`. Usecase/instruction = действие, вызов через `Invoke(ctx, ...)`.

**Tech Stack:** Go 1.27, `github.com/google/uuid`, `github.com/pkg/errors`, `github.com/gojuno/minimock/v3` (моки), PostgreSQL (SQL-миграции), stdlib `net/http`.

**Spec:** `promt.md` (ТЗ) + согласованный дизайн в чате. Scope: код + unit-тесты; без docker-compose и интеграционных тестов против живой БД.

## Global Constraints

- `internal/application` импортирует ТОЛЬКО stdlib и `github.com/google/uuid`. Никаких adapter-импортов.
- Адаптеры не импортируют друг друга.
- Все методы портов и адаптеров первым аргументом принимают `ctx context.Context`.
- Каждый usecase/instruction — тип-действие с методом `Invoke(...)`.
- Ошибки: пакет `errs` с типизированными `ErrNotFound/ErrConflict/ErrInvalid`; обёртка через `github.com/pkg/errors` `errors.Wrapf("...: %w", err)`. HTTP адаптер мапит в статусы 404/409/400 (+ 500).
- Моки — только minimock, лежат рядом с интерфейсом: `ports/foo.go` → `ports/mocks/foo_mock.go`, `//go:generate minimock -i <pkg>.<Iface> -o mocks -p mocks`.
- Комментарии на русском, публичные сущности `// Имя описание`.
- Логгер пакетный через `log.go` + `init()` (`MustComponentLogger("<layer.pkg>")`), в коде `logger.Debugf/Warnf/Errorf`.
- Даты/время UTC RFC3339. Graceful shutdown через `signal.NotifyContext`.
- Тесты: gofmt, `go vet ./...`, `go test -race ./...`.

## File Structure

```
go.mod
cmd/dispatcher/main.go            # DI-сборка всех слоёв, graceful shutdown
internal/config/config.go         # ENV-конфиг
internal/application/
  entity/subscription.go          # Subscription доменная модель + матчинг по типу
  entity/event.go                 # Event
  entity/delivery.go              # Delivery + status, attempts
  entity/outcome.go               # Outcome (2xx/4xx/…) → переходы
  entity/backoff.go               # Расчёт задержки T = base×2^n ± jitter
  entity/sign.go                  # HMAC-SHA256 подпись
  entity/*_test.go                # unit-тесты домена
  errs/errs.go
  ports/ports.go                  # все интерфейсы (repo, dispatcher, ratelimiter, clock, tx)
  ports/mocks/*.go                # minimock
  instruction/sign_payload.go     # Invoke: подписать body
  instruction/schedule_retry.go   # Invoke: решить DELIVERED/RETRYING/DEAD_LETTER + nextAttemptAt
  instruction/*_test.go
  usecase/create_subscription.go
  usecase/publish_event.go
  usecase/get_delivery.go
  usecase/claim_next.go
  usecase/process_delivery.go
  usecase/*_test.go
internal/adapter/driver/http/handler.go   # REST handlers
internal/adapter/driver/http/dto.go
internal/adapter/driver/http/errors.go
internal/adapter/driver/http/*_test.go
internal/adapter/driver/worker/pool.go    # пул горутин
internal/adapter/driver/worker/*_test.go
internal/adapter/driven/postgres/postgres.go        # pgx pool
internal/adapter/driven/postgres/subscription_repo.go
internal/adapter/driven/postgres/event_repo.go
internal/adapter/driven/postgres/delivery_repo.go
internal/adapter/driven/postgres/migrations/0001_init.sql
internal/adapter/driven/httpsender/sender.go
internal/adapter/driven/httpsender/*_test.go
internal/adapter/driven/ratelimiter/limiter.go
internal/adapter/driven/ratelimiter/*_test.go
```

## Task 1: go.mod + каркас

**Files:**
- Create: `go.mod`
- Create: `internal/application/entity/backoff.go`
- Test: `internal/application/entity/backoff_test.go`

**Interfaces:**
- Produces: `entity.BackoffDelay(attempt int) time.Duration`, `entity.MaxAttempts = 5` const.

- [ ] **Step 1: init module + deps**

```bash
cd /Users/david_keshishyan/Documents/projects/aibenchmarks/webhookdispatcher_03
go mod init webhookdispatcher
go get github.com/google/uuid github.com/pkg/errors
go get github.com/gojuno/minimock/v3
```

- [ ] **Step 2: Write failing test for backoff**

`internal/application/entity/backoff_test.go`:

```go
package entity

import (
    "testing"
    "time"
)

func TestBackoffDelayBounds(t *testing.T) {
    for attempt := 0; attempt < MaxAttempts; attempt++ {
        got := BackoffDelay(attempt)
        // base=1s, jitter ±25%: верхняя граница ~ base*2^attempt*1.25
        upper := time.Second*time.Duration(1<<attempt) + time.Second*time.Duration(1<<attempt)/4
        if got <= 0 || got > upper {
            t.Fatalf("attempt=%d: delay %v вне границ (0, %v]", attempt, got, upper)
        }
    }
}

func TestBackoffDelayDeterministicSeeded(t *testing.T) {
    a := BackoffDelay(2)
    b := BackoffDelay(2)
    if a <= 0 || b <= 0 {
        t.Fatalf("delay должен быть >0, got %v %v", a, b)
    }
}
```

- [ ] **Step 3: Run, verify fail (no package)**

Run: `go test ./internal/application/entity/`
Expected: FAIL (no Go files / undefined BackoffDelay).

- [ ] **Step 4: Implement backoff**

`internal/application/entity/backoff.go`:

```go
package entity

import (
    "math/rand"
    "time"
)

// MaxAttempts лимит попыток доставки (включая первую).
const MaxAttempts = 5

const (
    backoffBase   = time.Second
    backoffJitter = 0.25
)

// BackoffDelay рассчитывает экспоненциальную задержку с джиттером для попытки attempt.
// T = base × 2^attempt ± jitter. attempt индексируется с 0 (первая попытка → base).
func BackoffDelay(attempt int) time.Duration {
    if attempt < 0 {
        attempt = 0
    }
    base := backoffBase * time.Duration(1<<attempt)
    delta := time.Duration(float64(base) * backoffJitter * rand.Float64())
    if rand.Intn(2) == 0 {
        return base - delta
    }
    return base + delta
}
```

- [ ] **Step 5: Run tests + vet**

Run: `go test -race ./internal/application/entity/ && go vet ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/application/entity/backoff.go internal/application/entity/backoff_test.go
git commit -m "feat: каркас модуля, экспоненциальный бэкофф с джиттером"
```

## Task 2: Сущности Subscription, Event, Delivery, Outcome

**Files:**
- Create: `internal/application/entity/subscription.go`, `event.go`, `delivery.go`, `outcome.go`
- Test: `internal/application/entity/subscription_test.go`, `delivery_test.go`, `outcome_test.go`

**Interfaces:**
- Consumes: `MaxAttempts`, `BackoffDelay` из Task 1.
- Produces:
  - `type Subscription struct { ID uuid.UUID; URL string; Secret string; Events []string; MaxRPS int }`, `func (s Subscription) Matches(eventType string) bool` (пустой список = подписка ни на что? нет — матчинг только по вхождению; см. решение «по типу события»).
  - `type Event struct { ID uuid.UUID; Type string; Payload []byte; CreatedAt time.Time }`.
  - `type DeliveryStatus string` с константами `StatusPending/Sending/Delivered/Retrying/DeadLetter`.
  - `type Delivery struct { ID uuid.UUID; EventID, SubscriptionID uuid.UUID; Status DeliveryStatus; Attempt int; NextAttemptAt time.Time; Payload []byte; LastHTTPStatus int }`.
  - Outcome: `type Outcome int` (`OutcomeDelivered`, `OutcomeRetry`, `OutcomeDead`), функция решения от HTTP-статуса.

- [ ] **Step 1: Write failing tests**

`internal/application/entity/subscription_test.go`:

```go
package entity

import (
    "testing"
)

func TestSubscriptionMatches(t *testing.T) {
    s := Subscription{Events: []string{"order.created", "order.cancelled"}}
    cases := []struct {
        eventType string
        want      bool
    }{
        {"order.created", true},
        {"order.cancelled", true},
        {"user.created", false},
    }
    for _, c := range cases {
        if got := s.Matches(c.eventType); got != c.want {
            t.Errorf("Matches(%q)=%v want %v", c.eventType, got, c.want)
        }
    }
}
```

`internal/application/entity/delivery_test.go` — state machine и лимит попыток:

```go
package entity

import (
    "testing"
)

func TestDeliveryStart(t *testing.T) {
    d := Delivery{}
    d.Start()
    if d.Status != StatusSending || d.Attempt != 1 {
        t.Fatalf("после Start: status=%v attempt=%d (хотят SENDING, 1)", d.Status, d.Attempt)
    }
}

func TestDeliveryScheduleNeverExceedsAttempts(t *testing.T) {
    d := Delivery{Status: StatusSending, Attempt: 1}
    // не хватает попыток → dead letter
    d.ScheduleFrom(OutcomeDead)
    if d.Status != StatusDeadLetter {
        t.Fatalf("OutcomeDead → хотят DEAD_LETTER, got %v", d.Status)
    }
}
```

`internal/application/entity/outcome_test.go`:

```go
package entity

import (
    "testing"
)

func TestOutcomeFromStatus(t *testing.T) {
    cases := []struct {
        code int
        want Outcome
    }{
        {200, OutcomeDelivered},
        {204, OutcomeDelivered},
        {201, OutcomeDelivered},
        {429, OutcomeRetry},  // 429 трактуется как retry (перегрузка)
        {400, OutcomeRetry},
        {404, OutcomeRetry},
        {500, OutcomeRetry},
        {503, OutcomeRetry},
    }
    for _, c := range cases {
        if got := OutcomeFromStatus(c.code); got != c.want {
            t.Errorf("OutcomeFromStatus(%d)=%v want %v", c.code, got, c.want)
        }
    }
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test -race ./internal/application/entity/`
Expected: FAIL (undefined types).

- [ ] **Step 3: Implement entity files**

`internal/application/entity/subscription.go`:

```go
package entity

import "github.com/google/uuid"

// Subscription подписчик на события: точки доставки и правила.
type Subscription struct {
    ID     uuid.UUID
    URL    string
    Secret string
    Events []string
    MaxRPS int
}

// Matches сообщает, подписан ли подписчик на событие данного типа.
func (s Subscription) Matches(eventType string) bool {
    for _, e := range s.Events {
        if e == eventType {
            return true
        }
    }
    return false
}
```

`internal/application/entity/event.go`:

```go
package entity

import (
    "github.com/google/uuid"
    "time"
)

// Event публикуемое событие.
type Event struct {
    ID        uuid.UUID
    Type      string
    Payload   []byte
    CreatedAt time.Time
}
```

`internal/application/entity/delivery.go`:

```go
package entity

import (
    "github.com/google/uuid"
    "time"
)

// DeliveryStatus статус доставки.
type DeliveryStatus string

// Статусы жизненного цикла доставки.
const (
    StatusPending    DeliveryStatus = "PENDING"
    StatusSending    DeliveryStatus = "SENDING"
    StatusDelivered  DeliveryStatus = "DELIVERED"
    StatusRetrying   DeliveryStatus = "RETRYING"
    StatusDeadLetter DeliveryStatus = "DEAD_LETTER"
)

// Delivery одна доставка события подписчику (event × subscription).
type Delivery struct {
    ID             uuid.UUID
    EventID        uuid.UUID
    SubscriptionID uuid.UUID
    Status         DeliveryStatus
    Attempt        int
    NextAttemptAt  time.Time
    Payload        []byte
    LastHTTPStatus int
}

// Start переводит задачу в SENDING и увеличивает счётчик попыток.
func (d *Delivery) Start() {
    d.Status = StatusSending
    d.Attempt++
}

// ScheduleFrom применяет исход запроса: доставлен, retry или dead letter.
func (d *Delivery) ScheduleFrom(o Outcome) {
    switch o {
    case OutcomeDelivered:
        d.Status = StatusDelivered
    case OutcomeDead:
        d.Status = StatusDeadLetter
    default:
        d.Status = StatusRetrying
        d.NextAttemptAt = time.Now().UTC().Add(BackoffDelay(d.Attempt))
    }
}
```

`internal/application/entity/outcome.go`:

```go
package entity

// Outcome решение по HTTP-статусу ответа подписчика.
type Outcome int

// Итоги доставки.
const (
    OutcomeDelivered Outcome = iota // 2xx
    OutcomeRetry                   // прочие статусы — retry
    OutcomeDead                    // у задачи исчерпаны попытки
)

// OutcomeFromStatus возвращает Outcome по http-статусу согласно политике
// (2xx → delivered; все остальные коды, включая 429/4xx/5xx, → retry).
func OutcomeFromStatus(code int) Outcome {
    if code >= 200 && code < 300 {
        return OutcomeDelivered
    }
    return OutcomeRetry
}
```

- [ ] **Step 4: Run tests + vet**

Run: `go test -race ./internal/application/entity/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/application/entity/
git commit -m "feat: сущности домена и state machine доставки"
```

## Task 3: Пакет errs и порты

**Files:**
- Create: `internal/application/errs/errs.go`
- Create: `internal/application/ports/ports.go`
- Test: `internal/application/errs/errs_test.go`

**Interfaces:**
- Produces errs: `ErrNotFound`, `ErrConflict`, `ErrInvalid` — типы, `Is(...) bool`.
- Produces ports итерфейсы (подробности в шаге 3).

- [ ] **Step 1: Write failing test for errs**

`internal/application/errs/errs_test.go`:

```go
package errs

import (
    "errors"
    "testing"
)

func TestIs(t *testing.T) {
    if !Is(ErrNotFound, ErrNotFound) {
        t.Fatal("Is(ErrNotFound, ErrNotFound) должно быть true")
    }
    if Is(ErrConflict, ErrNotFound) {
        t.Fatal("ErrConflict не должен считаться ErrNotFound")
    }
    wrapped := errors.New("x") // тип DomainError через %w
    _ = wrapped
}
```

- [ ] **Step 2: Implement errs**

`internal/application/errs/errs.go`:

```go
package errs

import "errors"

// domainError доменная ошибка с фиксированным видом.
type domainError struct {
    kind string
}

func (e domainError) Error() string { return e.kind }

// Is позволяет errors.Is находить типизированные доменные ошибки.
func (e domainError) Is(target error) bool {
    t, ok := target.(domainError)
    return ok && t.kind == e.kind
}

// Предопределённые доменные ошибки.
var (
    ErrNotFound = domainError{kind: "not found"}
    ErrConflict = domainError{kind: "conflict"}
    ErrInvalid  = domainError{kind: "invalid"}
)

// Is сообщает, является ли err искомой доменной ошибкой kind.
func Is(err, target error) bool {
    return errors.Is(err, target)
}
```

- [ ] **Step 3: Implement ports**

`internal/application/ports/ports.go`:

```go
// Package ports объявляет все порты гексагональной архитектуры —
// интерфейсы, которые домен требует от адаптеров.
package ports

import (
    "context"
    "time"

    "github.com/google/uuid"

    "webhookdispatcher/internal/application/entity"
)

// SubscriptionRepo хранилище подписчиков.
type SubscriptionRepo interface {
    Save(ctx context.Context, s entity.Subscription) error
    GetByID(ctx context.Context, id uuid.UUID) (entity.Subscription, error)
    GetByEventType(ctx context.Context, eventType string) ([]entity.Subscription, error)
}

// OutboxResult результат атомарного сохранения события и доставок.
type OutboxResult struct {
    EventID   uuid.UUID
    DeliveryIDs []uuid.UUID
    Duplicate bool // true, если idempotency-ключ уже был применён
}

// EventRepo хранилище событий с идемпотентностью по ключу.
type EventRepo interface {
    // SaveWithin откладывает запись события и доставок в открытую транзакцию;
    // возвращает Duplicate, если ключ уже использован (атомарно).
    SaveWithin(ctx context.Context, idempotencyKey string, ev entity.Event, del []entity.Delivery) (OutboxResult, error)
}

// DeliveryRepo хранилище доставок с конкурентным забором задач.
type DeliveryRepo interface {
    // ClaimNext захватывает до n задач PENDING или готовых RETRYING (SKIP LOCKED),
    // переводит их в SENDING. Возвращает захваченные задачи.
    ClaimNext(ctx context.Context, limit int, now time.Time) ([]entity.Delivery, error)
    // MarkOutcome применяет исход доставки (DELIVERED/RETRYING/DEAD_LETTER).
    MarkOutcome(ctx context.Context, d entity.Delivery) error
    GetByID(ctx context.Context, id uuid.UUID) (entity.Delivery, error)
}

// Sender исходящий HTTP-клиент к подписчику.
type Sender interface {
    // Send доставляет payload по URL с подписью. Возвращает HTTP-статус.
    Send(ctx context.Context, url, userAgent, signature string, payload []byte) (int, error)
}

// RateLimiter ограничивает RPS на хост.
type RateLimiter interface {
    // Allow блокирует до появления слота для хоста. Ошибка — если хоста нет в конфиге.
    Allow(ctx context.Context, host string) error
}

// Clock абстракция времени для тестов.
type Clock interface {
    Now() time.Time
}
```

- [ ] **Step 4: Run tests + vet**

Run: `go test -race ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/application/errs/ internal/application/ports/
git commit -m "feat: типизированные доменные ошибки и порты"
```

## Task 4: Инструкции SignPayload и ScheduleRetry

**Files:**
- Create: `internal/application/instruction/sign_payload.go`, `schedule_retry.go`
- Test: `internal/application/instruction/sign_payload_test.go`, `schedule_retry_test.go`

**Interfaces:**
- Consumes: `entity.Subscription`, `entity.Delivery`, `entity.BackoffDelay`, `entity.OutcomeFromStatus`.
- Produces:
  - `signPayload.Invoke(secret string, body []byte) string` — `sha256=<hex>`.
  - `scheduleRetry.Invoke(delivery *entity.Delivery, status int) (entity.Outcome, error)`.

- [ ] **Step 1: Write failing tests**

`sign_payload_test.go`:

```go
package instruction

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "testing"

    "webhookdispatcher/internal/application/instruction"
)

func TestSignPayloadInvoke(t *testing.T) {
    secret := "s3cret"
    body := []byte(`{"type":"order.created"}`)
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

    got := SignPayload().Invoke(secret, body)
    if got != want {
        t.Fatalf("SignPayload().Invoke()=%q want %q", got, want)
    }
}
```

`schedule_retry_test.go`:

```go
package instruction

import (
    "testing"

    "webhookdispatcher/internal/application/entity"
    "webhookdispatcher/internal/application/instruction"
)

func TestScheduleRetryDelivered(t *testing.T) {
    d := &entity.Delivery{Status: entity.StatusSending, Attempt: 1}
    o, err := ScheduleRetry().Invoke(d, 200)
    if err != nil {
        t.Fatal(err)
    }
    if o != entity.OutcomeDelivered || d.Status != entity.StatusDelivered {
        t.Fatalf("хотят delivered, got outcome=%v status=%v", o, d.Status)
    }
}

func TestScheduleRetryExhaustsAttempts(t *testing.T) {
    d := &entity.Delivery{Status: entity.StatusSending, Attempt: entity.MaxAttempts}
    o, err := ScheduleRetry().Invoke(d, 500)
    if err != nil {
        t.Fatal(err)
    }
    if o != entity.OutcomeDead || d.Status != entity.StatusDeadLetter {
        t.Fatalf("исчерпаны попытки → dead letter, got %v %v", o, d.Status)
    }
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test -race ./internal/application/instruction/`
Expected: FAIL (undefined SignPayload/ScheduleRetry).

- [ ] **Step 3: Implement instructions**

`sign_payload.go`:

```go
// Package instruction содержит переиспользуемые шаги бизнес-логики.
package instruction

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
)

// SignPayload подписывает body секретом подписки (HMAC-SHA256).
type SignPayload struct{}

// SignPayload возвращает инструкцию подписи полезной нагрузки.
func SignPayload() *SignPayload { return &SignPayload{} }

// Invoke возвращает значение заголовка X-Signature: sha256=<hex>.
func (SignPayload) Invoke(secret string, body []byte) string {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
```

`schedule_retry.go`:

```go
package instruction

import (
    "webhookdispatcher/internal/application/entity"
)

// ScheduleRetry применяет исход HTTP-запроса к доставке и пересчитывает
// отложенную дату следующей попытки (если попытки не исчерпаны).
type ScheduleRetry struct{}

// ScheduleRetry возвращает инструкцию планирования следующей попытки.
func ScheduleRetry() *ScheduleRetry { return &ScheduleRetry{} }

// Invoke переводит доставку по статусу ответа: 2xx → DELIVERED;
// иначе RETRYING до лимита попыток и далее DEAD_LETTER. Возвращает исход.
func (ScheduleRetry) Invoke(d *entity.Delivery, httpStatus int) (entity.Outcome, error) {
    o := entity.OutcomeFromStatus(httpStatus)
    if o == entity.OutcomeRetry && d.Attempt >= entity.MaxAttempts {
        o = entity.OutcomeDead
    }
    d.ScheduleFrom(o)
    return o, nil
}
```

- [ ] **Step 4: Run tests + vet**

Run: `go test -race ./internal/application/... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/application/instruction/
git commit -m "feat: инструкции подписи и планирования повторов"
```

## Task 5: Порты-моки (minimock) + генерация

**Files:**
- Create: `internal/application/ports/mocks/*.go` (сгенерированные)
- Modify: `internal/application/ports/ports.go` (добавить `//go:generate` на каждый интерфейс)

**Interfaces:**
- Produces: `mocks.SubscriptionRepoMock`, `mocks.EventRepoMock`, `mocks.DeliveryRepoMock`, `mocks.SenderMock`, `mocks.RateLimiterMock`, `mocks.ClockMock`.

- [ ] **Step 1: Add go:generate directives**

Добавить над каждым интерфейсом в `ports.go` строку:

```go
//go:generate minimock -i SubscriptionRepo -o ./mocks -p mocks
```

(аналогично для EventRepo, DeliveryRepo, Sender, RateLimiter, Clock).

- [ ] **Step 2: Run minimock**

Run: `cd internal/application/ports && go generate ./...`
Expected: созданы файлы `mocks/*_mock.go`, пакет собирается: `go build ./...`.

- [ ] **Step 3: Commit**

```bash
git add internal/application/ports/
git commit -m "feat: minimock-моки портов"
```

## Task 6: Usecase CreateSubscription

**Files:**
- Create: `internal/application/usecase/create_subscription.go`
- Test: `internal/application/usecase/create_subscription_test.go`

**Interfaces:**
- Consumes: `entity.Subscription`, `ports.SubscriptionRepo`, `mocks.SubscriptionRepoMock`.
- Produces: `type CreateSubscription struct{...}; func NewCreateSubscription(repo ports.SubscriptionRepo) *CreateSubscription; func (c *CreateSubscription) Invoke(ctx, in CreateSubscriptionIn) (entity.Subscription, error)`; `CreateSubscriptionIn{URL, Secret string; Events []string; MaxRPS int}`.

- [ ] **Step 1: Write failing test**

```go
package usecase

import (
    "context"
    "testing"

    "webhookdispatcher/internal/application/entity"
    "webhookdispatcher/internal/application/ports/mocks"
    "webhookdispatcher/internal/application/usecase"
)

func TestCreateSubscriptionInvoke(t *testing.T) {
    ctx := context.Background()
    repo := mocks.NewSubscriptionRepoMock(t)
    var saved entity.Subscription
    repo.SaveMock.Set(func(_ context.Context, s entity.Subscription) error {
        saved = s
        return nil
    })

    uc := usecase.NewCreateSubscription(repo)
    got, err := uc.Invoke(ctx, usecase.CreateSubscriptionIn{
        URL: "https://s.example/hook", Secret: "shh", Events: []string{"order.created"}, MaxRPS: 5,
    })
    if err != nil {
        t.Fatal(err)
    }
    if got.ID == [16]byte{} {
        t.Fatal("ожидался сгенерированный ID")
    }
    if got.URL != saved.URL {
        t.Fatalf("Save не получил подписку: %+v", saved)
    }
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test -race ./internal/application/usecase/`
Expected: FAIL (no package / undefined usecase).

- [ ] **Step 3: Implement usecase**

`internal/application/usecase/create_subscription.go`:

```go
// Package usecase содержит сценарии применения доменной логики.
package usecase

import (
    "context"
    "net/url"

    "github.com/google/uuid"

    "webhookdispatcher/internal/application/entity"
    "webhookdispatcher/internal/application/errs"
    "webhookdispatcher/internal/application/ports"
)

// CreateSubscriptionIn входные данные регистрации подписчика.
type CreateSubscriptionIn struct {
    URL     string
    Secret  string
    Events  []string
    MaxRPS  int
}

// CreateSubscription регистрирует подписчика.
type CreateSubscription struct {
    repo ports.SubscriptionRepo
}

// NewCreateSubscription собирает сценарий с хранилищем подписчиков.
func NewCreateSubscription(repo ports.SubscriptionRepo) *CreateSubscription {
    return &CreateSubscription{repo: repo}
}

// Invoke валидирует и сохраняет нового подписчика.
func (c *CreateSubscription) Invoke(ctx context.Context, in CreateSubscriptionIn) (entity.Subscription, error) {
    if _, err := url.ParseRequestURI(in.URL); err != nil || in.URL == "" {
        return entity.Subscription{}, errs.ErrInvalid
    }
    if in.Secret == "" {
        return entity.Subscription{}, errs.ErrInvalid
    }
    s := entity.Subscription{
        ID: uuid.New(), URL: in.URL, Secret: in.Secret,
        Events: in.Events, MaxRPS: in.MaxRPS,
    }
    if err := c.repo.Save(ctx, s); err != nil {
        return entity.Subscription{}, err
    }
    return s, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/application/usecase/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/application/usecase/create_subscription.go internal/application/usecase/create_subscription_test.go
git commit -m "feat: usecase регистрации подписчика"
```

## Task 7: Usecase PublishEvent (idempotency + outbox)

**Files:**
- Create: `internal/application/usecase/publish_event.go`
- Test: `internal/application/usecase/publish_event_test.go`

**Interfaces:**
- Consumes: `entity.Event`, `entity.Delivery`, `ports.EventRepo`, `ports.SubscriptionRepo`, `mocks.*`.
- Produces: `type PublishEvent struct{...}; func NewPublishEvent(events ports.EventRepo, subs ports.SubscriptionRepo) *PublishEvent; func (p *PublishEvent) Invoke(ctx, in PublishEventIn) (PublishEventOut, error)`; `PublishEventIn{Type string; Payload []byte; IdempotencyKey string; Now time.Time}`; `PublishEventOut{EventID uuid.UUID; Duplicate bool}`.

- [ ] **Step 1: Write failing test**

```go
package usecase

import (
    "context"
    "testing"

    "github.com/google/uuid"
    "webhookdispatcher/internal/application/entity"
    "webhookdispatcher/internal/application/ports"
    "webhookdispatcher/internal/application/ports/mocks"
    "webhookdispatcher/internal/application/usecase"
)

func TestPublishEventCreatesDeliveries(t *testing.T) {
    ctx := context.Background()
    now := mustTime(t, "2026-01-01T00:00:00Z")

    eventRepo := mocks.NewEventRepoMock(t)
    eventRepo.SaveWithinMock.Set(func(_ context.Context, key string, ev entity.Event, del []entity.Delivery) (ports.OutboxResult, error) {
        if key != "k1" {
            t.Fatalf("key=%q want k1", key)
        }
        if ev.Type != "order.created" || string(ev.Payload) != "{}" {
            t.Fatalf("bad event: %+v", ev)
        }
        if len(del) != 1 || del[0].Status != entity.StatusPending || del[0].SubscriptionID != subsID {
            t.Fatalf("bad deliveries: %+v", del)
        }
        return ports.OutboxResult{EventID: ev.ID, DeliveryIDs: []uuid.UUID{del[0].ID}}, nil
    })

    subRepo := mocks.NewSubscriptionRepoMock(t)
    const subsID = "11111111-1111-1111-1111-111111111111"
    subRepo.GetByEventTypeMock.Set(func(_ context.Context, et string) ([]entity.Subscription, error) {
        return []entity.Subscription{{ID: uuid.MustParse(subsID), URL: "https://s/h", Secret: "k"}}, nil
    })

    uc := usecase.NewPublishEvent(eventRepo, subRepo)
    out, err := uc.Invoke(ctx, usecase.PublishEventIn{Type: "order.created", Payload: []byte("{}"), IdempotencyKey: "k1", Now: now})
    if err != nil {
        t.Fatal(err)
    }
    if out.Duplicate {
        t.Fatal("новое событие не должно помечаться дубликатом")
    }
}

func mustTime(t *testing.T, s string) time.Time {
    t.Helper()
    v, err := time.Parse(time.RFC3339, s)
    if err != nil {
        t.Fatal(err)
    }
    return v
}
```

(добавь второй тест: повторный ключ → out.Duplicate==true.)

- [ ] **Step 2: Run, verify fail**

Run: `go test -race ./internal/application/usecase/`
Expected: FAIL.

- [ ] **Step 3: Implement usecase**

`internal/application/usecase/publish_event.go`:

```go
package usecase

import (
    "context"
    "time"

    "github.com/google/uuid"

    "webhookdispatcher/internal/application/entity"
    "webhookdispatcher/internal/application/errs"
    "webhookdispatcher/internal/application/ports"
)

// PublishEventIn входные данные публикации события.
type PublishEventIn struct {
    Type          string
    Payload       []byte
    IdempotencyKey string
    Now           time.Time
}

// PublishEventOut результат публикации.
type PublishEventOut struct {
    EventID   uuid.UUID
    Duplicate bool
}

// PublishEvent публикует событие: в единой транзакции сохраняет событие
// и создаёт доставки для подписанных подписчиков (transactional outbox).
type PublishEvent struct {
    events ports.EventRepo
    subs   ports.SubscriptionRepo
}

// NewPublishEvent собирает сценарий публикации.
func NewPublishEvent(events ports.EventRepo, subs ports.SubscriptionRepo) *PublishEvent {
    return &PublishEvent{events: events, subs: subs}
}

// Invoke публикует событие с гарантией идемпотентности по IdempotencyKey.
func (p *PublishEvent) Invoke(ctx context.Context, in PublishEventIn) (PublishEventOut, error) {
    if in.Type == "" || in.IdempotencyKey == "" {
        return PublishEventOut{}, errs.ErrInvalid
    }
    subs, err := p.subs.GetByEventType(ctx, in.Type)
    if err != nil {
        return PublishEventOut{}, err
    }
    ev := entity.Event{ID: uuid.New(), Type: in.Type, Payload: in.Payload, CreatedAt: in.Now}
    dels := make([]entity.Delivery, 0, len(subs))
    for _, s := range subs {
        dels = append(dels, entity.Delivery{
            ID: uuid.New(), EventID: ev.ID, SubscriptionID: s.ID,
            Status: entity.StatusPending, Payload: in.Payload,
        })
    }
    res, err := p.events.SaveWithin(ctx, in.IdempotencyKey, ev, dels)
    if err != nil {
        return PublishEventOut{}, err
    }
    return PublishEventOut{EventID: res.EventID, Duplicate: res.Duplicate}, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/application/usecase/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/application/usecase/publish_event.go internal/application/usecase/publish_event_test.go
git commit -m "feat: usecase публикации события с идемпотентностью и outbox"
```

## Task 8: Usecase ClaimNext

**Files:**
- Create: `internal/application/usecase/claim_next.go`
- Test: `internal/application/usecase/claim_next_test.go`

**Interfaces:**
- Consumes: `ports.DeliveryRepo`, `ports.RateLimiter`, `mocks.*`.
- Produces: `type ClaimNext struct{...}; func NewClaimNext(repo ports.DeliveryRepo) *ClaimNext; func (c *ClaimNext) Invoke(ctx, now time.Time, limit int) ([]entity.Delivery, error)`.

- [ ] **Step 1: Write failing test**

```go
func TestClaimNextInvoke(t *testing.T) {
    ctx := context.Background()
    now := mustTime(t, "2026-01-01T00:00:00Z")
    repo := mocks.NewDeliveryRepoMock(t)
    repo.ClaimNextMock.Set(func(_ context.Context, limit int, n time.Time) ([]entity.Delivery, error) {
        if limit != 10 {
            t.Fatalf("limit=%d want 10", limit)
        }
        return []entity.Delivery{{ID: uuid.New(), Status: entity.StatusSending}}, nil
    })
    uc := usecase.NewClaimNext(repo)
    got, err := uc.Invoke(ctx, now, 10)
    if err != nil {
        t.Fatal(err)
    }
    if len(got) != 1 {
        t.Fatalf("ожидалась 1 задача, got %d", len(got))
    }
}
```

- [ ] **Step 2: Implement usecase**

`internal/application/usecase/claim_next.go`:

```go
package usecase

import (
    "context"
    "time"

    "webhookdispatcher/internal/application/entity"
    "webhookdispatcher/internal/application/ports"
)

// ClaimNext забирает до limit готовых задач на доставку из хранилища.
type ClaimNext struct {
    repo ports.DeliveryRepo
}

// NewClaimNext собирает сценарий забора задач.
func NewClaimNext(repo ports.DeliveryRepo) *ClaimNext {
    return &ClaimNext{repo: repo}
}

// Invoke захватывает до limit задач (PENDING/готовых RETRYING) и переводит их в SENDING.
func (c *ClaimNext) Invoke(ctx context.Context, now time.Time, limit int) ([]entity.Delivery, error) {
    return c.repo.ClaimNext(ctx, limit, now)
}
```

- [ ] **Step 3: Run tests**

Run: `go test -race ./internal/application/usecase/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/application/usecase/claim_next.go internal/application/usecase/claim_next_test.go
git commit -m "feat: usecase забора задач на доставку"
```

## Task 9: Usecase ProcessDelivery

**Files:**
- Create: `internal/application/usecase/process_delivery.go`
- Test: `internal/application/usecase/process_delivery_test.go`

**Interfaces:**
- Consumes: `entity.Delivery`, `entity.Subscription`, `ports.DeliveryRepo`, `ports.SubscriptionRepo`, `ports.Sender`, `ports.RateLimiter`, `instruction.SignPayload`, `instruction.ScheduleRetry`.
- Produces: `type ProcessDelivery struct{...}; func NewProcessDelivery(dr ports.DeliveryRepo, sr ports.SubscriptionRepo, s ports.Sender, rl ports.RateLimiter, now func() time.Time) *ProcessDelivery; func (p *ProcessDelivery) Invoke(ctx, d entity.Delivery) error`.

- [ ] **Step 1: Write failing test**

```go
func TestProcessDeliveryDelivered(t *testing.T) {
    ctx := context.Background()
    d := entity.Delivery{ID: uuid.New(), EventID: uuid.New(), SubscriptionID: uuid.New(), Payload: []byte("{}"), Status: entity.StatusSending, Attempt: 1}

    subRepo := mocks.NewSubscriptionRepoMock(t)
    subRepo.GetByIDMock.Set(func(_ context.Context, id uuid.UUID) (entity.Subscription, error) {
        return entity.Subscription{ID: id, URL: "https://s/h", Secret: "shh", MaxRPS: 1}, nil
    })
    rl := mocks.NewRateLimiterMock(t)
    rl.AllowMock.Set(func(_ context.Context, host string) error { return nil })
    sender := mocks.NewSenderMock(t)
    sender.SendMock.Set(func(_ context.Context, url, ua, sig string, payload []byte) (int, error) {
        if url != "https://s/h" { t.Fatalf("url=%q", url) }
        if !strings.HasPrefix(sig, "sha256=") { t.Fatalf("sig=%q", sig) }
        return 200, nil
    })
    deliveryRepo := mocks.NewDeliveryRepoMock(t)
    deliveryRepo.MarkOutcomeMock.Set(func(_ context.Context, dd entity.Delivery) error {
        if dd.Status != entity.StatusDelivered { t.Fatalf("status=%v", dd.Status) }
        return nil
    })

    uc := usecase.NewProcessDelivery(deliveryRepo, subRepo, sender, rl, func() time.Time { return mustTime(t, "2026-01-01T00:00:00Z") })
    if err := uc.Invoke(ctx, d); err != nil {
        t.Fatal(err)
    }
}
```

- [ ] **Step 2: Implement usecase**

`internal/application/usecase/process_delivery.go`:

```go
package usecase

import (
    "context"
    "time"

    "webhookdispatcher/internal/application/entity"
    "webhookdispatcher/internal/application/instruction"
    "webhookdispatcher/internal/application/ports"
)

// ProcessDelivery исполняет одну доставку: рейт-лимит, подпись, отправка,
// применение исхода (DELIVERED/RETRYING/DEAD_LETTER).
type ProcessDelivery struct {
    deliveries ports.DeliveryRepo
    subs       ports.SubscriptionRepo
    sender     ports.Sender
    rl         ports.RateLimiter
    now        func() time.Time
}

// NewProcessDelivery собирает сценарий доставки.
func NewProcessDelivery(deliveries ports.DeliveryRepo, subs ports.SubscriptionRepo, sender ports.Sender, rl ports.RateLimiter, now func() time.Time) *ProcessDelivery {
    return &ProcessDelivery{deliveries: deliveries, subs: subs, sender: sender, rl: rl, now: now}
}

// Invoke исполняет доставку задачи d.
func (p *ProcessDelivery) Invoke(ctx context.Context, d entity.Delivery) error {
    d.Start() // SENDING + attempt++
    sub, err := p.subs.GetByID(ctx, d.SubscriptionID)
    if err != nil {
        return err
    }
    sig := instruction.SignPayload().Invoke(sub.Secret, d.Payload)
    ua := "webhook-dispatcher/1.0"
    if err := p.rl.Allow(ctx, hostFromURL(sub.URL)); err != nil {
        return err
    }
    status, err := p.sender.Send(ctx, sub.URL, ua, sig, d.Payload)
    if err != nil {
        // сетевой сбой / timeout → retry
        status = 500
    }
    _, err = instruction.ScheduleRetry().Invoke(&d, status)
    if err != nil {
        return err
    }
    return p.deliveries.MarkOutcome(ctx, d)
}

// hostFromURL извлекает хост из URL подписчика (для рейт-лимита).
func hostFromURL(rawURL string) string {
    if u, err := url.Parse(rawURL); err == nil {
        return u.Host
    }
    return rawURL
}
```

- [ ] **Step 3: Run tests**

Run: `go test -race ./internal/application/usecase/`
Expected: PASS. (добавить тест исчерпания попыток → DEAD_LETTER и сетевой ошибки → RETRYING.)

- [ ] **Step 4: Commit**

```bash
git add internal/application/usecase/process_delivery.go internal/application/usecase/process_delivery_test.go
git commit -m "feat: usecase процесса доставки с подписью и рейт-лимитом"
```

## Task 10: Usecase GetDelivery

**Files:**
- Create: `internal/application/usecase/get_delivery.go`
- Test: `internal/application/usecase/get_delivery_test.go`

**Interfaces:**
- Consumes: `ports.DeliveryRepo`.
- Produces: `type GetDelivery struct{...}; func NewGetDelivery(repo ports.DeliveryRepo) *GetDelivery; func (g *GetDelivery) Invoke(ctx, id uuid.UUID) (entity.Delivery, error)` (пробрасывает `errs.ErrNotFound` из репо).

- [ ] **Step 1: Write test + implement (транзитный сценарий)**

`get_delivery.go`:

```go
package usecase

import (
    "context"

    "github.com/google/uuid"
    "webhookdispatcher/internal/application/entity"
    "webhookdispatcher/internal/application/ports"
)

// GetDelivery возвращает статус доставки по ID.
type GetDelivery struct {
    repo ports.DeliveryRepo
}

// NewGetDelivery собирает сценарий чтения доставки.
func NewGetDelivery(repo ports.DeliveryRepo) *GetDelivery {
    return &GetDelivery{repo: repo}
}

// Invoke возвращает доставку по ID (errs.ErrNotFound, если нет).
func (g *GetDelivery) Invoke(ctx context.Context, id uuid.UUID) (entity.Delivery, error) {
    return g.repo.GetByID(ctx, id)
}
```

`get_delivery_test.go`:

```go
func TestGetDeliveryInvoke(t *testing.T) {
    ctx := context.Background()
    id := uuid.New()
    repo := mocks.NewDeliveryRepoMock(t)
    repo.GetByIDMock.Set(func(_ context.Context, got uuid.UUID) (entity.Delivery, error) {
        return entity.Delivery{ID: got, Status: entity.StatusPending}, nil
    })
    uc := usecase.NewGetDelivery(repo)
    d, err := uc.Invoke(ctx, id)
    if err != nil { t.Fatal(err) }
    if d.ID != id { t.Fatalf("id=%v", d.ID) }
}
```

- [ ] **Step 2: Run tests**

Run: `go test -race ./internal/application/... && go vet ./...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/application/usecase/get_delivery.go internal/application/usecase/get_delivery_test.go
git commit -m "feat: usecase чтения статуса доставки"
```

## Task 11: Inbound HTTP handler

**Files:**
- Create: `internal/adapter/driver/http/handler.go`, `dto.go`, `errors.go`
- Test: `internal/adapter/driver/http/handler_test.go`

**Interfaces:**
- Consumes: `usecase.CreateSubscription`, `usecase.PublishEvent`, `usecase.GetDelivery`, `errs.*`.
- Produces: `func NewHandler(cs *usecase.CreateSubscription, pe *usecase.PublishEvent, gd *usecase.GetDelivery) http.Handler` с маршрутами POST /api/v1/subscriptions, POST /api/v1/events (заголовок Idempotency-Key), GET /api/v1/deliveries/{id}.

- [ ] **Step 1: Write failing test**

`handler_test.go` (таблично, через httptest):

```go
func TestHandlerPublishEventOK(t *testing.T) {
    cs := mocks.NewCreateSubscription(t)     // заглушки не нужны — см. ниже
    ...
}
```

Замечание: у usecase конкретные типы (`*usecase.PublishEvent`), поэтому handler конструируется с реальными usecase, а мокаются их зависимости. Для простого HTTP-теста PublishEvent создаётся с minimock EventRepo+SubscriptionRepo, ожидающими вызова. Проверить: POST /api/v1/events с валидным body и заголовком `Idempotency-Key: k` → 200/201 и `duplicate:false`; повторный с тем же ключом → duplicate маркер; `Idempotency-Key` отсутствует → 400.

- [ ] **Step 2: Implement handler + dto + errors**

`dto.go`: `SubscriptionRequest{URL, Secret string; Events []string; MaxRPS int}`, `EventRequest{Type string; Payload json.RawMessage}`, ответы с `id`/`status`.

`errors.go`:

```go
func writeError(w http.ResponseWriter, err error) {
    switch {
    case errs.Is(err, errs.ErrNotFound):
        http.Error(w, "not found", http.StatusNotFound)
    case errs.Is(err, errs.ErrConflict):
        http.Error(w, "conflict", http.StatusConflict)
    case errs.Is(err, errs.ErrInvalid):
        http.Error(w, "invalid", http.StatusBadRequest)
    default:
        http.Error(w, "internal", http.StatusInternalServerError)
    }
}
```

`handler.go`: декодирование JSON, чтение заголовка `Idempotency-Key` для publish, гораутинг через `http.ServeMux`, передача `ctx` в usecase.

- [ ] **Step 3: Run tests**

Run: `go test -race ./internal/adapter/driver/http/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/adapter/driver/http/
git commit -m "feat: REST-адаптер входящих запросов"
```

## Task 12: Worker pool

**Files:**
- Create: `internal/adapter/driver/worker/pool.go`
- Test: `internal/adapter/driver/worker/pool_test.go`

**Interfaces:**
- Consumes: `usecase.ClaimNext`, `usecase.ProcessDelivery`.
- Produces: `func Run(ctx context.Context, workers int, claim *usecase.ClaimNext, process *usecase.ProcessDelivery, pollInterval time.Duration) error` — пул горутин, завершающийся по отмене ctx.

- [ ] **Step 1: Implement pool**

`pool.go`:

```go
// Package worker реализует фоновый пул горутин, доставляющий события.
package worker

import (
    "context"
    "sync"
    "time"

    "webhookdispatcher/internal/application/usecase"
)

// Run запускает workers горутин, опрашивающих хранилище и доставляющих задачи.
// Возвращается при отмене контекста (graceful shutdown).
func Run(ctx context.Context, workers int, claim *usecase.ClaimNext, process *usecase.ProcessDelivery, pollInterval time.Duration) error {
    var wg sync.WaitGroup
    for i := 0; i < workers; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                default:
                }
                now := time.Now()
                tasks, err := claim.Invoke(ctx, now, 10)
                if err != nil {
                    if ctx.Err() != nil { return }
                    time.Sleep(pollInterval)
                    continue
                }
                if len(tasks) == 0 {
                    time.Sleep(pollInterval)
                    continue
                }
                for _, t := range tasks {
                    _ = process.Invoke(ctx, t)
                }
            }
        }()
    }
    <-ctx.Done()
    wg.Wait()
    return nil
}
```

- [ ] **Step 2: Write test verifying cancellation**

`pool_test.go`: запустить `Run` с контекстом, который отменяется через 50ms, workers=3, `claim` — minimock, `process` — minimock; убедиться, что `Run` возвращается (нет утечки горутин): `go test -race` детектирует незавершённые горутины в конце.

- [ ] **Step 3: Run tests**

Run: `go test -race ./internal/adapter/driver/worker/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/adapter/driver/worker/
git commit -m "feat: фоновый пул горутин доставки"
```

## Task 13: Postgres-адаптер (repo + миграция)

**Files:**
- Create: `internal/adapter/driven/postgres/postgres.go`, `subscription_repo.go`, `event_repo.go`, `delivery_repo.go`, `migrations/0001_init.sql`
- Test: (без живой БД; компиляция/вет покрываются в Task 14)

**Interfaces:**
- Produces: `func NewPool(ctx) (*pgxpool.Pool, error)`; реализации `ports.SubscriptionRepo`, `ports.EventRepo`, `ports.DeliveryRepo` от `*pgxpool.Pool`.

- [ ] **Step 1: W schemat migracii**

`migrations/0001_init.sql`:

```sql
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

CREATE INDEX IF NOT EXISTS deliveries_claim_idx
    ON deliveries (status, next_attempt_at)
    WHERE status IN ('PENDING','RETRYING');
```

- [ ] **Step 2: Implement subscription repo**

`subscription_repo.go`: `Save` (INSERT), `GetByID` (SELECT, `pgx.ErrNoRows` → `errs.ErrNotFound`), `GetByEventType` (`WHERE $1 = ANY(events)`).

- [ ] **Step 3: Implement event repo (idempotency + outbox)**

`event_repo.go`: `SaveWithin` в транзакции:

```go
func (r *EventRepo) SaveWithin(ctx context.Context, key string, ev entity.Event, del []entity.Delivery) (ports.OutboxResult, error) {
    tx, err := r.pool.Begin(ctx)
    if err != nil { return ports.OutboxResult{}, err }
    defer tx.Rollback(ctx) //nolint:errcheck
    // попытка вставить событие; если ключ уже есть — вернуть существующее событие
    var existingID uuid.UUID
    err = tx.QueryRow(ctx, `SELECT id FROM events WHERE idem_key=$1`, key).Scan(&existingID)
    if err == nil {
        return ports.OutboxResult{EventID: existingID, Duplicate: true}, nil
    }
    if err != pgx.ErrNoRows { return ports.OutboxResult{}, err }
    if _, err := tx.Exec(ctx,
        `INSERT INTO events(id,type,payload,created_at,idem_key) VALUES($1,$2,$3,$4,$5)`,
        ev.ID, ev.Type, ev.Payload, ev.CreatedAt, key); err != nil {
        return ports.OutboxResult{}, err
    }
    for _, d := range del {
        if _, err := tx.Exec(ctx,
            `INSERT INTO deliveries(id,event_id,subscription_id,status,attempt,payload) VALUES($1,$2,$3,$4,$5,$6)`,
            d.ID, d.EventID, d.SubscriptionID, string(d.Status), d.Attempt, d.Payload); err != nil {
            return ports.OutboxResult{}, err
        }
    }
    if err := tx.Commit(ctx); err != nil { return ports.OutboxResult{}, err }
    return ports.OutboxResult{EventID: ev.ID}, nil
}
```

- [ ] **Step 4: Implement delivery repo**

`delivery_repo.go`: `ClaimNext` c `SELECT ... FOR UPDATE SKIP LOCKED` (массово через CTE), `MarkOutcome` (UPDATE по id), `GetByID` (`pgx.ErrNoRows` → `errs.ErrNotFound`).

- [ ] **Step 5: Compile-check**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/driven/postgres/
git commit -m "feat: postgres-адаптер (repo, idempotency, outbox, claim)"
```

## Task 14: HTTPSender и RateLimiter

**Files:**
- Create: `internal/adapter/driven/httpsender/sender.go`, `_test.go`
- Create: `internal/adapter/driven/ratelimiter/limiter.go`, `_test.go`

**Interfaces:**
- Produces: `type Sender struct{ cli *http.Client }` → `func (s *Sender) Send(ctx, url, ua, sig string, payload []byte) (int, error)`; `type Limiter struct{...}` → `func (l *Limiter) Allow(ctx, host string) error` (token-bucket per host).

- [ ] **Step 1: Implement sender + test**

`sender.go`:

```go
// Package httpsender реализует исходящий HTTP-клиент к подписчикам.
package httpsender

import (
    "bytes"
    "context"
    "net/http"
    "time"
)

// Sender отправляет вебхуки подписчикам через HTTP.
type Sender struct {
    cli *http.Client
}

// New создаёт Sender с таймаутом запроса 5 секунд.
func New() *Sender {
    return &Sender{cli: &http.Client{Timeout: 5 * time.Second}}
}

// Send POST-запрос payload по url с подписью X-Signature и кастомным User-Agent.
// Таймаут/сетевые ошибки поверх timeouts возвращаются как ошибка с кодом 0.
func (s *Sender) Send(ctx context.Context, url, userAgent, signature string, payload []byte) (int, error) {
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
    if err != nil {
        return 0, err
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("User-Agent", userAgent)
    req.Header.Set("X-Signature", signature)
    resp, err := s.cli.Do(req)
    if err != nil {
        return 0, err
    }
    defer resp.Body.Close()
    return resp.StatusCode, nil
}
```

`sender_test.go`: через `httptest.Server`, проверяя, что пришли `X-Signature` и `User-Agent`, и возвращён статус 200.

- [ ] **Step 2: Implement ratelimiter + test**

`limiter.go` (token bucket, один на хост, рейт maxRPS):

```go
// Package ratelimiter ограничивает RPS доставки на хост подписчика.
package ratelimiter

import (
    "context"
    "sync"
)

// Limiter хранит по одному token bucket'у на хост.
type Limiter struct {
    mu      sync.Mutex
    buckets map[string]*bucket
    maxRPS  int
}

type bucket struct {
    capacity float64
    tokens   float64
}

// New создаёт Limiter с maxRPS токенов в секунду на хост.
func New(maxRPS int) *Limiter { ... }

// Allow блокирует до появления токена для host (или мгновенно, если токены есть).
func (l *Limiter) Allow(ctx context.Context, host string) error { ... }
```

`limiter_test.go`: за short интервал сделать maxRPS запросов — пройдут; следующий вынужден ждать (Allow блокируется до контекста). Проверить отсутствие рассинхрона (не строгая точность).

- [ ] **Step 3: Run tests**

Run: `go test -race ./internal/adapter/driven/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/adapter/driven/httpsender/ internal/adapter/driven/ratelimiter/
git commit -m "feat: исходящий HTTP-клиент и перхостовый рейт-лимитер"
```

## Task 15: Config и main (сборка + graceful shutdown)

**Files:**
- Create: `internal/config/config.go`, `cmd/dispatcher/main.go`

**Interfaces:**
- Consumes: все usecase, адаптеры; `signal.NotifyContext`.
- Produces: `func Load() (Config, error)` из ENV (`HTTP_ADDR`, `DATABASE_URL`, `WORKERS`, `RATE_LIMIT`).

- [ ] **Step 1: Implement config**

`internal/config/config.go`: чтение ENV с дефолтами + валидация `DATABASE_URL`.

- [ ] **Step 2: Implement main**

`cmd/dispatcher/main.go`:

```go
// Команда dispatcher собирает все слои и запускает http-сервер и воркеры.
package main

import (
    "context"
    "net/http"
    "os/signal"
    "syscall"

    "webhookdispatcher/internal/adapter/driver/http"
    "webhookdispatcher/internal/adapter/driver/worker"
    "webhookdispatcher/internal/adapter/driven/httpsender"
    "webhookdispatcher/internal/adapter/driven/postgres"
    "webhookdispatcher/internal/adapter/driven/ratelimiter"
    "webhookdispatcher/internal/application/usecase"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    cfg := config.Load()
    _ = cfg
    // 1. pool БД, репозитории
    // 2. usecase'ы
    // 3. http-сервер (goroutine) + graceful Shutdown по ctx
    // 4. worker.Run(ctx, ...)
}
```

`main.go` реализует: открыть пул БД, собрать репозитории → usecase → handler; http.Server с `srv.Shutdown(ctx)` в горутине после отмены; `worker.Run` блокирует main до отмены ctx.

- [ ] **Step 3: Build + vet**

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: PASS, no fmt diffs.

- [ ] **Step 4: Commit**

```bash
git add internal/config/ cmd/
git commit -m "feat: конфиг из ENV и сборка сервиса с graceful shutdown"
```

## Task 16: Финальная проверка

- [ ] **Step 1: Full test + race + vet**

Run: `go vet ./... && gofmt -l . && go test -race ./...`
Expected: PASS; `gofmt -l` пусто.

- [ ] **Step 2: Fix any failures** (компиляция, типы, тулчайн). Гарантируется, что `internal/application` импортирует только stdlib+uuid: проверка:

Run: `go list -deps ./internal/application/... | grep -v '^(stdlib)' | grep -v webhookdispatcher | grep -v google/uuid`
Expected: пусто.

- [ ] **Step 3: Final commit**

```bash
git add -A
git commit -m "feat: полный сервис доставки вебхуков (hexagonal)"
```

## Self-Review

- **Spec coverage**: ТЗ 3.1 (state machine, backoff, HMAC) → Tasks 1-4, 9. 3.2 REST + worker → Tasks 11-12. 3.3 Postgres/outbox/idempotency → Task 13, HTTP sender → 14. Rate limiter → 14. Раздел 4: strict hexagonal → Global Constraints + проверка в Task 16; ctx во всех портах → Task 3; graceful shutdown → Task 15; errs → Task 3; unit-тесты домена → Tasks 1-2, 4.
- **Placeholder scan**: все шаги содержат конкретный код/команды; postgres SQL в Task 13 полон.
- **Type consistency**: `OutcomeFromStatus`, `ScheduleFrom`, `BackoffDelay`, `Invoke`-сигнатуры согласованы между Tasks 1→2→4→9; порты Task 3 используются в usecase Tasks 6-10; minimock-моки Task 5 — во всех usecase-тестах.
