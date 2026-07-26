# Store — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-26

### #62, #71 — туннель-пул как форма туннеля и встроенная запись

**Changed:** `internal/store/{model,migrate,tunnels}.go` — `source = pool`, состав серверов и время обхода в `Tunnel`; миграция 3 (состав) и 4 (колонка `builtin`). Порядок серверов в `Pool` стал значимым: это приоритет отбора в конфиг.
**New surface:** `UpdateTunnelPool`, `EnsureBuiltinPool` (возвращает, завела она пул или признала существующий), `PoolServer` с полем `Misses`.
**Beware:** миграция 4 — `ALTER TABLE ADD COLUMN`, таблица не пересобирается, внешние ключи целы; проверено на стенде на БД с живыми туннелями, пиром и правилом. Правка `pool` в обход слоя lists переназначает теги участников группы и перезапускает sing-box.

## 2026-07-25

### #2 — схема SQLite, миграции и доступ к данным

**Changed:** `docs/02-data-model.md` — дописаны владение приоритетом, чистка `peer_ids`, key/value-настройки, обязательность `PRAGMA foreign_keys`. `go.mod` — первая зависимость.
**New surface:** `internal/store` — CRUD четырёх сущностей, `ReorderRules`, `Snapshot`, сентинелы ошибок.
**Beware:** соединение одно (`SetMaxOpenConns(1)`) — запрос через `s.db` внутри транзакции вешает демон.
