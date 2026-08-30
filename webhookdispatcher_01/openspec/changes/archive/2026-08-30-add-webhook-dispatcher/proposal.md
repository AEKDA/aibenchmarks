## Why

Внутренним системам нужен единый способ уведомлять внешних подписчиков о событиях, не встраивая в каждый сервис логику ретраев, подписи и защиты от перегрузки. Сейчас такого сервиса нет — репозиторий пуст, поэтому изменение вводит сервис доставки вебхуков целиком, с нуля.

## What Changes

- Новый Go-сервис `webhookdispatcher` на строгой гексагональной архитектуре: домен в `internal/application` (entity, ports, usecase, instruction, errs), адаптеры разделены на `driver` (входящие) и `driven` (исходящие).
- REST API v1: регистрация подписки, публикация события с обязательным `Idempotency-Key`, чтение статуса доставки.
- Идемпотентная приёмка событий: повторный запрос с тем же `Idempotency-Key` возвращает ID исходного события и не создаёт дублирующих доставок.
- Transactional Outbox: событие и порождённые им задачи доставки записываются в PostgreSQL одной транзакцией.
- Фоновый воркер-пул: опрашивает хранилище на `PENDING` и созревшие `RETRYING` доставки, забирает их атомарно и отправляет.
- State machine доставки `PENDING → SENDING → DELIVERED | RETRYING → DEAD_LETTER` с exponential backoff + jitter (base 1s, максимум 5 попыток).
- Подпись полезной нагрузки HMAC-SHA256 в заголовке `X-Signature: sha256=<hex_digest>` от тела запроса и секрета подписки.
- HTTP-отправитель с таймаутом контекста 5 секунд и кастомным `User-Agent`.
- Per-host rate limiter, ограничивающий RPS на целевой хост подписчика (`max_rps` из подписки).
- Типизированные доменные ошибки (`errs.ErrNotFound`, `errs.ErrConflict`, …) и их маппинг в HTTP-статусы во входящем адаптере.
- Graceful shutdown HTTP-сервера и воркеров через `signal.NotifyContext`.
- Unit-тесты чистого домена: state machine, расчёт backoff, HMAC-подпись; сборка и тесты под `-race`.

## Capabilities

### New Capabilities
- `webhook-subscriptions`: регистрация и хранение подписчиков (URL, secret, список событий, max_rps), а также выбор подписчиков, которым адресовано событие.
- `event-ingestion`: приём событий через REST с гарантией идемпотентности по `Idempotency-Key` и транзакционным созданием события вместе с задачами доставки (Transactional Outbox).
- `webhook-delivery`: жизненный цикл доставки (state machine), политика повторов с exponential backoff и jitter, HMAC-SHA256 подпись, per-host rate limiting, воркер-пул и чтение статуса доставки.

### Modified Capabilities
_Нет — проект новый, существующих спецификаций нет._

## Impact

- **Новый код:** `cmd/webhookdispatcher`, `internal/application/{entity,ports,usecase,instruction,errs}`, `internal/adapter/driver/{http,worker}`, `internal/adapter/driven/{postgres,httpsender,ratelimiter}`, `internal/config`.
- **Внешние API:** новые публичные эндпоинты `POST /api/v1/subscriptions`, `POST /api/v1/events`, `GET /api/v1/deliveries/{id}`; исходящие POST-запросы на URL подписчиков с заголовками `X-Signature`, `User-Agent`.
- **Инфраструктура:** требуется PostgreSQL; добавляются SQL-миграции для таблиц `subscriptions`, `events`, `deliveries` и уникального индекса по `idempotency_key`.
- **Зависимости (допущения, зафиксированные для реализации):** `github.com/google/uuid` (допустима и в домене), `github.com/jackc/pgx/v5` для PostgreSQL, `golang.org/x/time/rate` для лимитера — обе только в адаптерах. Роутинг — на `net/http.ServeMux` из стандартной библиотеки (Go 1.22+ поддерживает паттерны с переменными пути), внешний роутер не вводится.
- **Ограничение архитектуры:** пакет `application` импортирует только стандартную библиотеку и `uuid`; пакеты адаптеров не импортируют друг друга.
