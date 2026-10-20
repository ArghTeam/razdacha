# Ui — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-10-20

### #233 — accessible shared dashboard controls

**Changed:** Dialog headings, focus entry/trapping, inert backgrounds and stable
opener restoration; keyboard overflow menus with cleanup on every screen render.
Long names wrap, narrow/coarse controls use 44 px hit areas, neutral hints and
inactive rows remain readable, and repeated controls have contextual names.
**Beware:** Authentication dismissal suppresses application focus restoration.
Existing modal/session revisions and secret cleanup retain authority. No backend,
routing policy, settings transaction or language migration change was made.
**Validation:** UI guards and JavaScript syntax checks passed. Independent frozen
source `11e2621` browser checks passed: 16 screen/viewport captures, keyboard and
modal lifetime checks, corrected 44 px configuration disclosure, sampled contrast
and 12 routing/Apply/logout scenarios. These use synthetic APIs, not live backend
or formal accessibility validation. Detailed evidence is in the [completed task specification](../../tasks/done/233-dashboard-controls.md).


### #216 — bound configuration secrets to the current interaction

**Changed:** Central modal cleanup clears configuration attributes, form values,
QR pixels and modal-owned download URLs. Modal revisions reject stale configuration
and peer-creation responses. Logout cleans up before its network request; session
checks share normal current-session authenticated 401 handling; stale-session 401
cannot close a newer login. Login waits for an outstanding logout response so its
cookie cannot clear the newer session. Tunnel raw/parse state and debounce
share the modal lifetime, with immediate invalidation on newer input.
**Beware:** Successful peer creation still marks pending changes after ordinary modal
closure. Cleanup removes application references; it does not promise heap erasure or
remove a downloaded file. No frontend build step was added.
**Validation:** Frozen executable source `1f2d1dc`: independent local fixed 31/31
and six positive baseline defects; isolated staging fixed 31/31 and five positive
baseline defects. Baseline peer creation is inconclusive; the controller correctly
stopped with exit 2. Both stop receipts were 204; remote infrastructure/audit passed.
No installed deployment. Final documentation review/current CI remain merge gates. See [the specification](../../tasks/done/216-clear-ui-config-secrets.md)
and [fixture protocol](../../docs/11-staging-validation.md#configuration-ui-lifetime-fixture-216).

### #215 — preserve selected scope after peer deletion

**Changed:** Explain affected rules at deletion and explicit reassignment/activation; refresh orphan state and retain disabled orphan editing.
**Decision:** [ADR 0026](../../docs/decisions/0026-peer-deletion-preserves-scope.md).
**Validation:** Local tests/static checks and controlled four-case wire observations completed on `14e28b3`: two fixed passes and two positively proven original-base broadening failures, with 114 zero-drop capture barriers. See the [completed specification](../../tasks/done/215-safe-peer-deletion.md) for evidence and limits. Final documentation/evidence review and current CI remain merge gates.

## 2026-08-21

### #202 — блок «Исключены фильтром» на экране туннелей

**Changed:** `ui/dist/js/screens/tunnels.js` — отбракованные ноды показываются отдельным блоком с причиной.
**Beware:** нода, пропавшая из состава без объяснения, читается как баг пула. Причина отбраковки в интерфейсе — не косметика, а единственный способ отличить фильтр от поломки обхода.

## 2026-08-18

### #198 — контрол «Обновление пула» на экране настроек

**Changed:** селектор `s-pool` (30 мин … сутки) рядом с интервалом списков; варианты от 30 мин — floor подстрахован и на клиенте.

### #185 — адрес сервера в title вкладки

**Changed:** `shell.js` — `applyDocTitle()` ставит `document.title` = `<endpoint_host> · razdacha`; зовётся из `app.js` (loadAll) и `settings.js` (после сохранения). Нет адреса — «razdacha».

## 2026-08-17

### #181 — встроенный пул одной строкой

**Changed:** `tunnels.js` — раздел «Дефолтные туннели по странам» убран, все туннели в одном списке; `poolCountryLabel`/`poolCountries`/`isCountryPool` удалены.
**Beware:** страна сервера больше не рисуется (geo-IP убран, ADR 0018); `.pool-country` в CSS осталась неиспользуемой.

### #178 — свои туннели выше дефолтных страновых пулов

**Changed:** `tunnels.js` `view()` — порядок разделов: свои туннели сверху, страновые пулы ниже (`restCard + poolsCard`).

### #171 — экран туннелей: раздел дефолтных страновых пулов

**Changed:** `tunnels.js` делит список на «Дефолтные туннели по странам» (builtin+pool) и «Свои туннели»; строка вынесена в `tunnelRow`; стиль `.screen-section`.
**Beware:** признак странового пула — `builtin && pool`, не разбор имени (имя переименовывают).

## 2026-08-15

### #149 — отказ правила виден в панели

**Changed:** строка правила красится в `rule-dead` с бейджем «⚠» при недоступном туннеле (выключен, удалён, пул мёртв, выключено второе звено); панель пробника `probeView` над списком правил.
**New surface:** `api.route.test` в `api.js`.
**Beware:** `servers_alive: null` — «неизвестно», строку не красит. Опрос молчит, пока курсор в `input`/`textarea` — перерисовка сносит набранное.

### #152 — третье состояние строки правила: канал молчит
**Changed:** `tunnelSilent(t)` — включённый туннель со `status === 'down'`; `ruleTrouble()` отдаёт `{level: 'dead'|'silent', text, okAt}`, недоступность по цепи проверяется раньше молчания. `.rule-quiet` — «канал не отвечает, трафик теряется в туннеле», красное `⚠` — за отказом (ADR 0013).
**Beware:** `okAt` секундами из БД — после перезапуска демона отметка теряет доли секунды.

### #153 — бейдж вместо цифры для неизвестного пинга пула
**Changed:** `tunnels.js` красит бейдж «пинг неизвестен» вместо цифры, когда `ping_ms == null`.

### #150 — кнопка стартового набора на пустом экране правил

**Changed:** `emptyView()`, `presetLists()`, `liveTunnels()` и действия `preset-rule`/`go-tunnels`; `modalRule(id, seed)` принимает необязательный `seed`.
**Beware:** кнопка только заполняет форму нового правила (ADR 0014) — сохраняет пользователь через `POST /api/rules`, системного правила нет; целью берётся первый включённый туннель.

### #104 — трафик пира виден и у офлайнового

**Changed:** `peers.js` показывает `↓ rx · ↑ tx` не только у онлайн-пира: у офлайнового рядом с «был <когда>», у ни разу не подключавшегося — `↓ 0 Б · ↑ 0 Б · не подключался`.
**Beware:** трафик — с последнего поднятия wg0, не за всё время; поднятие `wg0` счётчики сбрасывает, перезапуск демона — нет; у выключенного пира поля `null`.

### #161 — «страна — со слов каталога» в трёх местах

**Changed:** `poolCountryLabel()` в `tunnels.js` помечает страну пунктиром и `cursor: help` с подсказкой, что страна со слов каталога и не проверена.
**Beware:** сервер без подписи спан не получает; бакет «без страны» подсказки не имеет.

## 2026-07-29

### #135 — адрес на карточке туннеля

**Changed:** `tunnelEndpoint` читает `t.host`/`t.port` вместо несуществовавшего `t.parsed`; макет `ui/mock/tunnels.html` приведён к той же форме.
**Beware:** разобранного конфига панель не видит вовсе — в нём ключи. Полей нет — рисуем «—», это законное состояние, а не поломка.

## 2026-07-28

### #114 — бейдж свежести рядом с версией

**Changed:** `#brand-fresh` в шапке; `release.latest()` в `api.js` ходит к GitHub из браузера, ответ живёт в `sessionStorage` (6 часов на удачу, 15 минут на отказ).
**Beware:** запрос делает браузер, а не демон — почему, в инвариантах слоя. На версии `dev` бейджа нет: сравнивать не с чем. Сравнение семантическое, иначе `v0.2.10` оказалась бы старше `v0.2.9`.

### #33 — версия в хедере и в диагностике

**Changed:** `#brand-version` в шапке, сводка версий на экране диагностики и в скачиваемом отчёте.
**Beware:** версии нет — метка скрыта целиком, пустого места не остаётся. Расхождение с `installed_version` красит метку и показывает обе версии: это случай «собрал новое, а systemd крутит старый».

### #99 — вторая выпадашка «и дальше через…» в модалке правила

**Changed:** `screens/rules.js` — выбор второго звена, цепь видна в строке правила; в выпадашке только туннели с `source = warp`.
**Beware:** экран туннелей не меняется — цепь это свойство правила, а не канала. Выключенный второй туннель роняет правило целиком: выход одним первым звеном показал бы ресурсу не тот адрес.

## 2026-07-27

### #100 — кнопка «+ WARP» на экране туннелей

**Changed:** `screens/tunnels.js` — кнопка и действие добавления, `util.js` — подпись WARP в списке туннелей.
**Beware:** до нажатия демон в Cloudflare не ходит — контракт тот же, что у туннель-пула (ADR 0010). Кнопка не спрашивает ничего: имя и ключи приходят от Cloudflare, настраивать нечего.

### #95 — каталог списков в модалке правила

**Changed:** форма — поток сверху вниз, каталог во всю ширину окна, отметка красит фон.
**Beware:** колонок три, а не четыре, как обещала спека; 24 названия влезают в строку.

### #90 — блок оповещений в модалке настроек

**Changed:** чат, токен, галочка и «отправить тестовое»; настройки оповещений едут отдельным запросом.
**Beware:** отдельного экрана нет — четыре экрана держатся. Поле токена всегда пустое: сервер его не отдаёт, подсказка говорит «сохранён», а пустое поле при сохранении не шлётся вовсе.

### #89 — интервал проверки туннелей в настройках

**Changed:** шестое поле в модалке настроек, единицы — минуты, не часы.
**Beware:** нижняя граница в 30 секунд стоит в демоне, а не в списке вариантов: каждый прогон пробивает обычные туннели настоящим запросом (ADR 0011).

### #87 — имя `.conf` приходит от демона

**Changed:** `peers.config()` возвращает `{ text, filename }`, имя читается из `Content-Disposition`; `slug()` из `util.js` удалён.
**Beware:** имя файла клиентского конфига панель не строит — из него WireGuard берёт имя туннеля, и правила длины и алфавита знает только демон. Второй слагификатор давал разные имена для одного пира.


## 2026-07-26

### #63, #69, #71, #75 — пул как айтем списка и модалка «Детали»

**Changed:** `ui/dist/js/screens/tunnels.js` — пул рисуется тем же `.row`, что и туннель; модалка «Детали» со списком серверов. Клиент WebSocket удалён, опрос стал основным путём.
**New surface:** `stamp(iso)`, `tunnelPool(t)`, `tunnelLabel(t)`. Стенд на замоканном ответе — `ui/mock/tunnels.html`, вне `dist`.
**Beware:** как читается пул и что он показывает выключенным — в инвариантах слоя.

### #47, #50 — модалка правила

**Changed:** раскладка в две зоны с фиксированной высотой окна; подсказки, поиск по каталогу, чипы выбранного, подсветка перекрытий.
**Beware:** незакрытая `@media` в `app.css` утащила весь блок стилей внутрь медиазапроса — на десктопе они не применялись, и это выглядело как невыполненная задача. Появилось при механическом разведении конфликта. Сторож — `ui/ui_test.go`.

### #46 — ход проверки в диагностике

**Changed:** строки обновляются по мере готовности, кнопка не плодит запросов, «Проверить заново» прогоняет и туннели.
**Beware:** высоту списка нельзя задавать через цепочку flex-родителей внутри `<details>` — его содержимое ведёт себя как flex-элемент не во всех браузерах, и прокрутка пропадает молча.


## 2026-07-25

### ui-prototype — кликабельный прототип четырёх экранов

**Changed:** `ui/prototype/` — статический макет на ванильном JS, без сборки и внешних зависимостей; свой QR-кодер вместо CDN.
**Beware:** контракт неполон — публичного ключа сервера нет ни в одной сущности API, а без него клиентский `.conf` не собрать. График трафика показать не из чего: временные ряды не хранятся. Статус туннеля дублируется между экраном туннелей и `/api/diag`.


## 2026-10-20 — #231 per-client Internet exit

Client routing controls expose shared rules or one tunnel, preserve disabled/missing selected targets visibly, and require Apply. Cards summarize routing; save marks pending, and authenticated Apply status restores that state after reload. Help states IPv4 TCP/UDP support, blocked IPv6/ping/other protocols/private forwarding, and retained gateway access. Async routing callbacks obey modal/session lifetime (ADR 0028).

Implementation and verification record: [task 231](../../tasks/done/231-peer-egress.md). The bounded isolated Linux fixture and independent synthetic-browser checks passed; the completed record retains the failed first attempt and exact evidence boundaries. The fixture uses veth identities and a synthetic SOCKS exit, not encrypted incoming WireGuard or a DO client flow.


## 2026-10-20 — #236 visible client routing

The client inventory now separates identity/activity from the configured route and
exposes Routing and Configuration directly. A named radio editor explains shared rules
versus one Internet exit, target availability and protocol scope. Save errors stay inline;
Save and Apply remain explicitly separate. Global dirty state is never presented as a
per-client activation fact. No remote assets or frontend build step were added.

Verification: see [task 236](../../tasks/236-client-routing-ui.md).
