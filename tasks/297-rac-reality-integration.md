---
type: task
tracker: "#297"
title: RAC-only REALITY integration build
status: in-progress
---

# RAC-only REALITY integration build

This branch preserves the existing RAC candidate while adding the reviewed
parallel REALITY ingress. It is a sandbox source/build artifact, not a proposal to
merge the candidate-only onboarding features into main. Its draft pull request
must never be merged into main.

## Source and ownership

- Authorized repository and designated primary checkout:
  `/Users/romanzhigalkin/Web/razdacha`, branch `main`.
- Remote/base: `origin/main`, freshly fetched by the primary before checkout at
  `b46be7f2dff567fb72bd9d788d64bfc91eff7100`.
- Exclusive integration checkout:
  `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/epic-297-rac-reality-integration`.
- Integration branch: `epic/297-rac-reality-integration`.
- Exact recorded deployed RAC candidate source:
  `deb5924d2362beb301443f1afa042427c3312e8e`.
- Reviewed UI source: `eef576b1b3b814b27faf73f821b7f3a055d9c051`.
- Final backend source and fresh merged main are pending primary approval.

The installed RAC binary did not include VCS build metadata. Its recorded source
relationship is deployment provenance, not self-attestation from that binary.
Future artifacts must pin a clean integration commit, version and checksums.

## Required composition

Preserve the candidate's opt-in free-start onboarding, authenticated first-login
wizard, public fresh-install panel bootstrap and measured WireGuard aggregate
counters. Preserve main's runtime observations, UI lifecycle and safety invariants.
The new REALITY method defaults disabled and must not change existing WG keys or
outgoing tunnel definitions. Reality-only clients must not display invented WG
traffic measurements.

Compose shared setup, publication gate, API routes, peer UI and context changes
rather than selecting either side wholesale. Preserve both independently numbered
ADR 0032 files and distinguish their source lineages in the RAC decision index.
Do not rewrite historical ADRs, task records or logs.

## Boundaries and validation

No host access, deployment, existing tunnel operations, provider probes or server
state changes belong to this worktree task. The primary coordinates host actions
and restart clarification separately. Only local synthetic fixtures are allowed.

After the final approved backend and fresh merged main are included, run the Go
suite, vet, pinned lint, static Linux amd64/arm64 builds and all discovered browser
suites, including onboarding, aggregate traffic and REALITY coverage. Do not build
final sandbox artifacts until the primary approves the final tree/version. Record
conflict resolutions and final verification below before review.
