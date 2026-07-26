# Singbox — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-26

### трафик — селективность проверена вживую

**Changed:** на стенде прошла полная цепочка: FakeIP → метка nft → tproxy → sing-box → WireGuard-туннель. Прямой трафик уходит через masquerade с адресом сервера.
**Beware:** `systemctl reload` не годится для первого применения — sing-box тогда ещё не запущен; нужен `reload-or-restart`. Ошибку окружения нельзя отдавать как `invalid_config`: на стенде отказ по правам выглядел как ошибка пользователя.


## 2026-07-25

### #3 — генератор конфига из состояния БД

**Changed:** `internal/singbox/{generate,dns,tunnel,ruleset}.go` — сборка `option.Options` из снимка состояния; доки 02 и 04 приведены к реализации.
**New surface:** `Generate(store.Snapshot)` и `Marshal(option.Options)`.
**Beware:** `sing-box check` ещё не вызывается; правило с одним plain-списком в конфиг не попадёт — пути для plain-списков доменов нет.

### #4 — парсеры proxy-URL и WireGuard .conf

**Changed:** `internal/singbox/parse*.go` — разбор vless/ss/trojan/hysteria2/socks и INI WireGuard; зависимость `sing-box v1.12.25`.
**New surface:** `Parse(raw)` — единственная точка разбора пользовательского ввода.
**Beware:** `kcp` и `xhttp` из корпуса Podkop не поддерживаются; `ech` игнорируется — там base64, а sing-box ждёт PEM.
