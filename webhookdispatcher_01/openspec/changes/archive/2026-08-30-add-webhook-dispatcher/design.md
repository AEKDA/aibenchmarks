## Context

Репозиторий пуст: сервис создаётся с нуля на Go. Мотивация — см. `proposal.md`, требуемое поведение — см. `specs/webhook-subscriptions/spec.md`, `specs/event-ingestion/spec.md`, `specs/webhook-delivery/spec.md`.

Жёсткие внешние ограничения, заданные заказчиком и формирующие всю структуру:

- Строгая гексагональная архитектура: домен называется `application`, все порты — в `ports`, адаптеры разделены на `driver` (вызывают usecase'ы) и `driven` (вызываются из usecase'ов).
- `internal/application` импортирует только стандартную библиотеку Go и `github.com/google/uuid`. Пакеты адаптеров не импортируют друг друга.
- Первый аргумент каждого метода порта, usecase'а и адаптера — `ctx context.Context`.
- Usecase'ы и инструкции именуются действием, точка входа — метод `Invoke(...)`.
- Graceful shutdown через `signal.NotifyContext`; сборка и тесты проходят под `-race`.

Разрешённые в ходе планирования неоднозначности (подтверждены заказчиком):

- `429 Too Many Requests` считается **повторяемым**; прочие 4xx — неповторяемые и ведут сразу в `DEAD_LETTER`.
- `{id}` в `GET /api/v1/deliveries/{id}` — идентификатор **доставки**, а не события.

## Goals / Non-Goals

**Goals:**

- Такая раскладка пакетов, при которой нарушение гексагональных правил ловится компилятором и автоматической проверкой импортов, а не ревью.
- Ровно одна попытка доставки на одну «созревшую» задачу, без двойной отправки при нескольких воркерах и без потери задач при аварийной остановке.
- Чистый, тестируемый без моков домен: state machine, backoff и HMAC — чистые функции/методы над сущностями.
- Идемпотентность приёма событий, обеспеченная ограничением БД, а не гонкой «проверил-потом-вставил».

**Non-Goals:**

- Аутентификация и авторизация вызывающих REST API (сервис считается внутренним, за периметром).
- Управление подписками кроме создания (нет update/delete/list).
- Ручной повтор доставок из `DEAD_LETTER`, UI, метрики и трейсинг.
- Горизонтальное шардирование очереди и вынос очереди во внешний брокер: очередь живёт в PostgreSQL.
- Ограничение размера payload и глобальный входящий rate limiting.

## Decisions

### D1. Раскладка пакетов

```
cmd/webhookdispatcher/main.go        — composition root, wiring, signal.NotifyContext
internal/application/
  entity/       Subscription, Event, Delivery, DeliveryStatus, AttemptResult
  errs/         ErrNotFound, ErrConflict, ErrInvalidInput, ErrAlreadyExists + helpers
  ports/        driver-порты (входящие интерфейсы usecase'ов) и driven-порты (репозитории, sender, limiter, clock)
  usecase/      CreateSubscription, PublishEvent, GetDelivery, ProcessDelivery
  instruction/  SignPayload, ScheduleRetry (общий код между usecase'ами)
internal/adapter/driver/
  http/         handlers, router, dto, маппинг доменных ошибок в HTTP-коды
  worker/       пул горутин, поллер, вызов usecase.ProcessDelivery
internal/adapter/driven/
  postgres/     репозитории Subscription/Event/Delivery, транзакции, миграции
  httpsender/   HTTP-клиент доставки
  ratelimiter/  per-host лимитер
internal/config/                     — чтение конфигурации из окружения
migrations/                          — SQL-миграции
```

Альтернатива — плоский `internal/adapters` без разделения driver/driven — отвергнута: разделение задано требованиями и делает направление зависимостей видимым по пути файла.

Правило импортов проверяется тестом-стражем (`go list -deps` над `./internal/application/...`, падает при любом импорте вне stdlib и `github.com/google/uuid`), чтобы нарушение ловилось в CI, а не глазами.

### D2. Границы портов

Driver-порты (то, что вызывают входящие адаптеры) — интерфейсы usecase'ов; HTTP-хендлеры и воркер зависят от интерфейса, не от конкретной структуры, поэтому адаптеры не связаны друг с другом.

Driven-порты:

- `SubscriptionRepository` — `Save(ctx, sub)`, `FindByEventType(ctx, eventType)`.
- `EventRepository` / атомарная запись — `SaveEventWithDeliveries(ctx, event, deliveries)`, `FindByIdempotencyKey(ctx, key)`.
- `DeliveryRepository` — `GetByID(ctx, id)`, `ClaimReady(ctx, limit, now)`, `Update(ctx, delivery)`, `ReleaseStale(ctx, olderThan)`.
- `WebhookSender` — `Send(ctx, target)` возвращает доменный `AttemptResult` (код ответа либо признак транспортной ошибки/таймаута), а не `*http.Response`: домен не должен знать про `net/http`.
- `RateLimiter` — `Wait(ctx, host, rps) error`.
- `Clock` — `Now(ctx)`; подменяется в тестах, чтобы проверять созревание `RETRYING` без ожидания реального времени.

