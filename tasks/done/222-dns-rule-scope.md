# 222. Keep CIDR conditions out of immediate DNS actions

Implement ADR 0024 for manual and daemon-managed plain sources. Preserve complete
route matching, source-peer scope, reject precedence, and existing native formats.
Issue #220 owns native-format projection and refresh coherence.

Verify CIDR-only, domain-only and mixed sources; unrelated and selected questions;
block, unavailable and live tunnels; real resolution; missing/empty sources; one
plain callback read per generation; and regeneration after refresh/restart.

The actual pinned runtime must fail the desired behavior on the original base and
pass with this fix. Independent reviews and a disposable Linux mount/network
namespace wire check are required before merge. The completed wire evidence is
recorded below. Never access the source host or publish private values.

Primary checkout: recorded privately by the orchestrator; target branch `main`.
Remote: `origin` (`git@github.com:ArghTeam/razdacha.git`), target `main`.
Fresh fetched base: `a095f21e418c4ac077d15051647ecfd6ffb38a4c`.
Feature: `fix/222-dns-rule-scope` in its isolated authorized worktree.

## Local evidence

The original-base runtime regression used unchanged generator and rule-set source.
Final baseline artifacts used a complete archive of the original base plus the
exact frozen nested test module. The desired-behavior
runtime suite failed 16 of 70 cases: manual/plain CIDR-only/mixed sources with
block, block with real resolution, unavailable tunnel, and unavailable tunnel with
real resolution returned REFUSED for permitted questions. Domain-only controls and
live-tunnel cases passed. The fixed pinned-runtime suite passes all 70 cases.

Full daemon-module tests and vet pass; Linux amd64 CGO-disabled daemon/CLI builds
pass. The nested actual-runtime race suite passes. Both baseline and fixed Linux
wire binaries compile. CI now runs the nested runtime suite with read-only module
resolution; privileged namespace wire execution remains opt-in.

Separate independent pre-run and final review gates remain mandatory before merge.
Missing/empty source skipping is pre-existing behavior, not last-good publication
protection; #214 owns that separate guarantee. Native source DNS semantics remain
unresolved in #220. No all-format or production-deployment claim is made.

## Staging attempt 1: incomplete evidence

The first isolated run used revision `bc013e7`. Its first baseline case reached
the expected unrelated-DNS REFUSED defect, but a fixture helper reported failure
through the parent test instead of the current subtest. This terminated the
baseline matrix: one baseline case failed, seven baseline cases were not run, and
the fixed binary was not run. The attempt is failed and supplies no successful
wire acceptance evidence. Process cleanup passed and host services were unchanged.

Probe and runtime-start helpers now accept the current `*testing.T` explicitly at
every call site. This preserves per-case failure attribution and continuation
without weakening desired behavior. Attempt 2 used the reviewed replacement fixture,
rebuilt binaries and a repinned supervisor.

## Staging attempt 2: completed bounded regression

The isolated run tested source `2c180179cba9e2fbbe1dcf353f42c115a5badb79` against
baseline `a095f21e418c4ac077d15051647ecfd6ffb38a4c`, both with the same reviewed
fixture. The pinned sing-box runtime was v1.12.25. The baseline and fixed binaries
were built with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -mod=readonly -c`,
using local cached dependencies and toolchain with network module resolution off.

| Wire case | Original base | Fixed source |
|---|---|---|
| manual-cidr-block | Expected FAIL: permitted DNS returned REFUSED (5) | PASS |
| manual-cidr-unavailable | Expected FAIL: permitted DNS returned REFUSED (5) | PASS |
| manual-mixed-block | Expected FAIL: permitted DNS returned REFUSED (5) | PASS |
| manual-mixed-unavailable | Expected FAIL: permitted DNS returned REFUSED (5) | PASS |
| plain-cidr-block | Expected FAIL: permitted DNS returned REFUSED (5) | PASS |
| plain-cidr-unavailable | Expected FAIL: permitted DNS returned REFUSED (5) | PASS |
| plain-mixed-block | Expected FAIL: permitted DNS returned REFUSED (5) | PASS |
| plain-mixed-unavailable | Expected FAIL: permitted DNS returned REFUSED (5) | PASS |

All eight original-base cases reached the intended DNS refusal assertion. All eight
fixed cases and the fixed top-level test passed. Controlled DNS over TCP/UDP
succeeded; matching domains rejected; selected CIDR TCP/UDP probes were not observed
at the sink; permitted traffic, prior checked config/nft restoration, failed-check
continuity and restart checks passed. No real tunnel credentials were used.

The raw collector acknowledged a fresh UDP nonce after the probes, and the gateway
waited for the output scanner to consume that marker before inspecting queued
reports. This is finite, scoped evidence that forbidden packets were not observed
in these probes. The fixture does not inspect `PACKET_STATISTICS`; it provides no
zero-drop, lossless collection or universal no-leak guarantee. Synthetic FakeIP
transport tests establish action selection, not actual persistent FakeIP allocation.
The result does not validate #214 publication ordering or unresolved native formats
in #220.

The supervisor and wrapper succeeded with zero wrapper failures. Host services and
pre-test inventories remained unchanged. No fixture process group or executable
remained, and the four uploaded fixture payloads were removed. No product binary
was installed; no production/source-host access occurred.

## Independent review

The pre-run threat/failure reviewer approved only the exact pinned disposable
fixture and supervisor scope. A distinct final reviewer independently checked the
source, all eight baseline refusals and fixed passes, failed attempt preservation,
recovery, cleanup and finite observation limits. Final documentation changes and
current CI remain part of the same PR's merge gate.

### Evidence identities

Connection values, host paths and raw output remain private. SHA256 identities:

| Artifact | SHA256 |
|---|---|
| Shared wire fixture | `851a0e2a04e490088d4c2528286ff923804e4f4959bf6571e535ad5b13b34bb9` |
| Baseline binary | `e36579905334a07b376d17d583b85116262b7256d6e47b8ae9e5ba6c739cc06b` |
| Fixed binary | `ecce9606b1abc86fd9e72ac88fbe4d75bbcefec3a8116ff0f426b2d461f564eb` |
| Pinned sing-box binary | `d14cf69a7dd1d49e88a1f033ded3213a8f3503c57a8561ed6f166f3b388f4c41` |
| Original-base raw log | `b8bf89192b4f98ad4d2ba171587dbd663581f03176bb8f30444b0a2dfb7e660a` |
| Fixed raw log | `17e20f94c1cc0628fbac7e147b9a0b5bc16c25cc7f7e944623a8e4a4fc8682e0` |
