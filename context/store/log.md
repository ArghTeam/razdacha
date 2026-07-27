# Store — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-27

### #100 — WARP как source туннеля

**Changed:** миграция 7 — значение `source = "warp"` рядом с `wg_conf`, `url`, `pool`; тип туннеля остаётся `wireguard`.
**Beware:** это признак второго звена цепи по ADR 0012 — по нему решается, годится ли туннель для «туннель → WARP». Свойство хранится, а не выводится из содержимого конфига.

### #91 — что уже сообщили наружу

**Changed:** миграция 6 — колонка `notified_status` в `tunnel_checks`, метод `SetNotifiedStatus`.
**Beware:** держать это в памяти нельзя — перезапуск демона переобъявлял бы падение, про которое владельцу уже написали. Очередная проверка колонку не трогает: наблюдение и «о чём сообщили» — разное знание.

### #90 — настройки оповещений

**Changed:** `NotifyConfig`/`SaveNotifyConfig`, ключи `notify_*` в таблице `settings`.
**Beware:** токен лежит вне структуры `Settings`, как хеш пароля: внутри он переписывался бы из `PATCH /api/settings` и уезжал бы в `GET`. Включение без токена или чата отвергается — иначе настройка выглядит рабочей и молчит.

### #89 — таблица `tunnel_checks`

**Changed:** миграция 5, `SaveTunnelCheck`/`TunnelChecks`, настройка `tunnel_check_interval` с нижней границей 30 секунд.
**Beware:** отдельная таблица, а не колонки в `tunnels`: состояние проверки — наблюдение о туннеле, и в `Snapshot` ему делать нечего, оттуда генерируется конфиг sing-box. Задержка не хранится: она живёт минуты (ADR 0011).


## 2026-07-26

### #81 — режим панели и версия установки в настройках

**Changed:** `internal/store/install.go` — ключи `panel_public` и `installed_version` в `settings`, **вне `store.Settings`**: `SaveSettings` их не переписывает, в `GET /api/settings` они не попадают.
**New surface:** `PanelPublic` → `(public, saved, err)`, `SetPanelPublic`, `InstalledVersionAt`.
**Beware:** «ключа нет» нельзя сворачивать в «значение по умолчанию»: `false == false` читалось как «ничего не изменилось», и панель молча уходила из интернета.

### #62, #71 — туннель-пул как форма туннеля и встроенная запись

**Changed:** `internal/store/{model,migrate,tunnels}.go` — `source = pool`, состав серверов и время обхода в `Tunnel`; миграция 3 (состав) и 4 (колонка `builtin`). Порядок серверов в `Pool` стал значимым: это приоритет отбора в конфиг.
**New surface:** `UpdateTunnelPool`, `EnsureBuiltinPool` (возвращает, завела она пул или признала существующий), `PoolServer` с полем `Misses`.
**Beware:** миграция 4 — `ALTER TABLE ADD COLUMN`, таблица не пересобирается, внешние ключи целы; проверено на стенде на БД с живыми туннелями, пиром и правилом. Правка `pool` в обход слоя lists переназначает теги участников группы и перезапускает sing-box.

## 2026-07-25

### #2 — схема SQLite, миграции и доступ к данным

**Changed:** `docs/02-data-model.md` — дописаны владение приоритетом, чистка `peer_ids`, key/value-настройки, обязательность `PRAGMA foreign_keys`. `go.mod` — первая зависимость.
**New surface:** `internal/store` — CRUD четырёх сущностей, `ReorderRules`, `Snapshot`, сентинелы ошибок.
**Beware:** соединение одно (`SetMaxOpenConns(1)`) — запрос через `s.db` внутри транзакции вешает демон.
