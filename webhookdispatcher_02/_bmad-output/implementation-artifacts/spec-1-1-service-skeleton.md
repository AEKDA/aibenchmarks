---
title: 'Скелет сервиса с жёсткими границами слоёв'
type: 'feature'
created: '2026-08-30'
status: 'in-review' # draft | ready-for-dev | in-progress | in-review | done
baseline_commit: 'NO_VCS'
review_loop_iteration: 0
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-1-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Репозиторий пуст. Каждая следующая история Эпика 1 нуждается в одном и том же каркасе — сборке, конфигурации, жизненном цикле процесса и доменных ошибках, — а гексагональные границы слоёв без автоматической проверки размываются в первую же неделю.

**Approach:** Собрать минимальный работающий бинарь `cmd/dispatcher` с деревом каталогов из архитектуры, конфигурацией из окружения с валидацией при старте, graceful shutdown через `signal.NotifyContext` и `errgroup`, пакетом доменных ошибок и тестом, который падает при нарушении направления импортов.

## Boundaries & Constraints

**Always:**
- Пакеты `internal/application/**` импортируют только стандартную библиотеку и `github.com/google/uuid`.
- Driven-адаптер не импортирует другой driven-адаптер и не импортирует driver-адаптер; обратное тоже запрещено.
- Единственный пакет, импортирующий адаптеры, — `cmd/dispatcher`.
- Окружение читается только в `cmd/dispatcher`; префикс `WHD_`.
- Всё, что работает в фоне или обслуживает сеть, реализует `Run(ctx context.Context) error`, возвращающийся после полной остановки.
- Первый аргумент каждого метода с вводом-выводом или ожиданием — `ctx context.Context`.
- Ошибки оборачиваются через `%w`.

**Ask First:**
- Добавление любой зависимости сверх `github.com/google/uuid` и `golang.org/x/sync`.
- Введение слоя или каталога, отсутствующего в дереве архитектуры.
- Изменение перечня sentinel-ошибок.

**Never:**
- HTTP-эндпоинты домена (`/api/v1/**`) — они приходят с историями 1.3 и 1.5.
- Обращение к PostgreSQL, миграции, сущности предметной области — истории 1.2–1.5.
- Веб-фреймворк, ORM, DI-контейнер, генераторы кода.
- `os.Exit` и обработка сигналов где-либо, кроме composition root.
- Конфигурация CI: закрываем `-race` через `Makefile`, файл workflow не создаём.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Валидный старт | Заданы обязательные `WHD_*` | Процесс поднят, `GET /healthz` отвечает `200` | N/A |
| Отсутствует обязательная переменная | `WHD_DATABASE_URL` не задан | Процесс не стартует | Сообщение с именем переменной, код возврата 1 |
| Нарушен инвариант аренды | `WHD_LEASE_TIMEOUT=3s`, `WHD_SEND_TIMEOUT=5s` | Процесс не стартует | Сообщение с обоими значениями, код возврата 1 |
| Неразбираемая длительность | `WHD_SEND_TIMEOUT=5` | Процесс не стартует | Сообщение с именем переменной и значением, код 1 |
| Graceful shutdown | Процесс запущен, приходит `SIGTERM` | Приём новых запросов прекращён, начатые дообработаны, все `Run` вернулись | Код возврата 0 в пределах таймаута |
| Превышен таймаут завершения | Компонент не остановился вовремя | Процесс завершается принудительно | Код возврата 1, запись в лог |
| Нарушение границ слоёв | В `internal/application` добавлен импорт `github.com/jackc/pgx/v5` | `go test ./...` падает | Тест называет пакет и запрещённый импорт |

</frozen-after-approval>

## Code Map

Greenfield: Go-файлов в репозитории нет, создаётся всё. Планировочный контекст — в `context` фронтматтера, повторять его в задачах не нужно.

- `_bmad-output/implementation-artifacts/epic-1-context.md` — источник конвенций слоёв, имён портов, правил ошибок и стека. Read-only.
- `_bmad-output/planning-artifacts/architecture/architecture-webhookdispatcher_02-2026-08-30/ARCHITECTURE-SPINE.md` — AD-1 (границы), AD-4 (контекст), AD-5 (ошибки), AD-16 (конфигурация), AD-17 (жизненный цикл), AD-21 (логи). Read-only, справочно.
- `_bmad-output/planning-artifacts/epics.md` — AC истории 1.1 дословно. Read-only.
- Module path: `github.com/aenigmma/webhookdispatcher`.
- Целевая версия Go: 1.27.

## Tasks & Acceptance

