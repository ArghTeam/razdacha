# Lists — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-08-18

### #196 — интервал обхода пула 12 ч → 1 ч

**Changed:** `DefaultPoolInterval` 12 ч → 1 ч. Источник igareck обновляется ~каждые 3 мин; обход дёшев — reload только при смене окна.
**Beware:** `poolMissesBeforeDrop` не тронут — 3 обхода × 1 ч = 3 ч на выселение.

### #194 — окно urltest 16 → 64

**Changed:** `PoolConfigServers` 16 → 64. Каталог igareck живость не отражает, мёртвые держали слоты вечно; широкое окно даёт urltest живой резерв.
**Beware:** держится равным `singbox.poolMaxServers`, сверяется `TestPoolConfigWindowAgrees`. Стенд: 2 живых из 16 → 17 из 64.

### #189 — имя члена пула из фрагмента ссылки

**Changed:** `igareck` заполняет `Title` из фрагмента `#…` (флаг, страна, город), fallback на хост; `MergePool` backfill'ит пустой `Title`/`Country` из свежего обхода.
**Beware:** backfill только для пустого поля — перезапись на каждом обходе дала бы churn конфига (#68). Title косметичен: на окно и теги не влияет. Проверено на стенде.

## 2026-08-17

### #181 — igareck без geo-IP, единый пул

**Changed:** драйвер `igareck` отдаёт все ключи одним списком; `poolServersForCountry`, `PoolTunnel.Country`, разбор хоста и geoip убраны; `refreshGroup` применяет выдачу целиком (ADR 0018).
**Beware:** `PoolServer.Country` igareck больше не заполняет; пакет `internal/geoip` удалён.

### #170 — источник igareck и раскладка пулов по странам

**Changed:** драйвер `igareck` (3 подписки, зеркало+резерв, страна geoip); `Refresh` — fetch-once-partition, `poolServersForCountry` фильтрует по стране пула.
**Beware:** vmess отсеивается (Parse не берёт); пустая страна = вся выдача.

### #168 — офлайн-страна по IP (пакет internal/geoip)

**Changed:** новый пакет `internal/geoip` — `Country(ip) string` из встроенной DB-IP Country Lite, чистый Go, без сети. Фундамент страновых пулов (#167, ADR 0017).
**New surface:** `geoip.Country`, `geoip.Attribution`.
**Beware:** база заморожена на релизе, обновляется только с razdacha; порча базы → `Country` даёт `""`, страна не течёт.

## 2026-08-15

### #149 — отказ правила виден в панели

**Changed:** `Fetcher.Update` возвращает `Refreshed{List, FetchedAt, Stale}`, `Parse` стал обёрткой над ним; появился `SourceState{URL, UpdatedAt, FailedAt, Err, Cached}` и `Manager.States()`.
**Beware:** раньше `Parse` глотал ошибку загрузки при живом кэше — теперь она видна через `States()`, а не молчит.

### #153 — каталог пула переехал на outlinekeys
**Changed:** `vpnkeys.go` удалён (источник умер); `pool_outlinekeys.go` — драйвер outlinekeys, `DefaultPoolCatalogURL` указывает на его страницу outline; `PoolCatalogRetired` хранит закрывшиеся хосты для переезда живых установок.
**Beware:** ручной `POST /refresh` и такт расписания могут обойти каталог одновременно — до 210 запросов вместо 105 на боевой длине пула.

### #156 — сериализация обхода каталога пула
**Changed:** `PoolCatalog.Servers` теперь занимает каталог по адресу (`u.String()`) через `beginCrawl`/`endCrawl` на время обхода; второй обход того же адреса отказывает сразу `ErrPoolCrawlBusy`.
**New surface:** Новый сентинел `lists.ErrPoolCrawlBusy`.
**Beware:** замок — по адресу каталога, не по туннелю; такт на занятом каталоге пишет INFO и неудачей не считается.

### #150 — состав стартового набора правил

**Changed:** `presetKeys`, `Preset()`, поле `InPreset` в `CommunityService`; состав — `russia_inside`, `google_ai`, `discord`, `meta`, `twitter`.
**New surface:** `CommunityService.Preset()`.
**Beware:** ASN-списки (`cloudflare`, `cloudfront`, `hetzner`, `ovh`, `digitalocean`) не входят — иначе пресет не селективен.

### #161 — страна сервера пула помечена как подпись источника

**Changed:** у `outlineKeysCountry` комментарий: это заголовок чужой карточки, не измерение. Поведение разбора не менялось, `store.PoolServer.Country` остался тем же полем.
**Beware:** подпись врёт — сверка пяти ключей дала одно расхождение (карточка «Scotland», реестр — Гонконг, geo — Лос-Анджелес). Подтверждение географии в задачу не входило и остаётся открытым.

## 2026-07-29

### #121 — обновление пула само доезжает до sing-box

**Changed:** `applyGate` в `cmd/razdachad/apply.go` подписан на `PoolManager.Updates()`; переносит в применённый снимок только `Pool` и `PoolUpdatedAt`.
**Beware:** применяется только состав пула, не снимок целиком — иначе расписание выкатило бы неприменённые правки правил. Базовый снимок на старте = состояние БД.

### #125 — домены plain-списка идут в inline-набор

**Changed:** `PlainLists` прокинут в `Generate`; ветка не-`.srs` собирает набор `rule-<id>-plain-<i>` вместо пропуска.
**Beware:** раньше правило с одним plain-списком выпадало из конфига, и трафик уходил напрямую. Список не в кэше или пуст после разбора — правило выпадает и теперь, но громко: `warn` в лог и причина в диагностике.

## 2026-07-28

### #117 — аудит утечек: что нашлось в слое

**Changed:** ничего в коде — разбор по коду, `docs/research/security-audit-2026-07.md`.
**Beware:** обе находки слоя закрыты — потребитель `PoolManager.Updates()` (#121) и домены plain-списков (#125), записи выше.

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

