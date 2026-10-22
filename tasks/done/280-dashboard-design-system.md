# Dashboard design system

## Goal

Replace inconsistent row actions, control sizes and narrow sequential forms with one
coherent embedded console. Follow [the contract](../../docs/ui-design-system.md).

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


Final review follow-up: explicit error/name/workspace grid rows preserve form order
when validation or server errors appear. The design matrices exercise both errors
at desktop and mobile widths, checking order, retained drafts and visible frame bounds.


## Implementation acceptance

Merged as [PR #281](https://github.com/ArghTeam/razdacha/pull/281), commit
`7c2f8a0ed540fc688350c9d480f8f65c1c3706f6`, from independently reviewed feature
HEAD `d9164fca9166aa4a690cd28cd1244a97d0a41a97`. Design evaluation passed after
expanded-state refinements. Independent code review accepted the stale-confirmation
and error-grid follow-ups; all six hosted CI checks passed. Root synchronized the
primary checkout to the merged tree before release preparation. These implementation
checks do not claim a completed staging deployment.


## Background polling follow-up

Read-only staging acceptance exposed periodic menu dismissal. During the fix,
independent synthetic review reproduced a retained-menu action-intent race in the
uncommitted candidate. [PR #282](https://github.com/ArghTeam/razdacha/pull/282), merged as
`2acb9d5adbeba6a59e47d3f4572cfc7e79bbe73f` from reviewed HEAD
`a77edca064c1c22782255531c83605fb70f77aff`, protects both poll admission and the
final render of an earlier background read. Accepted current-session inventory stays
fresh; a later poll resumes rendering after interaction ends. Explicit navigation,
mutations and session expiry retain cleanup. Rule/tunnel Enable and Disable menus
capture validated boolean intent rather than invert a newer background state.

Independent review and all six hosted CI checks passed. Twelve focused controlled-clock
and native-pointer cases cover both races, resumed refresh, navigation, expiry and both
toggle directions for both domains. Existing rule/tunnel lifetime, operations,
integration, shell and embedded-asset checks passed. Primary synchronization to the
merged tree was verified before preparation of the next candidate. Final installed
staging acceptance is recorded separately from these implementation results.


## Staging acceptance

The final staging candidate is **v0.11.0-rc.7**, commit
`2acb9d5adbeba6a59e47d3f4572cfc7e79bbe73f`. Published archive SHA-256:
`329257151a8bd1642ddef39f7b0bf98256f0e7f4701c8f58d0043adad55198b5`.
The bounded upgrade check and run both completed with accepted strict preservation
checks and a fresh full stopped snapshot retained privately. The daemon, sing-box and
nginx are active; daemon/setup CLI and authenticated API report the expected release,
and the authenticated API commit matches the release. Schema is 12, sing-box runtime is 1.12.25, the native guard is absent with
the expected shape, and WireGuard peer identity matches its pre-upgrade digest.
Interim rc.6 acceptance preceded the bounded polling follow-up; rc.7 is the final
verified candidate. No release tag was rewritten.

Actual authenticated browser postflight passed **48 checks** with 24 screen/theme/
viewport frames and 16 mobile dialog frames at 390 and 320 pixels. All 11 served UI
assets matched the release exactly. Visible version/header, client settings and
configuration cleanup, rule workspace/scrolling, manual tunnel editing, pool details
and all four Settings sections passed. A menu remained open for 16 seconds and natural
pointer Edit opened the intended editor afterward. All mobile dialog header/footer/body
scroll-end checks passed after settled geometry measurements.

There were zero persisted application-configuration mutations and zero JavaScript
errors; Apply was not invoked. Two verified nonpersisting configuration-parse previews
and owned-session logout were allowlisted. One external release lookup was blocked.
The owned login session was confirmed revoked and its temporary authentication file
was deleted. Screenshots, operational evidence and snapshot paths remain private.
These checks establish installed UI/API/operator behavior, not VPN-client or media
end-to-end connectivity, backend cookie timing in every race, or interruption-free
upgrade behavior.
