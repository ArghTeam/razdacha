---
type: invariants
title: Api
description: REST + WS, встроенная SPA, диагностика и клиент Clash API
---

## Invariants
- HTTP-сервер панели слушает `127.0.0.1:8080` и никогда не адрес wg0: наружу его открывает nginx (ADR 0008). Адрес проверяется на старте, не-loopback — выход с кодом 2.
- TLS терминируется на nginx; демон говорит по обычному HTTP и не знает про сертификаты. Реальный адрес клиента приходит в `X-Real-IP`/`X-Forwarded-For`, схема — в `X-Forwarded-Proto`.
- `/api/ws` идёт через прокси, поэтому в конфиге nginx обязательны `proxy_http_version 1.1` и проброс `Upgrade`/`Connection`. Их отсутствие ломает только живые обновления и молча — закреплено тестом в слое packaging.

## Key entry points
- `cmd/razdachad/main.go` — флаг `-listen` и проверка `checkListen`.
