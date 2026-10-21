# Honest diagnostics and compact shared shell

Issue: #248. Parent: #242.

## Intended behavior

Diagnostics exposes supported checks, rerun and locally generated allowlisted summary reports. Exports omit names, endpoints, DNS values, credentials and arbitrary check/runtime text; version suffixes are omitted. Full diagnostic details remain local. This is a bounded field-selection policy, not a universal redaction claim. Log viewing and generated-configuration viewing are explicitly unavailable in this version, with no active viewer controls or doomed requests. Loading, offline/error, missing support and empty check results remain distinct; absent data never becomes an invented healthy result. Diagnostic/version loads and reruns verify session and operation lifetime before changing state or sending subsequent checks.

The shell keeps four English navigation labels, a precise readable running version, Settings/Logout and a compact global Apply indicator. Responsive styling preserves readable labels and chrome clearance; global pending never claims a client's individual active route. Existing backend authentication, Apply publication and access behavior remain unchanged.

Focused diagnostic details clear measured header/Apply chrome. New notifications reveal within their bounded stack without page movement or unrelated focus changes; focused removal continues useful visible focus. Notifications retain at most three recent messages, deduplicate repeats, support dismissal and expire. Logout/session expiry clears their DOM/timers. Touched shared/diagnostic callbacks cannot add prior-session feedback; durable form errors remain in the owning forms. Modal focus/inert restoration, menus, secret/download cleanup and session-revision authority remain intact.

## Checkout and phase boundaries

Designated primary: `/Users/romanzhigalkin/Web/razdacha`, `main`. Remote: `origin` (`git@github.com:ArghTeam/razdacha.git`); target: `main`.
Root freshly fetched base `c650a59f6cb6d64a772a3e04f6b092cae2a21faf` and created `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/feat/248-shell-diagnostics`, branch `feat/248-shell-diagnostics`.

Phase 1 owns only shell.js, relevant app.js, diag.js, index.html, new shell/diagnostics tests/fixtures, this specification and a dedicated shell section in the UI log. CSS, docs/06-ui-screens.md, UI invariants, README and existing suites remain with #247. No PR or complete visual acceptance before #247 merge, fresh-main integration and explicit ownership transfer. No backend, other repository, staging, old-host, service or deployment actions.

## Acceptance and verification

New reusable full-app synthetic fixture supports state overrides, exact-origin request interception/journaling, unexpected-request failure, per-method/path hold/fail/release and optional empty/login readiness. Test supported diagnostic checks/report/rerun, unavailable viewers, loading/error/retry, empty state, old-session load/rerun/Apply feedback, toast stress/dismissal/expiry and logout cleanup. All API responses are synthetic; no real probes are run.

Phase 2 additionally checks all four screens and merged client/rule/settings dialogs at 1440/1024/375/320, both themes, doubled computed text, long diagnostic details, keyboard focus and scroll ends. Run maintained browser suites and focused Go API/UI checks, provide local preview/screenshots for independent design evaluation, then commit/push/PR with exact-head review and CI. Node remains development-only. Never merge/deploy.

## Phase integration

Phase 1 passed 16 synthetic functional/session cases. Phase 2 ownership was explicitly
transferred after #247 merged; fetched and fast-forwarded to `origin/main`
`b4ecfbcea24c5f2d1f6b6f8f950c17b7a0a14d12` without rebasing or losing WIP. Shared
CSS/docs/invariants now describe the completed behavior; existing client/rule/settings
semantics and scoped dialog layout remain untouched. The initial integrated runner
passed all seven then-maintained suites; the new shared layout suite is integrated
by automatic discovery. Independent visual and exact-head source gates remain pending.

## Visual repair checkpoint

Independent attempt 1 required three bounded corrections: reveal newest long feedback
inside its stack, preserve useful focus on focused notification removal, and clear
header/Apply when Tab enters long diagnostic details. All are implemented without
changing composition. Focused functional checks pass 23 cases; shared layout checks
pass eight light/dark matrices, including 320/375 × 667, actual Tab and enlarged-text
clearance, newest-error visibility, no document movement, cap/dedup/expiry focus and
passive-focus preservation. Attempt 2 and final exact-head/hosted CI remain gates.
Fetched and merged fresh main `fd52366b8ae5e94702ff8aa8031bb745665afade` after
#256 acceptance. app.loadAll now captures its shared tunnel-inventory read token before
the batch and guards both acceptance and missing/offline/error effects. Actual full-app
bootstrap success/404/503/network races preserve a newer accepted toggle. The focused
functional suite now passes 27 cases, including empty-report Unknown and these races.
New arrivals continue notification-local focus to the newest visible dismiss when
retained notification focus would otherwise be clipped; unrelated focus is unchanged.
A shared modal-action wrapping rule fixes enlarged Backup labels without field changes;
Client/Rule/Settings/Tunnel action geometry is checked at 320px with doubled text.

## Final integration verification

Obsolete navigation success/error feedback checks both session and navigation revision.
The held Diagnostics A/version → Clients → Diagnostics B/404 → release A regression
retains B's missing-capability state. Focused functional suite: 28 cases passed.
Runner discovery/failure/timeout unit checks: 4 passed. JavaScript syntax and diff
checks passed. `CGO_ENABLED=0 go test ./internal/api ./ui` passed after integration.
The final full browser runner and independent visual/source verdicts are recorded
below when complete; hosted CI remains a merge gate. No backend/publication/authentication
changes, live networking, staging operations or deployment were performed.

## Delivery checks

Final integrated browser runner passed all nine maintained suites: Client action
lifetime 29, Client Details 25, Client layout 8, Rule integrity 14, Rule layout 15,
Settings persistence/delivery/lifetime/responsive checks, shared shell/Diagnostics
functional 28, shared layout 16, and Tunnel actions/lifetime 47. The runner's four
failure/discovery/timeout tests also passed. Focused Go API/UI, JavaScript syntax and
diff checks passed. Fresh origin/main remains `fd52366b8ae5e94702ff8aa8031bb745665afade`.

Independent visual attempts 1 and 2 identified bounded scroll/focus/wrapping defects;
all requested repairs have maintained regressions. Attempt 3 and exact committed-head
review remain merge gates alongside all hosted CI checks. Synthetic checks do not
establish installed networking, live delivery, backend cookie timing or deployment
behavior. The local synthetic preview remains available for independent evaluation.
