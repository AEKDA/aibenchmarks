# Deferred Work

Собранные при ревью находки, не входящие в рамки текущей истории.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-1-service-skeleton.md`
  summary: Обязательные debug-эндпоинты `/live /ready /version /metrics /dependencies` и именование вместо `/healthz`.
  evidence: Ozon-конвенции microservices.md требуют их; спека 1.1 намеренно оставила только `GET /healthz`.
- source_spec: `_bmad-output/implementation-artifacts/spec-1-1-service-skeleton.md`
  summary: Ранняя валидация формата `WHD_HTTP_ADDR` при старте вместо позднего фейла в `net.Listen`.
  evidence: Конфиг проверяет обязательные поля и таймауты, но не адрес; невалидный адрес уходит в late failure.
- source_spec: `_bmad-output/implementation-artifacts/spec-1-1-service-skeleton.md`
  summary: Тест-покрытие composition root `main.run`, guard «Run called more than once» и failure-пути `net.Listen`.
  evidence: Эти ветки не упражняются ни одним тестом.
- source_spec: `_bmad-output/implementation-artifacts/spec-1-1-service-skeleton.md`
  summary: Чистка мёртвого экспортированного API: `Server.ShutdownTimeout()` и зеркальное поле `addr`.
  evidence: Getter нигде не используется вне структуры.
