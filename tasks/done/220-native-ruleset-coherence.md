# 220 — Daemon-owned native rule-set generations

## Goal

Close both autonomous-runtime/daemon version-skew directions and native preliminary
DNS broadening. Preserve ADR 0023's publication gate, authority and peer lifecycle.

## Contract

The daemon acquires every community SRS and custom native SRS/JSON source. Before
accepting a candidate it validates bounded syntax and the complete native AST.
Original bytes remain unchanged in immutable owner-only content-addressed blobs.
A single atomic accepted-source manifest pins original bytes, format, validators,
projection version, exact DNS projection and IPv4 interception projection. Runtime
configuration uses only local immutable paths. No autonomous remote set remains.
Do not overwrite or collect blobs: active, prepared and recovery generations retain
all references, including rollback history. A serialized aggregate storage admission
budget bounds disk use; quota exhaustion rejects new candidates without deleting or
repairing referenced files. Compressed, expanded, AST and projection budgets apply.
Community companion subnet lists and SRS are independent upstream artifacts, paired
only in a local publication.

## Semantic admission

Pinned sing-box v1.12.25 default destination domain and CIDR groups are ORed. Accept
provably separable `D(domain) OR A(destination address)` only. Preserve domain exact,
suffix, keyword, regex, logical AND/OR and inversion within pure domain expressions;
preserve address expressions with exact IPv4 set algebra. Reject genuine mixed
AND and mixed inversion unless exact simplification proves separation. Reject
unknown fields/versions, source/process/network/port predicates, empty conditions,
empty logical nodes and resource-budget violations. Never flatten native predicates.

DNS references only D. False D emits no DNS tag/action. Literal-IP coverage is the
address projection plus domain expressions evaluated with no domain. An expression
matching all literal IPv4 traffic is rejected before acceptance; no implicit catch-all
workload is enabled. Existing local-address and DNS-port nft exemptions still apply.
An unsupported candidate reports an error and preserves last-good content/validators.

## Recovery and upgrade

Generation v2 pins native original hash/format and DNS hash/projection version.
Verify all artifacts and rederive projections before prepare, guard release or no-op.
A v1 generation with native policy has no trusted content authority and remains
guarded with an explicit administrator recovery error. No fallback to autonomous
remote config, editable DB policy or dropped rules is allowed. An administrator
must make supported sources available and explicitly Apply the intended policy.
Fresh bootstrap has no native sources; missing native content never releases ingress.

## Acceptance and evidence

- Real pinned runtime tests compare original JSON/SRS matching and exact projection;
  cover same-entry domain+CIDR OR, true logical AND, nesting, inversion, regex,
  constants, unsupported scope and literal-IP behavior.
- Local HTTP tests cover invalid/truncated/oversized responses, validators/304,
  corrupt/missing blobs, changed sources and atomic manifest interruption.
- Gate tests cover both skew directions, refresh/Apply/pool serialization, pending
  sources, last-good, no-op repair, restart and guarded failures.
- Full Go tests/race/vet, nested DNS-runtime tests and static Linux amd64/arm64
  daemon/CLI builds; CI must pass.
- Opt-in Linux net+mount namespace fixture uses synthetic local sources/direct sink,
  private tmpfs state, exact process supervision, AF_PACKET nonce ACK barriers and
  zero-drop evidence. Baseline failure markers require positive prerequisites.
- Two distinct independent reviews; root alone coordinates isolated staging and merge.

## Implementation limits and validation status

Native resource budgets are documented in the maintained lists invariants. No-GC
history is capped at 256 MiB/4,096 files; quota failure does not advance validators.
Original JSON and canonical SRS v1/v2/v3 are supported within the explicit predicate
whitelist. Context predicates and true mixed domain/address expressions are refused.
Active artifact failures are audited independently from pending selection and close
client ingress before any attempted candidate validation. New invalid input alone
retains healthy last-good service. Full Go/race/vet, nested runtime/race and static
Linux amd64/arm64 daemon/CLI checks passed on initial frozen source `81d7858`.
The subsequent relative-path/resource changes at `d5cb9c1` passed affected package
and native race tests, pinned runtime tests and lint. CI run `39339932615` passed
all five jobs on `d5cb9c1`. Independent source, artifact-provenance and actual
eight-case evidence reviews passed. The final documentation and current CI remain
merge gates; this record grants no merge, release or deployment authority.

## Review corrections before staging

Canonicalize relative database and native-store roots for Apply, no-op active audit
and fresh committed recovery; path-resolution errors propagate. The regression opens
an actual relative database, applies its native policy, recovers through a fresh gate
and checks that later active-file failure still closes ingress.

Replace cumulative sibling address unions with one normalization per OR node. Pure
AND uses size-ordered linear range intersections. A shared 4-Mi-unit address-work
budget charges range visits and `N * (bit_length(N) + 1)` normalization reservations
before leaf/complement/intermediate work. Exhaustion rejects before source/cache
promotion, leaving last-good body and validators unchanged. A deterministic adversarial
4,000-node/80,000-CIDR regression checks flat and nested OR equivalence; a separate
inverted-AND case checks intermediate-work rejection and last-good retention. Timing
assertions use a generous 10-second ceiling to detect the former quadratic path;
shared deterministic operation admission is the primary bound, not a wall-clock promise.

