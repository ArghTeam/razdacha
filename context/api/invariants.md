---
type: invariants
title: Api
description: REST, встроенная SPA, диагностика и клиент Clash API
---

## Invariants
- HTTP-сервер панели слушает `127.0.0.1:8080` и никогда не адрес wg0: наружу его открывает nginx (ADR 0008). Адрес проверяется на старте, не-loopback — выход с кодом 2.
- TLS терминируется на nginx; демон говорит по обычному HTTP и не знает про сертификаты. Реальный адрес клиента приходит в `X-Real-IP`/`X-Forwarded-For`, схема — в `X-Forwarded-Proto`.
- Живого канала (WebSocket) нет и не обещается: панель работает опросом REST. Проброс `Upgrade`/`Connection` и `proxy_http_version 1.1` в конфиге nginx оставлены как задел и закреплены тестом в слое packaging — они ничего не стоят, но их отсутствие сломало бы канал молча, если он появится.
- Заголовкам `X-Real-IP` и `X-Forwarded-For` верим **только** когда соединение пришло с loopback; из `X-Forwarded-For` берётся последний элемент — nginx дописывает реальный адрес в конец, начало приходит от клиента. Иначе перебор обходится строкой в заголовке.
- В БД лежит SHA-256 токена сессии, сам токен живёт только в cookie: утечка базы не должна давать готовые сессии. Все сравнения секретов — `crypto/subtle`.
- Параметры argon2id читаются из самой строки хеша (PHC), а не из констант: ужесточение параметров не должно ломать вход со старым паролем.
- Одновременных проверок пароля не больше двух: 64 MiB на попытку — это вектор отказа на VPS, а не только защита.
- Всё под `/api/`, кроме входа, закрыто сессией по умолчанию — забытый эндпоинт отдаёт 401, а не данные.
- Cookie сессии всегда `Secure` + `HttpOnly` + `SameSite=Lax`. Следствие: по чистому HTTP не на localhost вход не сохранится — так и задумано (ADR 0009).

- Живое состояние пула — число живых серверов и текущий выбор — читается из Clash API у той же группы `urltest`, по которой идёт ротация. Своей проверялки не заводим: она расходилась бы с той, по которой реально переключается трафик. Запрос идёт один на весь список туннелей и только если в нём есть включённый пул; недоступный Clash оставляет поля `null` и список не роняет.
- Неудачный резолв через Clash API приходит не кодом HTTP, а телом: `GET /dns/query` отвечает `200` с ненулевым `Status` и без `Answer` (`3` — домена нет, `5` — запрос отклонён правилом). Ветвиться только по HTTP-коду нельзя — проверено на sing-box 1.12.25 (#149).
- Настоящий адрес в ответе резолвера — доказательство от ядра: ни одно правило с записью в `dns.rules` не совпало (записи есть у туннеля через FakeIP, у `block` и у правила с недоступным туннелем по ADR 0013; нет — у `direct` и `resolve_real_ip`). Отказ резолвера, наоборот, не снимает ни одного кандидата: он сам означает, что правило сработало. Победителем называется достоверное совпадение, а не первое по порядку: состав `.srs` ядро наружу не отдаёт, и правило со списком остаётся кандидатом с `certain: false` (#149).
- `409` на `POST /api/tunnels/warp` — ограничение кнопки, а не модели: туннелей с `source = warp` в системе может быть сколько угодно, второй заводится вставкой `.conf` через `POST /api/tunnels` и это законно (цепочки ADR 0012 опознают второе звено по `source`, а не по единственности). Отказ бережёт Cloudflare: каждая регистрация создаёт у них реальное устройство. Формулировки «WARP в системе один» в документах и текстах не заводить.
- Отказ «пул уже есть» выдаётся после разбора ссылки, а не до: иначе битая ссылка на каталог получает 409 про дубль вместо 400 с объяснением, что с ней не так.
- Ссылки `vless://` наружу не отдаются ни в списке, ни в деталях пула: в них UUID. Поля `raw` нет ни в одном ответе со списком туннелей — включая ответы на создание, правку и `refresh`: у WARP там `.conf` с приватным ключом, а список перечитывается на каждый опрос экрана. Конфиг отдаёт только `GET /api/tunnels/{id}/raw` по запросу открытой формы правки, и у WARP он отвечает `editable: false` с пустым `raw`: ключ сгенерировал сервер, править его нечего (issue #124).
- Обновление пула по требованию идёт мимо кэша расписания: свежего пула в кэше ещё нет, и ручка отвечала `404 «Туннель не найден»` до первого тика (issue #74).
- The daemon supplies a `CoherentApplier`: manual Apply, list refresh and pool refresh use one gate (ADR 0023). The API never publishes nft independently. Snapshot acquisition and peer allocation/insertion share the gate mutex; all committed peer addresses, including disabled/deleted identities, remain reserved until explicit Apply replaces the generation. Before trusted bootstrap, creating peers returns `503`. Live WG revocation still follows the editable database, never routing-record replay.
- Производное поле без источника отдаётся `null`, а не нулём: клиент обязан различать «нет данных» и «ноль». Клиентский `.conf` без ключа сервера — `503 not_ready`, а не файл, который выглядит рабочим.
- Блокировка после неудачных входов идёт по адресу и живёт в памяти: рестарт демона её снимает. CSRF-токена нет, защита держится на `SameSite=Lax`.
- Peer creation is refused while editable allocation pool/server fields differ from committed settings. Manual Apply snapshots all current peers inside the shared gate; candidate WG changes which exclude stored peers or collide with their addresses are rejected before mutation. No peer address migration is implied.
- A guarded generation is exposed as an `nft` diagnostic error. Generation/check failures return `422` before mutation; post-guard restart/nft/persistence/guard-release failures return `500` with the guard retained. Success has one unified nft count, rather than a second API-owned publication.

## Key entry points
- `cmd/razdachad/main.go` — флаг `-listen`, проверка `checkListen`, `-set-password`.
- `internal/api/server.go` — сборка сервера и middleware.
- `internal/api/clientip.go` — определение адреса клиента за прокси.
- `internal/api/throttle.go` — блокировка после серии неудачных входов.
