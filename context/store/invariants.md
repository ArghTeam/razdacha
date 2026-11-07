---
type: invariants
title: Store
description: "SQLite: схема четырёх сущностей, миграции, хранение ключей пиров"
---

## Invariants
- Pre-migration `BackupAt` reads existing SQLite state without migrations or journal-mode changes. Its transaction includes committed WAL, preserves schema/installation metadata, and creates a standalone integrity-checked, synced `0600` snapshot. Exclusive reservation refuses existing files/symlinks; failures remove incomplete output. `InstalledVersionAt` uses the same read-only path and never creates an absent source.
- `PRAGMA foreign_keys = ON` ставится в DSN и действует на каждом соединении: в SQLite внешние ключи выключены по умолчанию, без прагмы `ON DELETE RESTRICT` на `rules.tunnel_id` не работает.
- Удаление туннеля, на который ссылается правило, отклоняется до обращения к БД и называет эти правила; схемный `RESTRICT` — вторая линия, а не единственная.
- Приоритет правил назначает слой хранения, а не вызывающий: добавление в конец, сжатие после удаления, порядок меняется только через `ReorderRules` с полным списком идентификаторов.
- Перестановка приоритетов идёт в две фазы через отрицательный диапазон — SQLite проверяет уникальный индекс построчно, прямая перестановка столкнулась бы на первом промежуточном значении.
- Версия схемы — `PRAGMA user_version`. Выпущенный шаг миграции не редактируется: изменение схемы это всегда новый шаг. БД версии новее кода не открывается.
- Файл БД и его каталог создаются с правами `0600`/`0700` до того, как за них возьмётся SQLite: `-wal` и `-shm` он создаёт с правами основного файла, а в БД лежат приватные ключи пиров.
- Состояние читается целиком (`Snapshot`, одна транзакция): конфиг sing-box генерируется целиком, чтение по частям пустило бы в конфиг правило на туннель, удалённый между запросами.
- Версия `modernc.org/sqlite` пинуется так, чтобы её `go`-директива не поднимала директиву в нашем `go.mod` (сейчас `v1.38.2`, `latest` требует `go 1.25`).
- Ошибки слоя — сентинелы `ErrNotFound`, `ErrInUse`, `ErrInvalid`, проверяются через `errors.Is`; тексты на русском, они доходят до UI как есть.
- Драйвер не отдаёт код ошибки через `errors.As`: нарушения UNIQUE и внешнего ключа распознаются по тексту (`isUniqueViolation`, `wrapRuleWriteErr`) — при смене версии драйвера это проверяется заново.
- Соединение к БД одно (`SetMaxOpenConns(1)`): запрос через `s.db` изнутри транзакции вешает демон, внутри транзакции работают только через `tx`.
- Selected peer IDs are validated and deduplicated inside the rule write transaction. Peer deletion atomically removes deleted/dangling IDs; a rule with no surviving selected client retains `selected`, becomes disabled and preserves policy references (ADR 0026). Empty selected rules can be edited only while disabled; reassignment does not activate them. Existing all-client rules are not guessed or migrated.
- Порядок серверов в `Tunnel.Pool` значим — это приоритет отбора в конфиг, и позиция в нём равна тегу участника группы `urltest`. Ведёт порядок слой lists (`MergePool`), слой store хранит список как есть; правка `pool` в обход слоя lists переназначает теги и перезапускает sing-box (ADR 0010).
- Встроенный туннель-пул один (ADR 0018): заводится сам при старте демона (`EnsureBuiltinPool`), выключенным, `source=pool`, `builtin=1`, имя `builtinPoolName` («Общий пул»). Идемпотентность по (`builtin`, `source=pool`), не по имени (переименовывают) и не по каталогу (адрес меняется в релизах). Через API пул не создаётся. `UpdateTunnel` колонки `builtin`/`country` не пишет — иначе запрет на удаление обходился бы через `PATCH`.
- Апгрейд с версии со странами (ADR 0017) сворачивает семь встроенных пулов в один (`collapseBuiltinPools`): выживает самый ранний по `created_at`, лишние удаляются. Удаление не молчит — ссылавшиеся правила сперва отвязываются по ADR 0013 (первое звено `tunnel_id` → NULL и правило выключается, чтобы не уронить генератор; второе звено `via_tunnel_id` → NULL, правило остаётся), всё в одной транзакции. Выживший нормализуется: колонка `country` очищается, имя приводится к `builtinPoolName` (только при реальном сворачивании — иначе переименованный пользователем единственный пул возвращал бы имя; занято чужим туннелем — остаётся прежним).
- Колонка `country` с ADR 0018 не используется (единый пул страну не различает, geo-IP убран), но остаётся в схеме — в SQLite колонки без нужды не роняют. Заполнена бывает только у страновых пулов до сворачивания.
- Настройки хранятся как key/value: дефолты живут в коде, отсутствующий ключ читается как дефолт, неизвестный игнорируется.
- Установочные факты — режим панели (`panel_public`) и версия установки (`installed_version`) — лежат в той же таблице, но не в `Settings`: как и хеш пароля, они не должны переписываться из `PATCH /api/settings` и попадать в `GET /api/settings`. Отсутствие ключа не читается как «приватный режим»: `PanelPublic` отдаёт его вторым значением, потому что версии до 0.2.1 ключа не писали, и «не выбирали» отличается от «выбрали приватный» (issue #81). Отсутствие `installed_version` — «версия неизвестна».

## Key entry points
- `internal/store/store.go` — `Open`/`Close`, DSN с прагмами, права на файл, хелпер транзакций.
- `internal/store/migrate.go` — список миграций и версия схемы.
- `internal/store/model.go` — четыре сущности и их валидация.
- `internal/store/snapshot.go` — полный снимок состояния для `internal/singbox`.
- `docs/02-data-model.md` — схема и обоснование решений по ней.

- Peer egress references are nullable, transactionally validated and protected by FK RESTRICT. Disabled targets remain selected. API-style peer patches read/write only supplied fields transactionally so rename/toggle cannot erase a concurrent exit choice. Backups retain the choice; existing clients migrate to shared rules (ADR 0028).

- Password reset atomically rotates a private random authentication generation and deletes sessions. Login captures hash and generation together after Argon2 admission, then inserts only if both still match in one SQLite statement. This prevents stale and same-hash/ABA verification from issuing sessions across independent connections/processes. Legacy hashes without a generation remain supported until reset; settings APIs neither expose nor overwrite authentication state. A reset after insertion revokes that session even if its cookie is delivered later. Already authorized requests are not retroactively canceled (#276, ADR 0009).

## Parallel client ingress (ADR 0032)

- Migration 13 is additive. Legacy peers retain WireGuard through an internal default-false `wireguard_disabled`; Reality defaults off. An enabled peer needs at least one method. Existing WG keys/addresses are preserved.
- Private singleton REALITY identity and per-peer UUID tables are separate from policy authority. Snapshots contain opaque identity/admission IDs and revisions only; live credentials are intersected with committed admissions under the publication gate. No background refresh admits a new or rotated credential.
- Peer/global Reality disable atomically replaces live bearers and advances revisions; delete cascades private credentials. Rotation requires a current positive revision (`ErrConflict` on stale requests). Re-enable requires explicit Apply.
- Full backups include private credentials. Normal CLI restore deletes Reality credentials and server identity and switches Reality off before replacing live state; WG keys/addresses remain unchanged. Explicit reconfiguration creates new credentials and requires profile reissue. Ordinary migration/upgrade never resets identity.

- Legacy full `UpdatePeer` rejects transport/global-enable changes when Reality is present, requiring the gated patch path. Ordinary rename/egress updates and WG-only full writes retain compatibility.

- Restore disables the global peer flag only when its WG method is disabled as Reality is stripped. Such clients remain editable while disabled and require explicit method selection plus configuration/Apply; every WG-enabled peer retains its enabled flag, keys and address.

- `RealityDelivery` reads one export-only transaction under the publication gate:
  saved settings, enabled peer/current credential revision and public server identity.
  It returns no private server key, initializes nothing and changes no admission.
  Missing/disabled/deleted material is refused. `RealityRuntime` remains unchanged;
  pending export never authorizes runtime users (ADR 0036).

- Staged CLI restore atomically deletes all panel sessions and rotates the private authentication generation before publication, preserving the restored password hash. Saved cookies and pre-restore login verification never become valid again. Automatic pool filtering admits Shadowsocks only with a recognized encrypted cipher and well-formed plain/base64 userinfo; plaintext none, unknown methods and undecidable forms are excluded at catalog admission, merge and stored-member generation. Manual tunnel parsing remains unchanged (#326).

## Applied-policy eligibility metadata (ADR 0042)

- `RouteEligibility` is a read-only transaction for one requested client: settings, explicit public peer flags/address/key and admission IDs/revisions, server identity ID and credential presence. It never selects private/preshared WG keys, the REALITY UUID value or server private/short-ID material, and performs no key derivation or initialization. Missing clients return `ErrNotFound`; no migration/schema change is required.
- Explanation pending comparisons still use internal ordinary snapshots, which can contain WG/manual-tunnel material. Those snapshots are not public response models and contain no live REALITY UUID or server private key. A disposable SQLite regression drops private columns to prove the narrow metadata query does not read them.

- Selected-client evidence metadata uses the read-only `RouteEligibility` projection, without selecting private WG keys or REALITY UUIDs. Accepted client admission-flag changes advance the publisher's observation lifetime even across a disable/re-enable ABA cycle; this changes no authentication or publication behavior (ADR 0043). Existing trusted REALITY source validation retains its separate private live-admission read.
