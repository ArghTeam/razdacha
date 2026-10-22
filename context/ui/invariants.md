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

- Client, tunnel and rule inventories each have exactly one trailing vertical-more menu per row. All operations live in the shared body-mounted menu; rule movement has disabled boundary states. Read-only state and configured routes remain visible. The unified dialog keeps
  name/enabled state and Internet routing immediately discoverable, with server-owned
  WireGuard configuration in a separate disclosure. Creation reveals that disclosure
  and its loading/ready/retry state without hiding the handoff below the first view.
  Configured routes and global
  pending status do not identify an individual active route. Whole Internet scope,
  gateway access and no direct fallback remain explicit (ADR 0028).
- One client Save sends only changed name/enabled/exit fields in a transactional
  PATCH. Same-client writes are sequenced across replacement dialogs and recheck the
  initiating session before dispatch. Same-session durable success can update inventory
  and pending state after close, without replacing a newer draft. Inventory loads
  enforce session and request order. Errors/retries retain drafts; unavailable tunnel
  selections remain selected and blocked rather than silently replaced.
- Configuration loading/retry never replaces the editor. Saved rename invalidates
  text, QR, filename and download URLs, even after another Details dialog opens for
  that client. Copy/Download use saved identity; unsaved name edits disable delivery
  with a separate adjacent explanation, cleared on revert or confirmed save.
  Clipboard text is intentionally external; only callback feedback is lifetime-guarded.
  Close, Escape, header close and backdrop uniformly discard drafts, stated in the footer.
- Routing and affected rules need Apply, while enabled/deleted peers are synchronized
  from saved state to WireGuard on the daemon's 10-second interval and can revoke
  access before Apply. No per-client activation or exact revocation deadline is claimed.
  Deletion uses inline affected/orphaned-rule confirmation; cancellation preserves draft
  and explicit orphan reassignment/activation instructions remain (ADR 0026).

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

## Diagnostics and shared shell

- Shared modal action labels wrap within their available width at enlarged text sizes.
  The Tunnel editor keeps header/footer outside its body scroller and reveals focused
  name/configuration fields within the body. Its existing input, focus-trap and secret
  lifetime contracts remain unchanged.
- Header navigation retains exactly four screens. English labels and precise version
  readouts wrap without overlap or ellipsis at narrow widths/enlarged text. The global
  Apply bar reserves its measured height for content and notifications; global pending
  does not identify any individual client's active route.
- Only supported diagnostic checks, rerun and allowlisted summary download are active.
  Logs and generated-configuration viewing are explicitly unavailable in this version;
  no viewer request is dispatched to absent handlers. Empty/error/offline data is not
  presented as unimplemented support or a fabricated healthy result. Long raw check
  details remain local and keyboard-scrollable. Focused detail fields fit between
  measured header/Apply chrome, including enlarged text.
- Summary reports export known check labels/status enums, numeric settings/counts,
  version cores and validated commit text. They exclude names/IDs/endpoints/DNS,
  credentials and arbitrary diagnostic/runtime/version-suffix text. This is explicit
  field selection, not universal redaction; sharing still requires review.
- Diagnostic loads/reruns and touched shared load/navigation/Apply feedback capture
  initiating session lifetime. Old sessions cannot overwrite results, dispatch a next
  queued probe or repopulate feedback. An active rerun retains its progress snapshot
  against older polls; failures keep an actionable inline error and retry. Obsolete
  navigation completions cannot clear a newer screen's missing/offline state or post
  errors, even within the same session.
- Notifications reveal new entries within their own bounded stack without scrolling
  the page or stealing unrelated focus. Removal of a focused entry continues useful
  visible focus. New feedback continues notification-local focus when an older focused
  entry would be scrolled away; unrelated page/modal focus stays unchanged.
  Authentication cleanup owns the login focus target. They retain at
  most three recent entries, deduplicate identical repeats,
  support dismissal and expire after four seconds. Long content remains within bounded
  scrollable space above Apply. Auth dismissal clears DOM/timers. Existing form errors,
  modal/menu focus and secret cleanup retain their established ownership/lifetimes.

