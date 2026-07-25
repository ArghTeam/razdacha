# Store — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-25

### #2 — схема SQLite, миграции и доступ к данным

**Changed:** `docs/02-data-model.md` — дописаны владение приоритетом, чистка `peer_ids`, key/value-настройки, обязательность `PRAGMA foreign_keys`. `go.mod` — первая зависимость.
**New surface:** `internal/store` — CRUD четырёх сущностей, `ReorderRules`, `Snapshot`, сентинелы ошибок.
**Beware:** соединение одно (`SetMaxOpenConns(1)`) — запрос через `s.db` внутри транзакции вешает демон.
