---
type: spec
tracker: "#4"
---
## TLDR

Разбор пользовательского ввода туннеля в структуры `sing-box/option`: proxy-URL
(`vless`, `ss`, `trojan`, `hysteria2`/`hy2`, `socks4`/`socks4a`/`socks5`), WireGuard
`.conf` в INI-формате и произвольный JSON. Логика портирована из Podkop
`sing_box_config_facade.sh:58` и `helpers.sh:129-210`.

## Goal

Дать слою API и генератору конфига одну функцию `singbox.Parse(raw)`, которая по
строке определяет форму ввода, возвращает тип туннеля, форму источника и готовый к
вклейке JSON — то, что ложится в поля `type`, `source` и `parsed` модели `Tunnel`
(`docs/02-data-model.md`).

## Domains touched

- [singbox](../context/singbox/index.md) — парсеры proxy-URL и WireGuard `.conf`

## Decisions

- 2026-07-25 — `net/url` для разбора ссылок не используется: в открытом userinfo
  shadowsocks встречается `/` (`ss://method:base64pass@host`), на котором `url.Parse`
  обрывает authority и отдаёт ошибку. Ссылка режется вручную, как в помощниках Podkop;
  userinfo отделяется по последней `@` до отрезания пути.
- 2026-07-25 — WireGuard разбирается в `option.Endpoint{Type: "wireguard"}` с
  `System = false` и пустым `Name`, `bind_interface` не выставляется — ADR 0002.
  Тест отдельно проверяет, что этих полей нет в JSON.
- 2026-07-25 — транспорты `kcp` (mKCP) и `xhttp` из корпуса Podkop дают ошибку на
  русском: в sing-box их нет вовсе, тихо ронять транспорт — значит пустить трафик
  мимо ожиданий пользователя. Остальные 54 ссылки корпуса разбираются.
- 2026-07-25 — параметр `ech` игнорируется: в ссылках это base64 `ECHConfigList`, а
  `tls.ech.config` sing-box ждёт PEM. Podkop его тоже не переносит.
- 2026-07-25 — `sing-box` пинуется на `v1.12.25`: ADR 0002 требует ≥ 1.12.0, а брать
  структуры `option` из более новой ветки — значит генерировать поля, которых нет в
  минимально поддерживаемой версии.
- 2026-07-25 — `hysteria2` с `obfs`, но без `obfs-password`, отклоняется на разборе:
  sing-box отказался бы поднять такой outbound уже после `check`, когда ошибку
  некуда показать.
- 2026-07-25 — тег outbound'а парсер не проставляет: теги раздаёт генератор конфига,
  иначе они разъедутся между `parsed` в БД и итоговым конфигом.

## Acceptance criteria

- [x] корпус `podkop/String-example.md` скопирован в
      `internal/singbox/testdata/proxy-urls.txt` и разбирается целиком, кроме
      четырёх ссылок с `kcp` и `xhttp` — они дают ошибку
- [x] невалидный ввод даёт ошибку на русском, обёрнутую в `ErrParse`, а не панику:
      обрезанные URL, битый base64 в `ss://`, отсутствующие обязательные поля
- [x] `.conf` WireGuard превращается в userspace-`endpoint`, без `bind_interface`
- [x] произвольный JSON вставляется как есть, тип берётся из поля `type`
- [x] разбор не ходит в сеть и не требует root