`SaveEventWithDeliveries` выбран вместо явного порта `TxManager` с `Begin/Commit`: явная транзакция протекла бы в домен деталями SQL-драйвера. Транзакция целиком остаётся внутри postgres-адаптера, порт даёт атомарность как контракт. Цена — менее гибкая композиция транзакций; при нынешнем наборе usecase'ов её достаточно.

### D3. Модель данных и Transactional Outbox

Таблицы:

- `subscriptions(id uuid pk, url text, secret text, events text[], max_rps int, active bool, created_at timestamptz)`.
- `events(id uuid pk, idempotency_key text not null, type text, payload jsonb, created_at timestamptz)` + `unique index on (idempotency_key)`.
- `deliveries(id uuid pk, event_id uuid fk, subscription_id uuid fk, status text, attempt_count int, next_attempt_at timestamptz null, last_status_code int null, last_error text null, locked_at timestamptz null, created_at, updated_at)` + индекс `(status, next_attempt_at)`.

`deliveries` и есть outbox: событие и его задачи доставки пишутся одной транзакцией, поэтому опубликованное событие всегда имеет полный набор доставок.

### D4. Идемпотентность через ограничение БД

Порядок: `INSERT ... ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`. Если строка не вернулась — ключ уже занят, читаем существующее событие и возвращаем его `id`. Проверка «сначала SELECT, потом INSERT» отвергнута: она проигрывает гонку конкурентных запросов с одним ключом. Ограничение уникальности — единственный арбитр.

### D5. Захват готовых доставок

`ClaimReady` выполняет один запрос:

```sql
UPDATE deliveries SET status='SENDING', locked_at=now()
WHERE id IN (
  SELECT id FROM deliveries
  WHERE status='PENDING' OR (status='RETRYING' AND next_attempt_at <= now())
  ORDER BY next_attempt_at NULLS FIRST
  FOR UPDATE SKIP LOCKED LIMIT $1
) RETURNING ...
```

`SKIP LOCKED` даёт «ровно один обработчик на доставку» без внешнего лока и без advisory-локов. Альтернатива с `LISTEN/NOTIFY` даёт меньшую задержку, но требует отдельного соединения и всё равно нуждается в поллинге как страховке — не оправдано на текущем объёме.

Аварийная остановка оставляет доставки в `SENDING` с проставленным `locked_at`. `ReleaseStale` при старте и периодически возвращает в `RETRYING` те, что висят в `SENDING` дольше порога (по умолчанию 1 минута — заметно больше таймаута отправки в 5 секунд). Это даёт at-least-once: подписчик обязан быть готов к повторной доставке одного события.

### D6. Домен: state machine, backoff, подпись

- `entity.Delivery` держит переходы в одном месте: `MarkSending()`, `MarkDelivered(code)`, `MarkRetrying(next time.Time, code, err)`, `MarkDeadLetter(reason)`. Каждый метод проверяет допустимость перехода по таблице разрешённых пар и возвращает `errs.ErrConflict` при нарушении. Валидация перехода живёт в сущности, а не в usecase'е, — иначе её пришлось бы дублировать в каждом вызывающем.
- Backoff: `base * 2^attempt`, затем джиттер в пределах ±50% от полученного значения. Источник случайности передаётся как `func() float64`, поэтому тест получает детерминированный результат без глобального `rand`. Расчёт — чистая функция в `application`, `math/rand` из стандартной библиотеки допустим.
- Подпись: `instruction.SignPayload.Invoke(ctx, body, secret) (string, error)` возвращает `sha256=<hex>` через `crypto/hmac` + `crypto/sha256` + `encoding/hex`. Вынесена в `instruction`, потому что нужна и при отправке, и в тестах контракта.
- Классификация ответа (`2xx` → успех, `429`/`5xx`/таймаут → повтор, прочие `4xx`/`3xx` → dead letter) — чистая функция в домене над `entity.AttemptResult`; `httpsender` только заполняет результат, решение принимает домен.

### D7. Usecase ProcessDelivery

Один usecase собирает шаг обработки: получить подписку, собрать тело, дождаться лимитера, подписать, отправить, классифицировать результат, перевести статус и сохранить. Общая часть «посчитать задержку и перевести в RETRYING либо в DEAD_LETTER по исчерпании лимита» вынесена в `instruction.ScheduleRetry`.

Ошибка одной доставки никогда не роняет воркер: usecase возвращает ошибку, воркер логирует её и берёт следующую задачу.

