# Ui — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-25

### ui-prototype — кликабельный прототип четырёх экранов

**Changed:** `ui/prototype/` — статический макет на ванильном JS, без сборки и внешних зависимостей; свой QR-кодер вместо CDN.
**Beware:** контракт неполон — публичного ключа сервера нет ни в одной сущности API, а без него клиентский `.conf` не собрать. График трафика показать не из чего: временные ряды не хранятся. Статус туннеля дублируется между экраном туннелей и `/api/diag`.

