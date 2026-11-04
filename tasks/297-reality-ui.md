---
type: task
tracker: "#297"
title: Authenticated Reality client interface
status: in-progress
---

# Authenticated Reality client interface

Implement the approved connection workflow in the embedded panel, using real API
responses rather than the standalone preview. WireGuard and Reality are independent
client methods sharing routing. Existing responses without connection fields retain
WireGuard defaults. Reality profiles are disclosed only when the server reports
available; pending and guarded states never fabricate a usable profile.

The UI owns no server key generation or transport health inference. Credentials,
QR content and download URLs retain modal/session lifetime and are invalidated by
identity/method changes. Replacement sends an expected credential revision and
requires successful backend reconciliation; failures keep delivered copies cleared.

## Checkout

Authorized primary repository: `/Users/romanzhigalkin/Web/razdacha`; remote `origin`,
target `main`, verified base `fc0965eb0a9cf9ef9ebf5b9bd0f718536559fc3a`.
Worktree: `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/feat-297-reality-ui`;
branch `feat/297-reality-ui`. Only embedded UI, browser fixtures and UI context are
owned here. Existing sandbox tunnels and production are outside test actions.

## Acceptance

Verify actual UI modules with synthetic API responses: legacy compatibility,
independent methods, changed-field saves, pending/guarded profiles, text/QR/download
delivery, stale secret responses, close/logout/method switching, revision conflicts,
settings boundaries, keyboard controls and responsive widths. This is frontend
acceptance, not proof of server revocation, network transport or provider connectivity.

## Frontend verification

- `go test ./ui`: passed.
- JavaScript syntax and `git diff --check`: passed.
- All 16 synthetic browser suites passed with pinned Playwright 1.59.1, including
  existing client, settings, shell, rules and tunnel regression coverage. These
  intercepted fixtures never contact or mutate existing tunnels.
- The new Reality suite additionally passed 20 cases after final race refinements:
  independent/legacy methods, authenticated returned URI and QR/copy/download,
  stale method/modal/session responses, pending/guarded/disabled refusal, revision
  replacement/conflicts/failures, incomplete-operation refresh, changed-field
  settings, Reality-only create/Apply/delivery, invalid URI rejection, revision
  changes during confirmation/load, and keyboard/mobile widths 320/390/1440.
- Context records actual profile/session boundaries and observed WG-only statistics.
  Real client transport, server reconciliation, firewall isolation and DNS require
  backend integration and the separately authorized sandbox acceptance by the primary
  operator. No server or SSH actions were performed from this UI worktree.

Backend contract integration will be verified again after its reviewed changes reach
`origin/main`; this frontend branch will merge the fresh target before final delivery.


## Independent review correction

A P2 review reproduction showed that modal-local rotation uncertainty could disappear
on dismissal or replacement. The fix retains pending/uncertain state per client and
initiating session, blocks reopened profile reads while pending, clears any current
same-client delivery on ambiguous failure, and requires a fresh explicit authoritative
reload to recover. Rotation starts/completions invalidate older inventory/status
reads. Previous-login completions cannot affect the new session.

The Reality suite now has 24 cases, adding both failure/reopen orderings, stale reload
rejection and old-session isolation. Client detail/action regressions are rerun for
the correction; maintained context reflects the corrected lifetime.


## Durable mutation and JSON delivery completion

The backend contract permits durable revocation before a failing response for
Reality-affecting peer enabled/method writes, peer deletion and global Reality
settings writes. The panel now retains pending/uncertain recovery for these paths
across dialog lifetimes and rejects older inventory/status/profile responses.
Explicit authoritative peers/settings reload recovers; a deleted peer absent from
that result is removed and its stale dialog closes. Ordinary rename and legacy
WG-only behavior remain covered.

`GET /api/peers/{id}/reality-client-config` supplies an actual authenticated no-store
full sing-box JSON attachment. The optional download fetches only on demand and
checks draft, modal/session, credential revision and mutation epoch before creating
a blob. URI help requires app TUN and DNS-through-proxy configuration; the JSON
contains those routes. Frontend evidence establishes actual API delivery and secret
lifetime only; an operating-system TUN/client connectivity run remains separate.

The final focused Reality suite passes 36 cases, including durable peer-save/delete
and global failure reopen paths, absent deleted-client recovery, stale pre-global
reload rejection, actual on-demand JSON attachment delivery and late JSON refusal
after close, dirty edit, rotation or logout. Static syntax, embedded UI checks and
legacy client details/actions remain required alongside the full browser runner.

All 16 browser suites passed after the durable-mutation/JSON additions. The final
focused suite adds explicit Network recovery and stale recovery-after-client-write
coverage. Final syntax and whitespace checks pass; `go test ./ui` passes.
