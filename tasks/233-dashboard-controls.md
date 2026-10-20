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
