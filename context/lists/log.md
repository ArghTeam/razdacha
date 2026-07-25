# Lists — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-25

### #5 — загрузка, кэш и расписание списков

**Changed:** новый слой `internal/lists`: скачивание `.srs`/plain с условными запросами, атомарный кэш, расписание с ретраями.
**New surface:** `Manager.Sources(store.Snapshot)`, `Manager.Subnets()` — срез подсетей для nft-сета `razdacha_subnets`.
**Beware:** демон качает `.srs` только чтобы вынуть подсети для nft — маршрутизацию по ним по-прежнему ведёт sing-box. `docs/04-dns-fakeip.md` описывает это иначе и требует правки.

