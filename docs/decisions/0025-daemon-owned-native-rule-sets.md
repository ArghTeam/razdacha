# 0025. Daemon-owned immutable native rule sets

Date: 2026-10-20 · Status: proposed; extends ADR 0023 and ADR 0024

## Context

Autonomous sing-box remote refresh can select a different body from the daemon's
nft snapshot. Either skew direction exposes selected traffic to direct routing.
Native CIDR predicates also broaden preliminary DNS reject/FakeIP matching because
the runtime has no response address yet. Lossy domain/CIDR flattening is incorrect.

## Decision

The daemon owns native acquisition, validation, accepted-cache authority and immutable
content-addressed artifacts. A complete accepted manifest atomically joins body hash,
format, HTTP validators and projections. Every generation uses local original native
blobs for routing and a separate exact domain-only blob for DNS; nft derives from the
same accepted AST. No sing-box remote rule sets or mutable active paths are emitted.
There is no garbage collection until reachability across active, prepared and recovery
records can be proved. Serialized aggregate storage and file-count quotas reject new
artifacts before writes, preserving existing references on exhaustion; compressed,
expanded, AST and projection budgets also bound each candidate. Community SRS and companion subnets are independently versioned
upstream; the guard creates a coherent local pair, not upstream atomicity.

Admission is explicit: separable domain expression OR address expression, preserving
native boolean semantics. Default domain and destination CIDR conditions share an OR
group in v1.12.25. Pure domain expressions retain exact/suffix/keyword/regex, logical
AND/OR and inversion; pure address expressions use exact IPv4 algebra. Mixed logical
AND/inversion without proven factorization, unknown/context-dependent predicates,
unsupported versions and excessive complexity fail before acceptance. Empty matchers
are never used as accidental DNS true constants. If the full expression matches all
literal-IP traffic, reject it rather than silently enabling gateway-wide interception.
Existing DNS/local-address exemptions remain the enforcement boundary.

Address projection uses one normalization per OR node and sorted linear range
intersections for AND. Each compilation shares a 4-Mi-unit address-work budget:
range visits cost one unit, normalization reserves `N * (bit_length(N) + 1)` units
before the operation, and leaf sets, complements, nested/intermediate unions and
intersections all charge the same counter. This bounds intermediate growth rather
than relying on raw input counts alone. Work exhaustion explicitly rejects the
candidate before cache/validator promotion and preserves healthy last-good authority.
Regex admission separately caps each source at 1,024 patterns and 32 KiB aggregate
raw regex bytes (4,096 bytes per pattern). Before program expansion, a compact Go
syntax AST estimate reserves both runtime copies (original and DNS): at most 64 Ki
AST/rune units, 64 Ki expanded instructions including program bookends, and 128 Ki
expanded rune entries. Saturating count/repetition arithmetic rejects excessive
programs before Simplify/Compile; Unicode class tables and cheap-pattern overhead
also count. These are conservative compatibility limits, not matcher rewrites.
Database paths and native artifact roots are canonical absolute paths; relative
`-db` values select the same source store during Apply and committed recovery.


Generation v2 verifies original and DNS artifact permissions, format, hash and projection
version and rederives projections before publication/recovery. Legacy v1 native records
remain guarded until explicit administrator recovery; never reopen an autonomous remote
config or drop native rules. Fresh bootstrap cannot depend on unloaded native sources.
Bad downloads never advance accepted bodies or validators. 304 requires verified accepted
content; missing/corrupt content permits only a bounded unconditional retry.

## Consequences

Some previously accepted native files now produce explicit unsupported-predicate errors.
This is intentional compatibility loss rather than false coherent success. Existing
last-good accepted data remains active on refresh failure. Upgrade from legacy native
authority requires out-of-band administrator access, validated acquisition and explicit
Apply. Domain-only regex may match every nonempty name while still not matching literal
IP traffic; runtime tests establish this distinction. Unbounded literal-IP predicates
require a separate capacity/admin decision. No universal egress or DoH guarantee is made.
