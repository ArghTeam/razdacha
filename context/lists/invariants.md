---
type: invariants
title: Lists
description: загрузка и кэш .srs/plain-списков, расписание, дублирование подсетей в nft-сет
---

## Invariants
- Кэш пишется атомарно: тело целиком в память → проверка → временный файл рядом → `Sync` → `rename`. Битый, пустой и оборванный ответ валидный кэш не трогают.
- Длина тела сверяется с `Content-Length` явно: `net/http` отдаёт `ErrUnexpectedEOF` не на всех видах обрыва, без сверки недокачанный список выглядит целым.
- Неудачный прогон повторяется с нарастающей паузой (минута → 15 минут), а не через сутки: на свежем VPS сеть поднимается позже демона.
- `.srs` читается через `srs.Read(r, true)`; без `recover = true` матчеры остаются скомпилированными и `ip_cidr` пуст.
- В подсети идёт только IPv4 — nft-сет `razdacha_subnets` объявлен как `ipv4_addr` (ADR 0005). Битые строки считаются в `List.Skipped` и разбор не отменяют.
- Pool refresh preserves known-server ordering and avoids rewriting known metadata, apart from filling missing title/country data. A server is dropped after `poolMissesBeforeDrop` consecutive misses; rejected country/encryption entries are removed immediately. Missing-link cards and ping ranking are source-specific outlinekeys behavior (ADR 0010, ADR 0020, issue #68).
- Pools sharing a catalog are fetched once per group (`PoolManager.Refresh` → `refreshGroup`). Each group receives the catalog result after the configured country/encryption filter, without splitting it into country pools. Grouping protects against duplicate crawls even though there is only one built-in pool (ADR 0018, ADR 0020).
- The built-in igareck driver reads all three subscriptions as one list, excludes unsupported keys such as vmess, and falls back from githack to GitHub. It does not set `PoolServer.Country` or use geo-IP. The shared pool filter derives country from the key title and rejects blocklisted countries or unencrypted transports before membership is persisted (ADR 0018, ADR 0020).
- Reconcile pool configuration using `PoolIdentity` (ID, catalog, enabled), not the entire `PoolTunnel`: changing membership must not trigger a crawl every reconciliation tick. Refresh the scheduler snapshot each tick; `TestPoolTunnelFieldsGuard` guards new identity fields. The default crawl interval is one hour, not the reconciliation period (issue #77).
- Pool membership refresh is automatically published through the shared daemon gate, over committed policy and committed list contents only (ADR 0023). It cannot apply pending rules, settings, tunnel edits or uncommitted cache changes. Unchanged healthy generations do not restart the runtime.
- Pool catalog drivers are selected by URL host (ADR 0015); each driver owns source parsing and reports `PoolDriver.KeyType`. Shared HTTP limits, timing and request accounting live in `PoolCatalog`/`poolCrawl`. The igareck driver fetches full subscriptions and intentionally does not apply the outlinekeys collected-key count limit.
- The legacy outlinekeys driver limits collected keys, not working keys: its two-step list/detail crawl needs roughly 105 requests for 100 collected keys. Health selection remains with `urltest` (ADR 0010). The igareck driver fetches whole subscriptions instead; do not treat outlinekeys crawl costs as built-in-pool behavior.
- For outlinekeys, page card count determines the end of pagination, not extracted key count: the site returns HTTP 200 for an empty page. Cards marked dead on the listing do not trigger a detail request.
- The outlinekeys driver rejects `/protocols/vless/` and `/protocols/trojan/` before fetching: those source sections omit required transport/security fields despite producing syntactically parseable URLs (ADR 0015). This source restriction does not disable VLESS or Trojan keys in igareck subscriptions.
- Ключ, не прошедший наш парсер, в БД не попадает. Проверку слой lists получает замыканием от демона (`PoolCatalog.CheckKey`) — про `internal/singbox` он не знает, как и раньше; это та же связь, что у `PlainLists` в обратную сторону.
- Source ping is optional: neither igareck nor outlinekeys measures latency. `PoolServer.PingMS = 0` means unknown, not zero milliseconds; unknown-ping entries sort last rather than being discarded.
- Тесты разборщика каталога идут от фикстур страниц в `testdata`: разметка server-side, её правка ломает разбор молча.
- Пул проверяется прогоном на стенде — заведением через ручку API, как это делает человек, с замером PID sing-box и md5 конфига. На фикстурах и заведении через `store` три дефекта подряд (#66, #68, #77) не воспроизвелись.
- Демон качает `.srs` только ради подсетей для nft-сета; маршрутизацию по этим спискам ведёт sing-box сам.
- `Manager.Snapshot()` copies every loaded list under one read lock. Each publication filters that copied map to its policy sources and uses the same contents for inline sing-box matching and nft subnets. A required plain list which has never loaded rejects the candidate; failed fetches retain cached contents. Background list refresh republishes committed policy, while scheduler source selection retains the union of applied and pending sources (ADR 0023).
- `Manager.Start(ctx)` возвращается сразу, первый прогон уходит в горутину: загрузка списков не задерживает старт демона.
- Состав стартового набора правил (`presetKeys` в `internal/lists`) не включает ASN-списки каталога (`cloudflare`, `cloudfront`, `hetzner`, `ovh`, `digitalocean`) — за ними половина интернета, добавление любого из них в пресет ломает селективность шлюза (ADR 0014, issue #150).

- Plain-list coherence is bounded to daemon-managed inline content. Autonomous `.srs`/`.json` refresh still has an independently tracked synchronization gap in [#220](https://github.com/ArghTeam/razdacha/issues/220); do not describe all list formats as synchronized.

## Key entry points
- `internal/lists/manager.go` — `Sources(store.Snapshot)`, scheduling and immutable `Snapshot()` content for the gate.
- `internal/lists/pool.go` — `PoolTunnels(store.Snapshot)`, `SamePoolSet`, расписание пулов и слияние состава (`MergePool`).
- `internal/lists/fetch.go` — условные запросы, таймауты, потолок размера, откат на кэш.
