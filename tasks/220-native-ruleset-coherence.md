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
repairing referenced files. Compressed, expanded, AST and projection budgets apply. Community companion subnet lists and
SRS are independent upstream artifacts, paired only in a local publication.

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
retains healthy last-good service. Local cache/gate/runtime regression checks pass;
full validation, CI, exact independent review and isolated staging are pending.


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
