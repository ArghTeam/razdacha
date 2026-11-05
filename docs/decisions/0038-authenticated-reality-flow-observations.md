# 0038. Authenticated REALITY flow observations

Date: 2026-11-05 · Status: **accepted** · Tracker: [#312](https://github.com/ArghTeam/razdacha/issues/312)

## Context

WireGuard handshake and byte counters do not describe REALITY. The official pinned
sing-box 1.12.25 runtime omits authenticated users from Clash connection metadata,
but includes the matched route rule. It lacks the optional V2Ray statistics API.
Enabling information logging to infer activity would retain destinations in journal.
This extends ADR 0032's observation boundary and supersedes no earlier decision.

## Decision

Keep the official runtime and configured logging. Expand positive REALITY terminal
route alternatives to singleton authenticated user rules, and add an authenticated
direct fallback per admitted user. Preserve predicates, order, whole-client routing,
all guards and the original WG branches.

Count currently open flows through bounded local Clash `/connections` snapshots.
Identity requires the pinned singleton matched-rule prefix, `vless/reality-in`,
and current live admission. Public source addresses, domains, arbitrary substrings
and multi-user rules are never identity. Return only counts, state and observation
time to the panel, without destinations, addresses or byte counters.

Coalesce reads for five seconds with one active two-second controller read, a body
limit and a flow-count limit. Invalid, excessive, stale or unsupported observations
remain unavailable. Bind samples to publication epoch, checked configuration and
live credential revisions; verify authority before and after polling. Apply and
revocation advance the epoch even if configuration bytes later match. Pending,
disabled and guarded admission cannot receive positive observations.

Label positive counts **open flows**, separately from WG. Zero means no open flows
were observed, not offline. Polling can miss short connections, and idle open flows
do not prove recent traffic. No REALITY traffic volume is claimed.

## Consequences

Singleton terminal rules increase runtime rule count to O(clients × destination
rules); validate a representative 100-client, 100-rule configuration. Auth-list
content was already of that order, but matcher overhead is additional. No custom
runtime, public listener or continuous log stream is introduced.

URI import requires TUN capture and gateway DNS through the connection. Complete
sing-box JSON contains that setup, but outbound-only importers can discard it.
Domain recovery for QUIC and encrypted TLS names relies on the supported DNS path;
a successful profile import alone does not establish DNS capture.