**Execution:**
- [x] `go.mod` — объявить модуль `github.com/aenigmma/webhookdispatcher` на Go 1.27, добавить `github.com/google/uuid` и `golang.org/x/sync` — зафиксировать периметр зависимостей до появления кода. *Отклонение: `uuid` пока не в `go.mod` — `go mod tidy` убирает неиспользуемую зависимость. Появится с историей 1.2, где домен начнёт генерировать идентификаторы; периметр зависимостей закреплён правилом Ask First, а не строкой в `go.mod`.*
- [x] `internal/application/errs/errs.go` — объявить sentinel-ошибки `ErrNotFound`, `ErrConflict`, `ErrValidation`, `ErrInvalidTransition`, `ErrBlockedTarget` — единый словарь доменных ошибок, на который опираются все последующие истории.
- [x] `internal/application/ports/ports.go`, `internal/application/entity/doc.go`, `internal/application/instruction/doc.go`, `internal/application/usecase/doc.go` — создать пакеты с документирующим комментарием о назначении слоя — каталоги должны существовать до того, как в них начнут складывать код, иначе структура разъедется.
- [x] `internal/adapter/driver/http/server.go` — HTTP-сервер на `http.ServeMux` с единственным маршрутом `GET /healthz`, реализующий `Run(ctx) error` с корректным `Shutdown` — точка входа для историй 1.3 и 1.5 и проверяемый носитель graceful shutdown. *Маршруты вынесены в `Routes`, а `NewServer` принимает `http.Handler`: иначе таймаут завершения нечем проверить. Добавлены `Ready()` и `Addr()` для старта на свободном порту в тестах.*
- [x] `internal/config/config.go` — структура конфигурации, разбор переменных `WHD_*`, валидация обязательных полей и инварианта «таймаут аренды строго больше таймаута отправки» — единственное место чтения окружения.
- [x] `cmd/dispatcher/main.go` — composition root: загрузка и валидация конфигурации, `signal.NotifyContext`, запуск компонентов через `errgroup`, коды возврата — единственное место, где адаптеры связываются друг с другом.
- [x] `internal/arch/arch.go` + `internal/arch/arch_test.go` — правило направления импортов чистой функцией плюс обход дерева исходников через `go/parser` — превращает архитектурное соглашение в падающую сборку. *Отклонение от пути `internal/architecture_test.go`: каталог с одними тестовыми файлами Go не собирает, а вынесенное правило проверяется таблицей отдельно от обхода.*
- [x] `internal/config/config_test.go` — табличные тесты разбора и валидации по строкам матрицы I/O — фиксируют поведение стартовых проверок.
- [x] `internal/adapter/driver/http/server_test.go` — тест, что `Run` возвращается после отмены контекста и не оставляет горутин — доказывает graceful shutdown без ручной проверки.
- [x] `Makefile` — цели `build`, `test` (с `-race`), `vet`, `lint` — закрывает требование прогона с детектором гонок локально.
- [x] `README.md` — назначение сервиса, перечень переменных `WHD_*` со значениями по умолчанию, команды запуска — конфигурация иначе живёт только в коде.

**Acceptance Criteria:**
- Given чистый репозиторий, when выполняется `go build ./...`, then собирается бинарь `cmd/dispatcher` без ошибок.
- Given собранный сервис с валидной конфигурацией, when он запущен, then `GET /healthz` отвечает `200`.
- Given структура проекта, when она сверяется с архитектурой, then существуют `internal/application/{entity,errs,ports,instruction,usecase}` и `internal/adapter/{driver,driven}`.
- Given запущенный процесс, when ему отправлен `SIGINT` или `SIGTERM`, then все компоненты `Run` возвращаются, процесс завершается кодом 0, и ни одна горутина не остаётся активной.
- Given тесты проекта, when выполняется `make test`, then они проходят с флагом `-race` без обнаруженных гонок.
- Given пакет `internal/application`, when в него добавлен импорт вне разрешённого списка, then `go test ./...` падает с сообщением, называющим пакет и импорт.

## Design Notes

Тест границ слоёв — центральная ценность истории, поэтому он должен быть дешёвым в чтении: таблица правил, а не набор ad-hoc проверок. Ориентир формы:

```go
rules := []struct{ pkgPrefix string; allow func(string) bool }{
    {"internal/application", func(imp string) bool {
        return isStdlib(imp) || imp == "github.com/google/uuid"
    }},
    {"internal/adapter/driven", func(imp string) bool {
        return !strings.Contains(imp, "/internal/adapter/")
    }},
}
```

Конфигурация: разбор без внешних библиотек — `os.LookupEnv` плюс `time.ParseDuration` и `strconv`. Ошибки валидации собираются в список и печатаются все разом, а не по первой — иначе поднятие сервиса превращается в игру «угадай следующую переменную».

`healthz` намеренно не проверяет базу: на этом этапе её нет, а превращать liveness в readiness — отдельное решение.

## Verification

**Commands:**
- `go build ./...` — expected: сборка без ошибок
- `go vet ./...` — expected: пусто
- `make test` — expected: все тесты зелёные, гонок не обнаружено
- `go run ./cmd/dispatcher` с минимальным набором `WHD_*`, затем `curl -s -o /dev/null -w '%{http_code}' localhost:8080/healthz` — expected: `200`
- `kill -TERM <pid>` по запущенному процессу — expected: процесс завершается кодом 0 в пределах таймаута

**Manual checks (if no CLI):**
- Временно добавить `import "github.com/jackc/pgx/v5"` в файл внутри `internal/application` и убедиться, что `go test ./...` падает, затем откатить.
