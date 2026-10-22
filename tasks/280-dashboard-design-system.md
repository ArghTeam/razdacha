# Dashboard design system

## Goal

Replace inconsistent row actions, control sizes and narrow sequential forms with one
coherent embedded console. Follow [the contract](../docs/ui-design-system.md).

## Scope and acceptance

Only panel assets, synthetic UI regressions and maintained UI context. No backend,
release or deployment changes. Each inventory row has one vertical-more menu. Shared
controls use 40-pixel desktop and 44-pixel touch targets. The rule workspace exposes
resources and routing together. Client settings/configuration remain unified. Settings
retain independent saves and persistent navigation. Destructive confirmations share
one contract. Existing session, draft and secret lifetimes remain covered by real menu
interaction tests, alongside responsive and enlarged-text geometry.

## Base

Primary checkout: `/Users/romanzhigalkin/Web/razdacha`.
Feature checkout: `.codex/worktrees/feat/280-dashboard-design-system`.
Remote `origin`, target `main`, freshly fetched base
`af68bd8bef4258c4fdd05b7d89113b089bd92fc3`.


## Final behavior and verification

The feature integrates target `origin/main` at
`6ff099cc63f36a7900a9b5f0b7877da73d9b2e4b` by fast-forward after its initial base.
Shared controls use 40 pixels on desktop and 44 on narrow/coarse-pointer displays.
Inventory operations use one menu. The rule editor groups action and clients beside
resources; six-row search/page controls preserve all hidden selected values. Pool
server groups use search and six-row pages within the sole dialog body scroller.
Settings navigation and section save boundaries remain independent. Confirmation
acceptance captures modal revision, initiating session and entity identity; stale
controls cannot close replacement drafts or issue deletion. Tunnel progress stays
visible after its menu item has been detached.

Verified with pinned Playwright 1.59.1 and matching Chromium from an existing local
installation: all 14 browser suites passed through `node ui/tests/run-browser-tests.js`.
The new design suite covers 21 real menu/responsive/long-source/confirmation/async
progress cases; existing secret cleanup, settings, rule, tunnel and shell regressions
remain enabled. `go test ./ui`, runner unit checks, changed-module syntax checks and
`git diff --check` pass. Synthetic screenshots stay outside the repository. The
checks establish frontend behavior, not installed networking or backend cookie timing.
Root owns final independent review, versioning, release and staging deployment.