## Rule editor integrity

- The rule dialog reads Name, Resources, Action, Clients in one primary body scroller.
  Desktop resources sit beside one grouped outcome/client pane; the pane stays together
  during long-source browsing, with a persistent Save/Cancel footer and Apply reminder.
  Validation and server errors occupy an explicit row above Name; revealing errors
  never moves identity below the resource/outcome workspace. Ordering and DNS explanations
  are optional help. Client search/count and separate disabled/whole-Internet status
  retain fully wrapping identities. Catalog and selected-source review are compact
  disclosures with six-row pages and search; hidden pages retain checked inputs. Counts
  and unavailable-source retention warnings remain discoverable when closed. Existing URL sources remain outside the catalog disclosures.
- Tunnel selection has a physically associated label. Advanced WARP controls are
  optional and open for existing chain references, including unavailable references;
  changing to Direct/Block still clears the saved chain through the existing payload.
  Keyboard focus scrolls fully into the rule dialog/list viewport, including at 200%
  text size; disclosure and chip controls preserve keyboard focus after removal.

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
- Development-only Chromium regressions in `ui/tests` run sequentially in the separate
  `ui-browser` CI job with pinned Playwright, its matching browser and a supported runner
  image. They use actual UI modules with synthetic local/intercepted API responses.
  Node and Playwright are never daemon build or release dependencies; these checks do
  not verify deployed networking or backend cookie delivery timing.

## Settings persistence boundaries

- Network, Schedules, Telegram and Backup have separate save boundaries and retained
  drafts with persistent local status/errors. Save never silently saves another
  section. Network changes require global Apply; timers use saved schedules directly,
  while Apply updates the active configuration record. Telegram/Backup persistence
  does not require routing Apply.
- Configuration endpoints perform full-record read/modify/write. Panel saves serialize
  across sections and replacement dialogs; only changed ordinary fields and explicit
  new secrets are sent. Blank secret fields remain omitted. Queued previous-session
  intent is canceled before dispatch; confirmed responses update their own current
  section only. Closing drops drafts and clears application-held secret fields.
- Notification tests and backup sends use saved settings without an implicit save;
  unsaved section edits disable delivery. Backup sends refresh delivery history only,
  without replacing drafts. Direct full-backup download does not save the form and
  discloses its unencrypted client-key contents (ADR 0016).

## Tunnel observation labels

Tunnel rows label measured latency as HTTPS, distinguish inconclusive and historical
observations, and expose runtime-resolver DNS separately. Transport remains unknown;
a successful HTTPS observation does not promise every protocol, client or pool member.
Manual check completion carries the additive signals into current inventory under the
existing session/interaction guards. ADR 0031 defines backend scope and freshness.

## Unified visual system

- The [design system](../../docs/ui-design-system.md) defines spacing, typography,
  semantic status colors, 40-pixel regular controls and 44-pixel narrow/coarse-pointer
  targets. Text and icon buttons share height; each vertical-more trigger has a square
  hit area. Light and dark themes retain identical geometry.
- Main content is bounded at 1240 pixels. Compact confirmations, regular editors and
  wide workspaces use shared tiers. Dialog header/footer stay outside one primary
  body scroller; small screens use nearly the full viewport without horizontal overflow.
- Rule source, selected-source and client pages hide rows without deleting or filtering
  saved selections. Search applies across all pages. All checked inputs remain part of
  Save, including unavailable sources and hidden pages. Rule outcome and client scope
  share one desktop pane, preserving the mobile semantic reading order.
- Tunnel server details use search and one primary dialog scroller, with grouped
  observed states. Pool rows never infer health for disabled or unmeasured servers.
- Settings section navigation remains visible during body scrolling. Independent
  section saves, retained drafts and secret/delivery boundaries remain unchanged.
- Shared destructive confirmation identifies the target and focuses Cancel. Closing,
  Escape, replacement and session dismissal cancel unconfirmed intent; confirmed
  operations retain their existing single-flight and initiating-session checks.