### D8. Rate limiter

Реализация — `golang.org/x/time/rate` с `map[host]*rate.Limiter` под `sync.Mutex` в `driven/ratelimiter`. Ключ — хост из URL подписки, чтобы несколько подписок на один хост делили ограничение и сервис не устраивал DDoS. `Wait(ctx, ...)` уважает отмену контекста, поэтому shutdown не блокируется ожиданием токена. Лимитер живёт в процессе; при нескольких инстансах сервиса эффективный RPS на хост умножается на число инстансов — принято осознанно, распределённый лимитер вне объёма.

### D9. HTTP-слой

Роутинг — `net/http.ServeMux` с паттернами Go 1.22 (`POST /api/v1/events`, `GET /api/v1/deliveries/{id}`): внешний роутер не даёт ничего, что нужно трём эндпоинтам. DTO живут в `driver/http` и не переиспользуются доменом. Единый `writeError(w, err)` разбирает ошибку через `errors.Is` по `errs.*` и отдаёт `404/409/400/500`; тело `500` содержит фиксированный текст, детали уходят только в лог.

### D10. Ошибки

`errs` объявляет сентинелы (`ErrNotFound`, `ErrConflict`, `ErrInvalidInput`, `ErrAlreadyExists`). Оборачивание — `fmt.Errorf("...: %w", err)` из стандартной библиотеки (внешний пакет ошибок в `application` не тянем, требование `%w`-обёртки этим выполняется), сравнение — `errors.Is`. Адаптер postgres переводит `pgx.ErrNoRows` в `errs.ErrNotFound`, а нарушение уникальности — в `errs.ErrAlreadyExists`; наружу драйверные ошибки не протекают.

### D11. Тесты

- Unit без внешних зависимостей: state machine (все разрешённые и запрещённые переходы), backoff (границы джиттера, положительность, разброс), HMAC (формат, детерминированность, чувствительность к телу и секрету), классификация ответов.
- Usecase-тесты на ручных фейках портов (интерфейсы узкие, генератор моков не нужен).
- HTTP-хендлеры через `httptest`, включая обязательность `Idempotency-Key` и маппинг ошибок.
- Тест-страж импортов домена (D1).
- Postgres-тесты (идемпотентность, атомарность, `ClaimReady` без дублей) требуют реальной БД и помечаются `testing.Short()`, чтобы `go test -short ./...` проходил без инфраструктуры.
- Весь прогон — под `-race`.

## Risks / Trade-offs

- **Поллинг БД создаёт постоянную нагрузку и задержку до интервала опроса** → интервал опроса и размер батча конфигурируются; запрос опирается на индекс `(status, next_attempt_at)`; `SKIP LOCKED` не даёт воркерам конкурировать за одни строки.
- **At-least-once: подписчик может получить одну доставку дважды** (например, ответ 200 потерян после отправки, доставка вернулась из зависшего `SENDING`) → в теле доставки передаётся стабильный `event_id`, чтобы подписчик мог дедуплицировать; ограничение фиксируется в документации API.
- **Порог `ReleaseStale` слишком мал → доставка отправится повторно, пока первая ещё в полёте** → порог (1 минута) многократно больше таймаута отправки (5 секунд).
- **Секреты подписок хранятся в БД в открытом виде** → они не возвращаются через API и не попадают в логи; шифрование на стороне БД вне объёма и отмечено как известное ограничение.
- **In-process rate limiter не держит лимит при нескольких инстансах** → см. D8; при переходе к нескольким инстансам понадобится общий лимитер.
- **`SaveEventWithDeliveries` вместо явного менеджера транзакций ограничивает композицию** → достаточно для текущих usecase'ов; при появлении сценариев на несколько агрегатов порт расширяется, домен не меняется.
- **Неповторяемая трактовка 4xx маскирует опечатку в URL подписчика как быстрый `DEAD_LETTER`** → `last_status_code` и `last_error` сохраняются и видны через `GET /api/v1/deliveries/{id}`.

## Migration Plan

Новый сервис, миграции данных нет. Развёртывание: применить SQL-миграции из `migrations/` (таблицы и индексы создаются с нуля), затем запустить сервис с переменными окружения (DSN PostgreSQL, адрес HTTP, размер пула воркеров, интервал опроса, размер батча). Откат: остановить сервис; схема принадлежит только ему, поэтому откат — удаление его таблиц; частичный откат кода не требуется, так как внешних потребителей схемы нет.

## Open Questions

- Нужен ли фиксированный верхний потолок задержки повтора (например, 30 секунд)? При 5 попытках и base 1s максимум и так около 16 секунд, поэтому вопрос не влияет ни на спецификации, ни на разбиение задач и может быть закрыт позже, если лимит попыток вырастет.
