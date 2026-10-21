# ADR 0030: Initialize WireGuard chain parents through singleton aliases

Date: 2026-10-21
Status: accepted
Clarifies: [ADR 0012](0012-tunnel-chain-detour.md)

## Context

The pinned sing-box 1.12.25 runtime can deadlock during startup when a WireGuard
endpoint with a hostname peer uses another WireGuard endpoint as its direct detour.
Hostname resolution can initialize the parent while endpoint startup holds a lock
that parent initialization also needs. `sing-box check` validates the configuration
without exercising this startup path. The Clash API never becomes ready; shutdown
can also wait on the blocked startup.

A private isolated comparison reproduced the hostname failure. A diagnostic literal
address copy started/stopped promptly; singleton parent aliases also restored normal
startup/shutdown while preserving hostnames. This is pinned-runtime lifecycle evidence,
not a proof of end-to-end provider reachability.

## Decision

After materializing valid rule chain clones, inspect WireGuard endpoints. A child
with any nonempty hostname peer and a detour identifying a materialized WireGuard
parent uses a generated singleton selector as its detour. That selector contains
and defaults to exactly the original parent, with connection interruption false.
Keep original endpoint addresses/keys/resolvers, parent endpoints, chain clone tags,
and route/DNS/source policy unchanged. Literal children and other parent protocols
keep their original direct detours.

Alias tags use a reserved prefix and full SHA256 of the parent tag. Deduplicate per
parent in first-use order; reject collisions with all materialized endpoint/outbound
tags and built-in direct. Aliases are internal wiring, never user policy targets.
The pinned runtime accepts persisted selection only within current membership; stale
direct/deleted exits cannot escape a singleton selector.

Collect chain clones only after confirming destinations, usable sources and available
legs. Skipped/rejected rules leave no unused startup-blocking clones. Existing scoped
rejection and missing-tunnel errors remain; aliases cannot make unavailable legs usable.
Pool retry/pruning continues using actual member tags, never aliases.

This clarifies ADR 0012 rather than replacing its two-hop clone model. ADR 0012 rejected
a selector *instead of the child clone*, because selectors lack detour dialer options.
The clone still owns its detour here; the selector supplies startup ordering for a
fixed parent, initializing that dependency outside the endpoint startup lock.

## Consequences and alternatives

- One deterministic internal selector is added per eligible parent. No schema,
  runtime dependency, version or deployment change is required.
- Generator tests preserve peer/DNS/route policy and exercise collision/deduplication,
  disabled/missing/orphan/empty rules and pool pruning. Actual pinned-runtime cache
  tests reject stale alternate exits. Syntax checking alone cannot prove readiness.
- Replacing hostnames with resolved addresses was rejected because it changes provider
  behavior and loses DNS updates. Global DNS changes or external lifecycle patches
  would broaden the fix.
- Reevaluate the alias when upgrading sing-box. It creates no fallback and changes
  no tunnel-health policy.
