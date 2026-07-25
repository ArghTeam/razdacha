---
type: invariants
title: Singbox
description: генерация option.Options из состояния БД, парсеры proxy-URL, check и reload
---

## Invariants
- WireGuard из `.conf` разбирается в `option.Endpoint{Type: "wireguard"}` с `System: false` и пустым `Name`; `bind_interface` не используется (ADR 0002).
- Структуры sing-box сериализуются через `MarshalJSONContext`: обычный `json.Marshal` теряет содержимое `Options`.
- Невалидный ввод возвращает ошибку на русском, пригодную для показа как есть; паника недопустима даже на обрезанном URL и битом base64.
- Неизвестный `security=` в proxy-URL — отказ, а не продолжение с выключенным TLS (Podkop здесь мягче: пишет в лог и идёт дальше).

## Key entry points
- `internal/singbox/parse.go` — `Parse(raw)`: определение формы ввода (URL / INI / JSON) и единая точка разбора.
- `internal/singbox/testdata/proxy-urls.txt` — корпус ссылок из `podkop/String-example.md`; фикстуры живут в репозитории, а не в чужом каталоге.
