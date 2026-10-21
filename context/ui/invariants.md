---
type: invariants
title: Ui
description: Static HTML/CSS/ES modules embedded from ui/dist; defaults instead of options
---

## Invariants
- Интерфейс — статика без сборщика в `ui/dist`, уезжает в бинарник через `go:embed`. Никакого npm и node в цепочке сборки демона.
- Внешних ссылок в статике нет вовсе: ни CDN, ни шрифтов, ни картинок. Всё, что нужно панели, лежит рядом — иначе оно не попадёт в `go:embed`. Единственный внешний запрос — проверка свежего релиза у GitHub, и её делает **браузер**, а не демон: сервер наружу не ходит, панель работает и без исходящего интернета, а бейдж просто не появляется (#114).
- Пустота не заполняется выдумкой: нет данных — так и говорим («не проверялся», «данные появятся позже»), а не показываем ноль.
- A current-session authenticated `401` returns to the login gate; successful login
  returns to the selected screen. A response from a previous session cannot dismiss
  a newer login. Incorrect-password login retains its local error handling.
- Высота элементов с прокруткой задаётся им самим, а не цепочкой flex-родителей: цепочка рвётся молча (см. `<details>` в каталоге списков).
- Меню строк живёт в `body`: внутри `.card` с `overflow: hidden` его обрезает.
- Отметка строки в сетке красит фон и рамку, но не меняет метрику текста: `font-weight` по `:checked` расширяет название, строка переносится, и сетка пересобирается под курсором.
- Пул узнаётся по `source`, а не по `type`: тип означает протокол, и какой он — решает драйвер каталога (ADR 0015), а не форма конфига. Пинга у источника может не быть вовсе: `ping_ms = null` показывается словами «пинг неизвестен», а не нулём.
- Выключенный пул не показывает ни живых, ни выбранный сервер: цифры у туннеля, которого нет в конфиге, это ложь, а не ноль. Число серверов подписано «известно N» — список в БД больше того, что каталог отдаёт за раз.
- Четыре экрана, не больше (`docs/00-vision.md`). Экран входа — гейт, а не пятый раздел.

## Key entry points
- `ui/dist/js/api.js` — единственный слой доступа к API: экраны не зовут `fetch` сами.
- `ui/dist/js/shell.js` — тосты, модальные окна, меню, общее состояние.
- `ui/ui_test.go` — сторож: баланс скобок в css и ссылки из `index.html`.

- Deleting a client names affected/orphaned rules before confirmation, reloads rules after success and explains explicit reassignment, activation and Apply. Selected empty disabled rules remain editable and show the inactive reason. Assignment alone does not enable a rule (ADR 0026).

- Client configuration text, filename, QR pixels and download object URLs belong only to
  the current modal. Closing or replacing it clears these copies and invalidates both
  successful and failed pending configuration requests, including peer creation.
  Logout invalidates the modal before waiting for the server; session expiry shares
  the same cleanup. This removes application references, not JavaScript heap contents.
- Tunnel editor raw text, pending debounce and last parse state share that modal
  lifetime. Late raw/parse responses cannot refill closed or replaced editors; newer
  input wins over older parse responses. Session checks returning 401 invalidate the
  modal as other authenticated API calls do; incorrect-password login stays local.
- Session revisions distinguish ordinary modal closure from logout/expiry. A completed
  peer creation still updates pending-change state after ordinary close/replacement;
  completion from a previous session cannot repopulate UI state. A late old-session
  401 cannot trigger frontend dismissal of a configuration opened after a new login.
  Backend invalid-session responses still expire cookies; late cookie processing can
  require another login. Browser stale-401 evidence without Set-Cookie proves only
  notification/rendering lifetime, not cookie delivery timing.
- Logout clears secrets immediately and prevents a new login until its response
  settles, including failure. This prevents a late logout cookie from deleting a
  newly issued session cookie; form submission by Enter obeys the same pending gate.

## Configuration lifetime verification boundary

The synthetic actual-API fixture verifies frontend lifetime, not backend cookie
delivery or installed deployment. Cleanup removes application references without
promising heap erasure or immunity to delayed cookie expiry. A dataset marker alone
does not prove visible rendering. Keep case-specific coverage and inconclusive
observations explicit in the [completed specification](../../tasks/done/216-clear-ui-config-secrets.md).

- The client inventory exposes a direct Routing action beside Configuration and labels
  its route as configured, never inferred active. The editor uses explicit Shared rules
  and Whole Internet radio choices with a conditional exit selector. Disabled/missing
  exits remain selected and visibly blocked; an empty inventory cannot save an empty
  target. Save failures stay in the editor with its selection preserved.
- Saving routing marks global pending state; Apply activates configuration. Authenticated
  Apply status restores global dirty state after reload, but cannot identify which peer
  changed or its individual active route. Health and enabled status never imply successful
  client connectivity. Whole Internet mode alone shows its IPv4 TCP/UDP scope, blocked
  IPv6/ping/other protocols/private forwarding, gateway access and no direct fallback.
  Shared-mode guidance describes tunnel, direct and block rule actions neutrally (ADR 0028).
- Routing loading, retry and save callbacks obey modal/session lifetime; an old response
  cannot replace a newer editor. Existing configuration download/QR cleanup is unchanged.

- Tunnel usage includes client exit references; rule assignment identifies clients whose whole-Internet mode takes precedence. Domain diagnostics are explicitly labeled as shared-rule scope and do not claim to evaluate a selected client exit.

## Shared dashboard controls

- Dialogs expose their visible heading as an accessible name. Opening preserves
  intentional form focus or focuses the heading; Tab stays inside and application
  and login backgrounds retain and restore their prior inert state. Closing restores
  a visible opener or matching rerendered row control, then the selected tab as
  fallback. Replacement preserves the original opener. Auth dismissal suppresses
  application focus restoration. Focus management does not replace modal/session
  revision checks or configuration secret cleanup.
- Body-mounted row menus expose menu semantics, move focus with arrow/Home/End
  keys, close on Escape/Tab/outside interaction, and clean listeners and anchor
  ARIA state. Every screen render dismisses stale menus after replacing rows so
  focus can return to the current equivalent anchor.
- At narrow widths or with a coarse pointer, shared controls have non-overlapping
  44 px minimum hit areas. Long unbroken names wrap without displacing modal close
  controls. Disabled rows do not use whole-row opacity; small neutral hints remain
  readable in both system themes. Repeated controls identify their row in their
  accessible names; the rule tunnel selector has an associated label.

## Rule editor integrity

- Community source selections absent from a partial or unavailable catalog remain
  checked and visibly identified as unavailable. An unrelated edit preserves their
  IDs; removal requires the existing explicit checkbox/chip action. API-managed URL
  sources render as escaped read-only text and are omitted from PATCH so they remain
  server-owned. URL-only rules remain editable.
- New rules require an explicit tunnel choice. Empty, missing, disabled and empty-pool
  targets never silently select a replacement; existing references remain visible.
  The editor explains blocked traffic and no direct fallback, distinguishes failed
  health checks from configured rejection, and retains unavailable chain references.
- Resource overlap warnings require intersecting enabled shared-rule clients. Clients
  with whole-Internet exits are excluded because source routing precedes shared rules
  (ADR 0028); partial intersections never claim to shadow every client.
- Rule Save completion may mark pending changes and reload inventory after ordinary
  dialog closure in the same session. It cannot close a replacement dialog or put an
  error into it. Previous-session completions cannot update pending state or inventory;
  older inventory loads cannot overwrite newer loads. Validation and save failures
  retain the draft and a persistent actionable dialog error. Selected empty disabled
  orphan rules remain editable without implicit enabling (ADR 0026).
- Optional Chromium regressions in `ui/tests/rule-integrity.cjs` use the actual UI
  modules with synthetic local API responses. Playwright is a developer prerequisite,
  never a daemon build dependency; these checks do not verify deployed networking.
