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
