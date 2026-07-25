# Singbox — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-25

### #4 — парсеры proxy-URL и WireGuard .conf

**Changed:** `internal/singbox/parse*.go` — разбор vless/ss/trojan/hysteria2/socks и INI WireGuard в структуры `sing-box/option`; зависимость `sing-box v1.12.25`.
**New surface:** `Parse(raw)` — единственная точка разбора пользовательского ввода в туннель.
**Beware:** `kcp` и `xhttp` из корпуса Podkop не поддерживаются — таких транспортов нет в sing-box, они отдают ошибку. `ech` из ссылки игнорируется: там base64 `ECHConfigList`, а sing-box ждёт PEM. `NOTICE.md` устарел — sing-box теперь линкуется, а не только запускается процессом.
