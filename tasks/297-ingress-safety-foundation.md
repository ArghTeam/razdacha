---
type: task
tracker: "#297"
title: Dormant public ingress safety foundation
status: review
---

# Dormant public ingress safety foundation

Part of [#297](https://github.com/ArghTeam/razdacha/issues/297) and proposed ADR 0032.
This bounded slice adds reusable authority/locking and an endpoint-specific kernel
barrier. It exposes no new listener and changes no existing runtime behavior until
the following backend integration explicitly wires it.

## Checkout and ownership

- Authorized repository and designated primary: `/Users/romanzhigalkin/Web/razdacha`.
- Remote `origin`, target `main`, freshly fetched base
  `fc0965eb0a9cf9ef9ebf5b9bd0f718536559fc3a`.
- Worktree `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/feat-297-ingress-safety`;
  branch `feat/297-ingress-safety`.
- Owned source: new `internal/ingressguard` package and new netstack public-guard
  files. Existing daemon, store, API, generator and packaging files are outside this
  slice. Their integration owner updates the affected maintained layer invariants.

## Behavior and acceptance

The [package contract](../internal/ingressguard/README.md) defines strict private
authority, exact config hashes, old/candidate endpoint union and short shared locks.
Startup holds the same short lock across reading authority, persisting guarded
intent and installing the matching barrier. It never opens the DB or waits for the
daemon publication gate; no lock is held across a runtime restart.

The new kernel table affects local INPUT TCP endpoints only. The WG guard, routing
table, forwarded traffic and unrelated ports remain outside its ownership. IPv6
wildcard coverage is conservative; malformed endpoint plans fail before mutation.
Legacy compatibility accepts explicitly private DNS/loopback TProxy shapes and
never removes an existing barrier when authority is absent.

Local tests cover ambiguous/corrupt authority, unsafe files/parents, canonical
endpoint union/coverage, exact checked bytes, unsafe legacy listeners, failed
installer behavior and serialized startup/controller transactions. Relevant race
checks, the repository suite, static checks and diff whitespace checks passed. A
static Linux test binary was supplied to the primary operator for independently
reviewed isolated namespace execution. No live tunnel probe or application
deployment is part of this foundation slice.

The primary operator ran the reviewed opt-in kernel fixture on designated staging
on 2026-11-04. It passed in 1.69 seconds, including IPv4 and IPv6 rather than an
IPv6 skip. The test exercised bounded new/established connection blocking, old/new
port union, unrelated listener survival, invalid-plan preservation, address scope
and release inside fresh network/mount namespaces. The static test executable
SHA-256 was `0c5d1d544c9882155e7a71337e4a799de0cf3114ae42658152ddca39e644cd15`;
source commit `428bf05b852e0b28ccaa93e49f7c928518642e18` differs from initial
`2ee135d40cc15a7fa3b54fd587eb62cc6d2e9485` only by two reviewed lint/style fixes.
The executable hash stayed unchanged. Existing application/runtime/nginx main PIDs,
configuration and runtime hashes were unchanged, and the owned cgroup was removed.
Raw evidence stays private; no existing provider tunnel was changed or probed.

This is not proof of full product Apply/recovery, service boot ordering, credential
revocation, existing WG coexistence or an external REALITY client connection.
Those remain required by the umbrella implementation before public exposure.
