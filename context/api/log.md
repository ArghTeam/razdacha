# Api — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-25

### #25 — эндпоинты пиров, туннелей, правил и настроек

**Changed:** `internal/api/{data,peers,tunnels,rules,settings,diag}.go` — CRUD четырёх сущностей, разбор строки туннеля, выдача клиентского `.conf`.
**New surface:** `server_public_key` в `GET /api/settings` (только чтение, источник — хук); `PUT /api/rules/order`; `POST /api/tunnels/parse`.
**Beware:** отсутствующие производные поля отдаются `null`, а не нулём — клиент обязан различать «нет источника» и «ноль». Конфиг пира без ключа сервера отдаёт `503 not_ready`, а не файл, который выглядит рабочим.


## 2026-07-25

### #20 — HTTP-сервер и авторизация

**Changed:** новый слой `internal/api`: сервер, argon2id, сессии в БД, блокировка после неудач; таблица `sessions` миграцией 2.
**New surface:** `POST /api/login`, `/api/logout`, `GET /api/session`; `razdachad -set-password` читает пароль из stdin.
**Beware:** блокировка идёт по адресу и хранится в памяти — рестарт демона её снимает. CSRF держится на `SameSite=Lax`, токена нет: вернуться к вопросу, когда появятся мутирующие эндпоинты.

