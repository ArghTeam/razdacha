---
type: task
tracker: "#300"
title: Parallel client connection interface preview
status: complete
---

# Parallel client connection interface preview

Refs [#300](https://github.com/ArghTeam/razdacha/issues/300) and umbrella
[#297](https://github.com/ArghTeam/razdacha/issues/297).

## Scope

Create a standalone, interactive preview before functional ingress implementation.
Keep the existing dashboard's four-screen shell, system typography, neutral surfaces,
blue accent, control geometry and shared client identity.

- Network settings retain WireGuard and add an optional VLESS + REALITY TCP block.
- Client details allow independent WireGuard and Reality selections and one routing
  choice, with method-specific profile copy/download.
- Drafts block profile delivery until saved. Save creates local pending state;
  Apply simulates activation. Key-management confirmation describes reconciliation
  and possible interruption of other connections.
- All content and profiles are synthetic English examples. No backend request,
  real credential, application mutation or transport listener is included.

## Checkout

Designated primary: `/Users/romanzhigalkin/Web/razdacha`; remote `origin`
(`git@github.com:ArghTeam/razdacha.git`); target `main`; freshly fetched base
`001008d0d0e9761a9f0016cfae82a1f77d932dce`.
Implementation worktree: `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/feat-300-ingress-ui-preview`,
branch `feat/300-ingress-ui-preview`.

## Review surface

[Preview](../../designs/reality-ingress/index.html) and
[viewing instructions](../../designs/reality-ingress/README.md).

The page opens directly through `file://` or a loopback static server. UI state is
memory-only and resets on reload. Local Chromium checks passed for file-open behavior, independent transport choices,
draft/profile delivery locking, Save/Apply, pending key replacement/revocation, initial and rerendered-row focus
restoration, Escape cleanup, Network persistence, and
320/390/1440-pixel geometry. No external requests or JavaScript errors were observed.
Independent design evaluation passed after two bounded revisions. The final
evaluation verified pending key semantics, modal cleanup and focus restoration,
file-open behavior, responsive geometry and unchanged visual direction. The user
accepted the preview and authorized merging it and serving the static page on the
designated staging gateway. Design/review delivery is complete; static deployment
and its postflight checks remain the primary operator's next action. No product
transport or server acceptance is claimed.
No product behavior or maintained runtime invariant changes.
