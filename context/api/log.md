# Api — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-25

### #20 — HTTP-сервер и авторизация

**Changed:** новый слой `internal/api`: сервер, argon2id, сессии в БД, блокировка после неудач; таблица `sessions` миграцией 2.
**New surface:** `POST /api/login`, `/api/logout`, `GET /api/session`; `razdachad -set-password` читает пароль из stdin.
**Beware:** блокировка идёт по адресу и хранится в памяти — рестарт демона её снимает. CSRF держится на `SameSite=Lax`, токена нет: вернуться к вопросу, когда появятся мутирующие эндпоинты.

