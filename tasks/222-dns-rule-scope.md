# 222. Keep CIDR conditions out of immediate DNS actions

Implement ADR 0024 for manual and daemon-managed plain sources. Preserve complete
route matching, source-peer scope, reject precedence, and existing native formats.
Issue #220 owns native-format projection and refresh coherence.

Verify CIDR-only, domain-only and mixed sources; unrelated and selected questions;
block, unavailable and live tunnels; real resolution; missing/empty sources; one
plain callback read per generation; and regeneration after refresh/restart.

The actual pinned runtime must fail the desired behavior on the original base and
pass with this fix. Independent reviews and a disposable Linux mount/network
namespace wire check are required before merge. Staging evidence remains pending
until those gates pass. Never access the source host or publish private values.

Primary checkout: recorded privately by the orchestrator; target branch `main`.
Remote: `origin` (`git@github.com:ArghTeam/razdacha.git`), target `main`.
Fresh fetched base: `a095f21e418c4ac077d15051647ecfd6ffb38a4c`.
Feature: `fix/222-dns-rule-scope` in its isolated authorized worktree.

## Local evidence

The exact unchanged base generator and rule-set builder were substituted only for
baseline compilation and restored immediately afterward. The desired-behavior
runtime suite failed 16 of 70 cases: manual/plain CIDR-only/mixed sources with
block, block with real resolution, unavailable tunnel, and unavailable tunnel with
real resolution returned REFUSED for permitted questions. Domain-only controls and
live-tunnel cases passed. The fixed pinned-runtime suite passes all 70 cases.

Full daemon-module tests and vet pass; Linux amd64 CGO-disabled daemon/CLI builds
pass. The nested actual-runtime race suite passes. Both baseline and fixed Linux
wire binaries compile. CI now runs the nested runtime suite with read-only module
resolution; privileged namespace wire execution remains opt-in.

Staging wire execution and both independent security review gates remain pending.
Missing/empty source skipping is pre-existing behavior, not last-good publication
protection; #214 owns that separate guarantee. Native source DNS semantics remain
unresolved in #220. No all-format or production-deployment claim is made.
