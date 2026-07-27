# Lists — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-26

### #62, #68 — разборщик каталога ключей и слияние состава

**Changed:** `internal/lists/{vpnkeys,pool}.go` — обход страниц каталога и расписание обновления пулов.
**New surface:** `PoolCatalog.Servers`, `PoolManager` с `RefreshPool(ctx, PoolTunnel)`, `MergePool(stored, fresh)`, `DefaultPoolInterval` = 12 часов, `DefaultPoolCatalogURL`.
**Beware:** каталог отдаёт **разный набор ссылок почти на каждом запросе** — следствия в инвариантах слоя.

### стенд — три дефекта, которых не нашли тесты

**Changed:** проверка на живом Debian после мержа вскрыла то, что зелёные тесты пропустили: пул нельзя было создать вовсе (#66), обход каталога перезапускал sing-box и рвал туннели (#68), каталог обходился каждые 30 секунд вместо 12 часов (#77).
**Beware:** причина у всех трёх одна — идеализированный вход в тестах. Как проверять пул — в инвариантах слоя.

## 2026-07-25

### #5 — загрузка, кэш и расписание списков

**Changed:** новый слой `internal/lists`: скачивание `.srs`/plain с условными запросами, атомарный кэш, расписание с ретраями.
**New surface:** `Manager.Sources(store.Snapshot)`, `Manager.Subnets()` — срез подсетей для nft-сета `razdacha_subnets`.
**Beware:** зачем демону `.srs` — в инвариантах слоя; `docs/04-dns-fakeip.md` описывал это иначе.

