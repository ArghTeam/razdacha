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
- Reviewed UI source: `eef576b1b3b814b27faf73f821b7f3a055d9c051`, plus copy-only
  `e90db78bd1ffa210b81a569c2260bf131cf414ff`.
- Final approved backend source: `5ec522bdacf3151962516f68ee1371885257ed97`,
  including `c5bfa11fa577463392095476037f824e94303e8b` and import-only Linux lint
  correction `c850fb433a32df6a9da732faa6dbdfe3123e5544`.
- Final functional main source: freshly fetched
  `4eef6024cbc3e15ff22562b65c97b04c75d31133`, approved by the primary.
  Later test-only main changes are outside this artifact freeze.

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

## Rollback limitation

The earlier sandbox backup was checked for file integrity only. It is not a
functional boot or network rollback rehearsal. A deployment rollback must restore
the schema-12 daemon, runtime, generated configuration, SQLite state and committed
authority together; main's new schema 13/authority 5 must not be opened by the old
daemon. Existing WireGuard keys, client records and outgoing tunnel data must be
preserved. No rollback or host operation has been performed by this task.

## Composition record

The exact recorded RAC candidate merged with only an ADR-index conflict. Both
0032 files are retained and the RAC index labels their independent lineages.
The frozen UI merge required composing the peer inventory: retained candidate
unknown-aware WG aggregate counters and REALITY method/status/profile lifetimes,
with explicit WG-only aggregate semantics and mixed-transport regressions.

The backend merge retained both onboarding catalog ownership and the live Store
reference on the serialized publication gate. Its exact managed runtime command,
private config reads and conservative systemd filesystem-view preflight remain
unchanged. API routes and fresh-install bootstrap were composed automatically and
are checked by the complete suites.

Two RAC-specific adaptations keep onboarding coherent with the new backend:
transaction-local onboarding reads now share the complete Snapshot reader,
including non-secret Reality identity/admission authority; Configure/Finish also
refuse an independently guarded public ingress under their existing lock. Focused
regressions exercise enabled/retained identities and successful checked repair.

## Local verification progress

The complete Go suite, vet, pinned sing-box matcher module and pinned linter for
both Darwin and Linux passed on the composed source. Focused onboarding authority
and public-guard repair regressions passed. The first full browser run exposed a
strict rule-action fixture missing the candidate's authenticated onboarding-status
request after relogin; explicit `pending:false` responses now preserve that known
application contract in rule/tunnel fixtures. This changes no product behavior.
The complete 18-suite browser runner passed after that fixture correction, including
all 36 REALITY cases, candidate onboarding and mixed-transport WG aggregates.
Static Linux amd64 and arm64 daemon/CLI compile checks passed with CGO disabled;
verification binaries were discarded rather than versioned as deployment artifacts.
The final restore-only follow-up passed targeted store/CLI restore regressions;
unchanged browser suites were not repeated solely for that SQL change.
Final main synchronization, source review and artifact/version approval remain
separate gates. No live host, provider, tunnel or deployment proof is claimed.

## Legacy wizard fingerprint limitation

An already prepared/applied pending schema-12 wizard may retain an old raw Snapshot
fingerprint after the schema-13 model adds settings fields. This integration does
not automatically rewrite or bless that historical hash: doing so could accept
unrelated routing edits. Such candidate-lineage state needs explicit reconciliation
before claiming wizard completion. The primary's read-only RAC inventory reported
that the saved onboarding-state row is absent, so this generic lineage edge does
not affect the designated RAC deployment. That inventory involved no metadata
writes and is not deployment or functional rollback proof. Main has no candidate
onboarding metadata, so this limitation is outside the main feature scope.

## Final functional source freeze

The final functional main merge changes only the clarified handshake-target help
copy plus its UI task/log records. Candidate WG counters and both onboarding
adaptations are preserved. The ingress guard, managed unit/preflight implementation,
private config reader, REALITY compiler/client profile, publication controller and
credential/restore files match the approved functional main bytes. Syntax/embed
checks validate the copy-only merge; prior unchanged functional suites are not
repeated solely for that wording.

Versioned binaries and build evidence are written outside the worktree after the
final source commit. Their manifest records the full source SHA, linker version/
commit, static Linux target settings, checksums and Go build information. A local
metadata probe must distinguish linker/API values from potentially misleading
worktree VCS metadata; it is not a deployed Linux daemon or network test. No source
changes are permitted after the artifact version/hash freeze.
