# 0032. Parallel authenticated client ingress

Date: 2026-11-04 · Status: **proposed** · Tracker: [#297](https://github.com/ArghTeam/razdacha/issues/297)

## Context

An optional VLESS + REALITY TCP listener would complement standard WireGuard when
its UDP transport is blocked. This is a proposal, not implemented or deployed
behavior, and promises no particular DPI or IP-block resistance. It addresses the
client-to-gateway path; outgoing tunnels remain a separate choice.
No decision is superseded by this proposal. Acceptance would extend the incoming
transport boundary of ADR 0001 and the contracts in ADRs 0005, 0009, 0013, 0016,
0023, 0024, 0025, 0026, 0028 and 0029. Historical implementation and validation
records remain unchanged.

The pinned sing-box version is 1.12.25. Current policy uses WG peer addresses and
the persistent guard covers only `wg0`; both boundaries need extension.

## Proposed decision

### One client identity, independent credentials

Keep the existing Peer ID as the client identity and add an optional alternative
ingress credential attached to it. Standard WireGuard keys and configuration stay
valid. The new credential is a separately generated VLESS UUID; never derive it
from a WireGuard key, client name, public address or panel password.

Use a stable opaque authenticated user name to map a successfully authenticated
VLESS connection to that Peer ID. The runtime user name is not the bearer secret.
Duplicate credential identities are rejected. Global listener enablement and each
client's transport enablement are explicit and start disabled. A client may use
either or both enabled transports; automatic selection is outside this proposal.

Clients/routers need compatible software. A client TUN can capture traffic, but
the protocol supplies TCP/UDP flows. Arbitrary IP, ICMP, LAN VPN equivalence and
IPv6 are excluded; an unchanged WG client cannot speak the new protocol.

### Direct inbound and shared policy semantics

Terminate VLESS in sing-box; do not inject it into `wg0`, create synthetic peers,
or trust public source addresses as identity.

Introduce one source-selector abstraction used by route rules, DNS rules and
whole-Internet client exits. A selected client matches either:

1. A trusted existing WireGuard interception inbound and its committed peer address.
2. The dedicated VLESS inbound **and** that client's authenticated opaque user name.

OR the branches, then AND with destination/transport predicates; a default rule
containing both source-IP and auth-user fields does not express this OR. Deny
unknown, missing, disabled or revoked users before ordinary policy/`route.final`.
Same-NAT users stay distinct; shared rules cannot capture infrastructure traffic.

Preserve rule ordering, unavailable-exit rejection, selected orphan handling and
whole-Internet exit precedence. The new ingress must use original native rule sets
and exact DNS projections under ADRs 0024/0025/0029; it does not widen the accepted
native predicate language or change the existing nft projection for WG traffic.

### DNS and destination boundaries

Client profiles capture DNS through the authenticated connection and use the gateway
resolver. DNS hijack and subsequent resolution must preserve authenticated identity
for both TCP and UDP; this is a required pinned-runtime experiment. Whole-Internet
exits keep their same-exit DNS and bootstrap behavior, including stale FakeIP handling.
Client-side encrypted DNS has the same documented inspection limits as existing
policy; do not claim to identify every DoH request.

Kernel restrictions attached to `wg0` do not protect a direct proxy inbound. Add
VLESS-scoped equivalents for IPv6 rejection and existing DNS safety restrictions,
including the destination-port 853 policy. Explicitly reject private, loopback,
link-local and other protected destinations before ordinary direct fallback, both
for IP-literal destinations and after hostname resolution. The gateway DNS service
is an explicit narrow exception. The listener must not grant access to gateway
administration, the runtime controller or other host services. Alternative ingress
does not implicitly inherit WG access to the private panel.

### Guard every listener throughout publication

Extend ADR 0023 with a persistent kernel guard covering both `wg0` and the public
alternative listener endpoints. Guard the union of old and candidate listen
addresses/ports before starting or changing any runtime, including already
established TCP connections. Install it before ordinary established-flow accepts.
Block both address families where the listener can bind. Remove obsolete endpoint
protection only after successful publication proves it safe.

Preserve prepare/check before mutation, serialized publication, durable authority,
bootstrap invalidation and release-last ordering. Every failure after guarding
keeps both client paths closed; retry cannot treat matching configuration bytes as
proof of a running authorized process. A listener-only repair or credential change
must not take the existing no-restart fast path when runtime authentication differs.

Boot and independent `sing-box.service` starts must establish the guard before
loading a saved public listener. This requires packaging integration for both the
generated service and a drop-in for a distribution-provided unit, without replacing
unowned package files. Avoid a dependency cycle with the daemon's runtime restart.
An authoritative endpoint manifest and its failure behavior must be proven before
enablement: missing or corrupt authority cannot permit a stale listener to start.
Reset, uninstall and recovery must handle both guard branches deliberately.

### Live revocation and recovery

Policy generations are not authentication restore sources. Build effective runtime
users from the intersection of committed transport admission and current live
credential/client state. Newly saved credentials cannot become active through a
background refresh before explicit Apply. Disabled/deleted clients and rotated or
revoked credentials cannot be recreated from old committed or bootstrap records.

Revocation must stop active TCP and UDP proxy sessions as well as new handshakes.
The first implementation may use a guarded full runtime restart; document its
effect on other clients and verify it before claiming successful revocation.
Serialize authentication reconciliation with Apply, refresh, restore and recovery;
an uncertain restart or failed reconciliation keeps the affected ingress guarded.
Define the save-to-reconciliation timing and UI status explicitly rather than
claiming immediate revocation at database commit.

Allocate a new network-authority envelope version for transport identity and
listener authority. Older binaries must reject the new semantics. Existing older
records keep their original verification contract and recover WG-only behavior;
they cannot authorize a new listener. A downgrade needs a documented migration or
rollback path and must not silently ignore alternative credentials.

### Listener, credentials and delivery

Use a dedicated configurable TCP endpoint. Public nginx already owns TCP 443 on
many installations; do not assume it is free, move the panel implicitly, or add
unreviewed TLS multiplexing. Validate address/port ownership and collision with
panel, DNS and controller sockets before publication. Select the concrete staging
port only after inventory; shared-port operation is a separate future decision.

Generate REALITY server keys with supported cryptographic primitives. Keep private
keys and client UUIDs owner-only, out of ordinary APIs, logs, diagnostics and public
evidence. Full backups include the new secrets under ADR 0016; restore retains the
existing requirement for explicit policy Apply and must not reactivate cloned
access. Profile disclosure uses authenticated dedicated endpoints and the existing
modal/session cleanup rules. Validate exported profiles on selected client/router
implementations and pin the tested compatibility set.

## Alternatives and consequences

AmneziaWG retains a VPN model close to WG but still requires UDP and a compatible
client plus a separate server integration. Hysteria2 also relies on UDP. Both can
be evaluated later; neither substitutes for the TCP alternative requested here.
Reverse tunnels solve reachability and connection-initiation problems and are not
required for this ingress design.

Implementation spans store, generator, netstack, authority, lifecycle, packaging,
API and panel. The [task specification](../../tasks/297-alternative-ingress.md)
requires runtime proof, independent review and staging validation before enablement.
This proposal grants no merge, release or production access.