Regex admission now estimates compact Go syntax trees before program expansion.
Per source, cap 1,024 patterns/32 KiB aggregate raw regex bytes and reserve both
original/DNS runtime copies against 64 Ki AST/rune units, 64 Ki expanded instructions
including program bookends and 128 Ki expanded rune entries. Saturating arithmetic
handles repeated programs and Unicode classes without allocating an oversized
program to measure it. Tiny estimator-versus-actual tests cover zero repeats,
empty alternatives, nullable stars, captures, foldcase and Unicode; duplicate-pattern
and cross-leaf JSON/SRS budget tests preserve accepted bodies and HTTP validators.


## Accepted controlled wire evidence

Tested source: `d5cb9c1a2ba6531127801ce79494c321ac65533a`.
The baseline uses prerequisite `d5b8ad3d939a2a1faa3548b70fc8090782d59e4b`
with exactly four reviewed fixture overlays. Independent provenance verified all
414 unchanged original tracked files, both role-specific source inventories and
both compiled Linux binaries. The installed fixture runtime was sing-box v1.12.25.

Canonical B4 completed all eight cases in the required order:

| Format | Controlled skew | Fixed | Original baseline |
|---|---|---|---|
| JSON | Daemon new, runtime old | Go PASS; zero autonomous requests | Expected Go FAIL after positive TCP/UDP leak proof |
| JSON | Runtime new, daemon old | Go PASS; zero autonomous requests | Expected Go FAIL after positive TCP/UDP leak proof |
| SRS | Daemon new, runtime old | Go PASS; zero autonomous requests | Expected Go FAIL after positive TCP/UDP leak proof |
| SRS | Runtime new, daemon old | Go PASS; zero autonomous requests | Expected Go FAIL after positive TCP/UDP leak proof |

Each case produced exactly two same-receiver nonce ACK barriers with zero reported
packet drops: 16 barriers total. Fixed success followed exact accepted-generation
protection, unrelated DNS success, selected-domain REFUSED and committed recovery.
Each baseline proved positive initial controls, the served body versions and actual
nft membership before both forbidden TCP and UDP echoes succeeded; the final raw
barrier preceded its explicit desired-leak marker. Generic failures were never
accepted as baseline evidence. Both nested and outer Go summaries were checked.
The supervisor exited zero, with no supervisor/wrapper/collector errors.

All 11 downloaded files matched their independently checked remote size/SHA inventory.
The private evidence manifest SHA-256 is
`5c312d8c45ae6dcafef05ab730c90d74d57618db35245fedfcac832bb2d58828`.
The exact case-log SHA-256 values are:

| Case | SHA-256 |
|---|---|
| Fixed JSON, daemon new | `e9d2b2095a3a3fcd19c42092f8597a3dcd6555ebaf4f6f4445596e32f741ad28` |
| Fixed JSON, runtime new | `595c3bf38037ed12f24769105fbbdf03f15585d011609fc22a6809882a1b3d4c` |
| Fixed SRS, daemon new | `b919883cdf1183bd7e2ecf4c4c9417ec9d4313ec4531820af1adc15e02461be3` |
| Fixed SRS, runtime new | `450903215c71b1b2a4e944949671b78aed2edde67d26409a61b05c4fe169cc31` |
| Baseline JSON, daemon new | `0e469df9261d582cdd57daf16de335883619713d101adda66a8ced14d49d8963` |
| Baseline JSON, runtime new | `d2ac4808f259695febc4c4b16b5b19a15ea8f6cf6bb82ff782062cbf5de81177` |
| Baseline SRS, daemon new | `ed2ad8da6e14e2a38c9a0ca11d8d54cc3d226d2f118e6a9ed32d8593e2039a2e` |
| Baseline SRS, runtime new | `f8ac95d56d2756cdcde7756a13353e894bde66f279156653d3e41afa9d4a2ca7` |

Snapshot-to-BEFORE differed only in the previously reviewed health timestamps:
seven `checked_at` cells and three `ok_at` cells. BEFORE-to-AFTER full database
semantics and policy semantics were exactly equal, with zero timestamp changes.
Policy identity, host/file inventories, installed source files and services were
stable. All four pinned uploaded payloads were removed; no owned processes/groups
or private case-directory processes remained. Private result records were retained.
Neither full snapshot byte identity nor generic database stability is inferred from
a comparison which deliberately distinguishes health timestamps.

## Earlier attempts and observation limits

The earlier native wire attempt stopped after its first fixed case returned Go PASS:
the private supervisor rejected the nested/outer Go summary envelope, and seven
cases were not run. It remains uncertified. Prelaunch state-check stops ran no
fixture cases; observed pool/catalog drift required a fresh current snapshot rather
than weakening the policy/identity gate. Only canonical B4 supplies the accepted
complete matrix above.

These tests exercise actual nft/route/sing-box behavior and disposable veth address
control flow. They do not test an encrypted WireGuard handshake. Zero-drop nonce
barriers bound finite queued observations, not universal lossless capture or future
traffic protection. Wire cases use source-peer scope all; selected-peer behavior is
covered by local pinned-runtime tests. Unsupported native predicates remain explicit
admission failures, not silently rewritten matchers. No active application deployment,
provider-credential activation, production change or release occurred.

The author, threat/failure reviewer and final implementation/evidence reviewer were
distinct. The final reviewer independently accepted the actual eight-case evidence
and transfer/cleanup assertions. Review of this documentation and current CI remains
required before the primary agent may exercise separately granted merge authority.
