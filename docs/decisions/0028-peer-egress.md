# 0028. Source-scoped whole-Internet tunnel routing

Date: 2026-10-20 · Status: accepted; extends ADR 0003, 0013 and 0023

## Context

Destination rules cannot express a client-wide exit without broadening interception,
leaking DNS or allowing unsupported traffic directly. The pinned sing-box 1.12.25
TProxy/userspace WireGuard path supports TCP/UDP, not arbitrary IP packets.

## Decision

A peer optionally references one outgoing tunnel through `egress_tunnel_id`.
Null preserves shared rules. Source policies precede ordinary rules and resolve
stale FakeIP destinations through a dedicated same-exit DNS transport. Gateway DNS
uses that transport before ordinary rules; HTTPS-query/application-DNS guards remain.
A hostname upstream has a separate same-exit bootstrap transport. Tunnel establishment
metadata resolution remains infrastructure traffic, not client destination DNS.

Absent, disabled or empty targets reject peer routing and DNS. Stored health never
authorizes direct fallback. Disabling preserves selection; deleting a referenced
tunnel is refused transactionally.

The nft source set contains exact IPv4 peer addresses from committed policy, never
a global destination catch-all. DNS and local gateway administration retain existing
exemptions. Only TCP/UDP Internet traffic is intercepted; private and residual
forwarding is rejected, including unsupported protocols and prior direct connections.
Shared-rule clients retain their behavior. IPv6 stays disabled. This is a client
Internet exit, not an arbitrary IP or LAN VPN.

All publication uses ADR 0023. New committed/bootstrap records use version 3 so older
binaries reject unknown semantics. Old versions without peer routing remain readable;
routing fields in older envelopes are refused. Refresh/recovery use committed choices,
never pending edits or restored live identities. Deleted addresses stay reserved until Apply.

## Consequences

The client editor exposes shared rules or a chosen tunnel with protocol help.
Changes require Apply and remain visible as pending after panel reload. Configured DNS
must be supported by the target; failure has no direct fallback. Tests cover isolation,
unavailable exits, DNS/stale FakeIP, references and recovery. No runtime upgrade or
kernel outgoing WireGuard interface is introduced.

## Validation boundary

Source `3f971684f3b65c9e338589d38e56535dc27df2e0` passed the bounded isolated
Linux nft/TProxy matrix with two source identities, DNS and cached FakeIP,
unavailable exits, unsupported protocol rejection and independent sink capture.
The first attempt failed because the synthetic SOCKS relay rejected the pinned
client's UDP control bind address; its correction and successful fresh attempt
are retained in the [completed task](../../tasks/done/231-peer-egress.md).
Independent synthetic-browser verification passed 11/11 checks.

The live fixture uses veth identities and a synthetic SOCKS exit, not incoming
encrypted WireGuard or an actual DO exit/client flow. Observed zero-drop capture
barriers apply to finite fixture windows, not universal traffic or lossless
capture. Acceptance of this decision does not grant merge, release or deployment.
