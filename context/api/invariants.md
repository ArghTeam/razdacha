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
- Route diagnostics retain uncertain CIDR candidates even when DNS returns real addresses. Manual/plain CIDRs can select destination traffic without a DNS projection; native sets may contain unknown address predicates. A real answer cannot prove direct routing. Known domain matches remain certain; unknown native content remains a candidate (ADR 0024, #222).
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

- Community list status combines daemon-owned native SRS and any companion subnet source; either failure is visible and the timestamp is the oldest successful component. Native predicates remain uncertain route-probe candidates even when loaded or real DNS answers are returned. Generation errors remain diagnostic errors rather than omitted-policy success (ADR 0025).

- Peer deletion returns `ok`, `affected_rule_ids` and `inactivated_rule_ids` from one committed transaction. Rule listing computes `inactive_reason: no_selected_peers` against a consistent snapshot. A stale selected-ID PATCH cannot resurrect deleted selection; empty enabled selections are rejected (ADR 0026).

- A panel session check uses normal authenticated 401 handling so an expired session
  invalidates any open configuration interaction. Incorrect-password login and logout
  keep their local handling; logout clears UI secrets before waiting for the response.
  The opt-in UI security fixture serves only disposable synthetic state and is not a
  daemon startup mode; it never runs schedulers or initializes installed networking.
- Frontend requests capture a session revision. Login/logout attempts and the first
  current authenticated 401 advance it; a stale 401 cannot invalidate a newer login.
  Client creation, rename, enable/disable, deletion preflight and post-delete rule
  loading callbacks from an earlier session cannot mutate current inventory, pending
  state, notifications or confirmations. Completed mutations in the same session
  remain visible after ordinary close or modal replacement. Client-dialog feedback
  and deletion confirmation render only in their initiating dialog/session; close,
  replacement, logout and expiry invalidate them. Independent configuration/tunnel
  operations retain the current editor and its draft.
  Client creation has one in-flight gate per form shared by Enter and pointer input;
  closing the form does not discard its successful same-session creation result.
  Backend invalid-session 401 cookie expiry is unchanged; a delayed expiry cookie
  may still require another login. The frontend guard does not control cookie timing.
  This browser lifetime guard does not replace backend session authentication.

## Configuration lifetime verification boundary

The synthetic actual-API fixture verifies frontend lifetime, not backend cookie
delivery or installed deployment. Cleanup removes application references without
promising heap erasure or immunity to delayed cookie expiry. A dataset marker alone
does not prove visible rendering. Keep case-specific coverage and inconclusive
observations explicit in the [completed specification](../../tasks/done/216-clear-ui-config-secrets.md).

- Peer PATCH has omitted/string/null exit semantics and transactional field updates; unknown/empty references return 400 and referenced tunnel deletion returns 409. Authenticated GET /api/apply/status reads editable-versus-committed policy and uncertainty under the publication gate without mutation. Committed/bootstrap writers use generation v4 (v3 introduced peer routing); old envelopes containing new peer routing are rejected. Refresh/recovery never publish pending choices (ADR 0028).

- Native generation transport compatibility follows ADR 0029: unsupported refresh retains accepted content and validators, and publication reports transport prefixes in the unified nft count. A successful source download alone never applies pending policy.

- Apply status compares the JSON persistence representation of editable and committed policy under the publication gate. Tunnel parsed-JSON whitespace and nil versus JSON null must not report pending edits after Apply or recovery; genuine peer egress and rule changes still do. Uninitialized or uncertain publication remains pending, and comparison encoding failures are returned as errors.

## Tunnel action lifetime

- Tunnel actions capture the initiating frontend session and modal/interaction.
  Confirmed same-session mutations retain inventory/pending effects after ordinary
  closure or navigation, but never close a replacement editor. Previous-session
  completions cannot update inventory, pending state or feedback, nor dispatch
  follow-up inventory/details reads. This guard does not change backend authentication
  or delayed cookie delivery.
- Current-session tunnel actions are single-flight for the same target. The tunnel
  screen loader coalesces reads within an unchanged generation. Its shared
  `beginInventoryRead()` token accepts a snapshot only in the same session and before
  a newer reader or accepted mutation invalidates it. The Clients reader uses that
  token plus its own screen/request guard and ignores obsolete read errors rather
  than marking tunnel metadata missing. Startup `app.loadAll()` captures the same
  token before its parallel batch
  and accepts tunnel inventory through it, so an earlier completed tunnel response
  cannot overwrite a mutation accepted while another bootstrap request is pending.
  Bootstrap tunnel success/error feedback also checks token currency before changing
  missing/offline state or posting errors. Callback updates resolve the current
  inventory identity. Connected initiating buttons are restored
  by operation/session ownership independently of obsolete modal feedback.
- Confirmed Save, WARP registration and catalog refresh success is distinct from
  follow-up inventory/details failure, so read errors never imply that an accepted
  mutation should be repeated. Pool metadata, raw loading and parser responses obey
  their modal/session/request lifetime; raw cleanup and backend protocol/reference
  semantics remain unchanged. Development-only tests use synthetic local responses,
  never installed networking or external WARP registration.

## Rule inventory and probe lifetime

- Rule inventory actions are single-flight per target, with full-order writes gated
  separately. Confirmed same-session effects update current identities and pending
  state after ordinary navigation/replacement; previous-session completions cannot
  publish state/feedback or start follow-up reads. Reorder failure never restores a
  captured inventory snapshot over concurrent accepted changes.
- Shared `rules.beginInventoryRead()` tokens accept inventory only in their initiating
  session and before a newer read or accepted mutation. Rule screen/editor/duplicate
  and Clients deletion-follow-up readers use that authority and ignore stale read
  errors. Startup batch adoption is owned by dependent #249; #259 alone does not
  close the demonstrated bootstrap rules overwrite before that integration lands.
- Duplicate success is distinct from inventory refresh failure and retains existing
  URL sources/resolve_real_ip. Domain route and direct-reachability probes remain
  shared-rule diagnostics, never per-client exit evaluation; result/error/busy state
  follows session/request/interaction ownership. Existing editor layout/policy and
  backend contracts remain unchanged. Browser evidence uses synthetic intercepted
  requests, not installed networking or real domain probes.
