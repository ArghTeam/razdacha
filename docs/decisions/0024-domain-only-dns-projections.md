# 0024. Domain-only DNS projections for manual and plain rules

Date: 2026-10-19 · Status: accepted; clarifies ADR 0013

## Context

sing-box v1.12.25 ignores destination CIDR conditions during preliminary DNS
matching. An IP-only entry in a shared OR rule set can therefore match an unrelated
question before immediate reject or FakeIP actions execute. A mixed domain/CIDR
set is unsafe for those actions even though it is correct for destination routing.

## Decision

Manual and daemon-managed plain sources retain their complete domain/CIDR route
sets. Each also receives a separate `-dns` inline set containing only its domain
suffix entries. Both projections use the same single source read. DNS actions
reference only DNS tags; a source without domains contributes no DNS tag. An empty
DNS tag list omits the DNS rule, including for peer-scoped policies, rather than
creating an unrestricted match. The full route rule and nft CIDR protection remain.

Block and unavailable-tunnel actions reject matching domains regardless of
`resolve_real_ip`. Live tunnels use FakeIP unless real resolution is requested.
Direct rules need no DNS action. Source-peer constraints remain identical in both
layers. The global IPv4, HTTPS-query and application-DNS guards remain unchanged.

This refines ADR 0013: domain protection belongs in DNS, while literal CIDR
protection belongs in route rules and nft steering. A CIDR-only policy does not
justify rejecting unrelated DNS questions.

Native community, SRS and JSON rule sets retain their complete predicates and
existing tags. Their DNS projection and coordinated refresh are unresolved in
issue #220; this decision does not claim to fix those formats. Flattening native
logical, inverted or regular-expression predicates is not permitted.

## Consequences

The configuration contains additional inline sets for known domains. Existing
route tags remain stable. Diagnostics cannot infer that a CIDR-only route failed
to match merely because a DNS answer contains a real address. Runtime regression
checks must exercise the pinned sing-box matcher and controlled DNS exchange in
addition to inspecting generated JSON.
