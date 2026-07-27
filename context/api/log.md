# Api — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-27

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

**Changed:** `internal/api/{tunnels,tunnels_pool,handlers}.go` — блок `pool` в ответе списка, `POST /api/tunnels/{id}/refresh`, `GET /api/tunnels/{id}/pool`, отказ на создание пула и на удаление встроенного. Обещание WebSocket убрано из документов и из панели: канала не было, а потребителя у него нет — графика трафика в интерфейсе не существует.
**New surface:** `poolResponse` (`servers_total`, `servers_alive`, `current`, `updated_at`, `next_update_at`), список серверов с `in_rotation`.
**Beware:** обновление по требованию не должно опираться на кэш расписания — так возник #74, когда свежий пул отвечал `404 «Туннель не найден»` до первого тика. Ссылки `vless://` наружу не отдаются: в них UUID. Проброс `Upgrade` в nginx оставлен намеренно и закреплён тестом — без него будущий канал сломался бы молча.

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

