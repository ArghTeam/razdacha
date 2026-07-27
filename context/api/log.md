# Api — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-27

### #100 — ручка добавления WARP

**Changed:** `POST /api/tunnels/warp` — регистрация устройства и туннель из её ключей; `409` отдаётся до запроса наружу, чтобы у Cloudflare не оставались лишние устройства.
**Beware:** «WARP в системе один» проверяется только на этой ручке. Вставленный руками `.conf` заводит второй туннель с тем же source — цепочкам это не мешает, но формулировку в `docs/05-api.md` стоит сверить.

### #91 — события оповещений

**Changed:** `internal/api/notify_events.go` — подтверждение перехода тремя проверками, режим «нестабилен», вызов транспорта из расписания.
**Beware:** потолка на частоту нет намеренно — он выгорает на падениях и режет сообщение «поднялся». Дыру подтверждения (туннель, валящийся через раз, не наберёт трёх одинаковых проверок) закрывает только режим «нестабилен».

### #90 — транспорт оповещений в телеграм

**Changed:** пакет `internal/notify`, ручки `GET`/`PUT /api/notify` и `POST /api/notify/test`.
**New surface:** `notify_sender` подменяется в тестах — настоящий api.telegram.org в них не участвует.
**Beware:** токен наружу не отдаётся, только `token_set`; пустой токен в `PUT` означает «оставить прежний». Битый токен даёт от телеграма 404, а не 401 — он подставляется в путь URL.

### #89 — состояние туннелей по расписанию

**Changed:** `internal/api/tunnels_watch.go` — прогон каждые `tunnel_check_interval`, подъём сохранённых проверок при старте.
**Beware:** состояние снимается одним `/proxies`, а не пробой каждого туннеля: проба группы `urltest` прогнала бы всю сотню серверов пула. Пустой журнал группы и недоступный Clash оставляют прежний результат, а не пишут `down` (ADR 0011).

### #87 — имя `.conf` по правилам клиента WireGuard

**Changed:** `confFileName()` транслитерирует кириллицу, схлопывает остальное в дефис, режет по 15 символам; префикс `razdacha-` убран.
**New surface:** имя файла отдаёт только `Content-Disposition` — UI своё не строит.
**Beware:** клиент WireGuard берёт имя туннеля из имени файла и валидирует как `[a-zA-Z0-9_=+.-]{1,15}`. Префикс не возвращать: бюджет и так 15 символов.


## 2026-07-26

### #63, #71, #74, #75 — блок pool в ответе, ручка деталей, WebSocket убран

**Changed:** `internal/api/{tunnels,tunnels_pool,handlers}.go` — блок `pool` в списке, `POST /api/tunnels/{id}/refresh`, `GET /api/tunnels/{id}/pool`; обещание WebSocket убрано.
**New surface:** `poolResponse` (`servers_total`, `servers_alive`, `current`, `updated_at`, `next_update_at`), серверы с `in_rotation`.
**Beware:** кэш расписания и `vless://` наружу — в инвариантах слоя.

## 2026-07-25

### #25 — эндпоинты пиров, туннелей, правил и настроек

**Changed:** `internal/api/{data,peers,tunnels,rules,settings,diag}.go` — CRUD четырёх сущностей, разбор строки туннеля, выдача клиентского `.conf`.
**New surface:** `server_public_key` в `GET /api/settings`; `PUT /api/rules/order`; `POST /api/tunnels/parse`.
**Beware:** `null` вместо нуля и `503 not_ready` вместо неполного `.conf` — в инвариантах слоя.


## 2026-07-25

### #20 — HTTP-сервер и авторизация

**Changed:** новый слой `internal/api`: сервер, argon2id, сессии в БД, блокировка после неудач; таблица `sessions` миграцией 2.
**New surface:** `POST /api/login`, `/api/logout`, `GET /api/session`; `razdachad -set-password` читает пароль из stdin.
**Beware:** природа блокировки и отсутствие CSRF-токена — в инвариантах слоя.

