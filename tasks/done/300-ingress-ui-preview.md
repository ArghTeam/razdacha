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


## Static staging acceptance

PR #301 merged at `414108c88c1e78e6aea8c52a8d10912c974b6638`. The primary
checkout was fast-forwarded to the fresh `origin/main` at that commit before delivery;
existing local edits were preserved. This acceptance adds a static preview at
`/preview/reality/` on the designated staging gateway, not product functionality.

Public and loopback HTTPS returned 200 with valid TLS and exact reviewed HTML
SHA-256 `4d86988ea4fd15328d22d16acd884ee8548c7cf4e14cdc821a7e0db92bf9e806`.
Responses carried no-store/noindex protections and a CSP with `connect-src 'none'`.
The existing panel body remained identical with HTTP 200; the unauthenticated API
retained its expected 401 response. All three service main PIDs stayed unchanged.
Only nginx was gracefully reloaded; application services were not restarted.

A fresh private nginx configuration backup and consistent SQLite backup were retained.
Strict comparison preserved all peer, rule, tunnel, setting and registration rows,
plus daemon/runtime/configuration bytes. No application or network-policy mutation
occurred. The first location attempt returned 500 because alias processing appended
an index path. Exact-body verification refused that attempt; rollback restored the
original nginx configuration and all three services remained active. A reviewed
literal-root/try-files correction passed the second delivery attempt.

Public Chromium postflight passed exact-content/CSP checks, independent WG/Reality
selection, unsaved profile-delivery blocking, settled Escape/profile cleanup,
Network settings and 390-pixel mobile layout without overflow. There were no API or
external requests or page errors. Screenshots and raw evidence remain private.
The interface is synthetic and nonfunctional; production was outside the scope.
This does not prove transport operation, real clients, revocation or DPI resistance.
