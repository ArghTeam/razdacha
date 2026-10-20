# Dashboard control accessibility (#233)

## Provenance

Authorized repository and designated primary: `/Users/romanzhigalkin/Web/razdacha`.
Feature checkout: `.codex/worktrees/fix/233-dashboard-controls`, same branch name.
Remote target: `origin/main`; freshly fetched base: `b95e3cf8ba86008a3443f52103a6a1777220822f`.

## Intended behavior and acceptance

- Accessible modal heading, deliberate focus entry, Tab trapping, inert background,
  stable opener restoration after row rerenders and modal replacement.
- Logout/session expiry close overlays without restoring hidden application focus.
  Existing secret cleanup and async modal/session revisions remain authoritative.
- Overflow menus focus their items and support arrows, Home, End, Escape, Tab,
  outside dismissal and listener/ARIA cleanup.
- Unbroken long names wrap without hiding close controls at 320/375 px.
- Narrow/coarse-pointer controls have 44 px hit areas; small hints reach 4.5:1
  contrast in both themes and disabled rows retain readable text/actions.
- Associated tunnel-select label and contextual accessible names for row controls.

No backend, routing policy, settings-save redesign, draft guard, language migration
or live deployment changes. Independent synthetic-browser verification covers
keyboard, modal/session cleanup, widths, targets and contrast. UI Go guards and
JavaScript syntax checks cover embedded assets. No full accessibility conformance
or live-server behavior is claimed.

## Completed verification

Tested executable UI source: `11e2621540bfeb9fda3beaa0e2c501c10c7b8bbf`.

- `go test ./ui`, edited-module JavaScript syntax checks and `git diff --check`
  passed. CI on initial source `da6b9b8` passed all five jobs: tests, lint,
  pinned sing-box validation and Linux amd64/arm64 builds. Final-head CI remains
  a merge gate.
- An independent browser verifier froze all six changed UI assets and checked
  the four screens at 1440, 768, 375 and 320 px: 16 captures without page overflow.
- Explicit checks passed for named dialogs, intentional form focus, Tab trapping,
  inert backgrounds, opener restoration, menu arrows/Home/End/Escape/Tab,
  menu-to-dialog transitions, screen-render cleanup, empty-focusable dialogs,
  long names, stale configuration responses and authentication dismissal.
- Mobile/coarse-pointer sampled controls meet the 44 px target. The first source
  check found the client configuration disclosure still measured 18.84 px high.
  Commit `11e2621` extended the shared rule to generic `summary`; final measurements
  were 44 px at both 320 and 375 px. This correction was reverified.
- Sampled inactive metadata/actions and small neutral hint contrast were recorded
  as at least 5.98:1 in light mode and 6.85:1 in dark mode.
- All 12 routing/Apply/logout regression scenarios passed, including successful
  and failed saves, explicit shared-mode reset, disabled targets, pending state
  after reload and logout focus. No JavaScript page errors occurred. Blocked
  external release checks and the deliberately failed API request produced the
  expected console resource errors.

The verifier's private source manifest, DOM measurements and scenario receipts
bind the evidence to the tested source. This used loopback synthetic APIs and
synthetic clients, not a live backend, installed deployment or real tunnel. It is
bounded browser evidence, not a formal accessibility conformance audit or proof
of complete secret erasure. Backend routing behavior was not changed or retested.
Final review, final-head CI and merge/synchronization remain separate gates.
