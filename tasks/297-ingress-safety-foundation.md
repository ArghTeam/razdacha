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
checks and the repository suite are run before delivery. A static Linux test binary
is supplied to the primary operator for reviewed isolated namespace execution;
no host access, live tunnel probe or deployed-source change is part of this slice.

The opt-in kernel fixture proves bounded new/established connection blocking,
old/new port union, unrelated listener survival, invalid-plan preservation and
release inside its own namespace. IPv6 wire evidence is conditional on loopback
availability. Until its actual execution is recorded, Linux wire results are pending.

This is not proof of full product Apply/recovery, service boot ordering, credential
revocation, existing WG coexistence or an external REALITY client connection.
Those remain required by the umbrella implementation before public exposure.
