# 0037. Recover visible TCP names for REALITY destination policy

Date: 2026-11-05 · Status: **accepted** · Tracker: [#313](https://github.com/ArghTeam/razdacha/issues/313)

## Context

An Android VLESS client may resolve a site itself and send only a numeric destination.
The gateway then cannot match a domain rule even though the TLS ClientHello still
contains a visible server name. A domain-bearing SOCKS request did not reproduce
this case. This decision extends ADR 0032's destination inspection contract; it
supersedes no decision and leaves ADRs 0024 and 0028 unchanged.

## Decision

For authenticated REALITY IPv4 TCP connections reaching shared destination rules,
use the pinned sing-box 1.12.25 route sniff action with only the TLS and HTTP
sniffers and a 300 ms timeout. Run it after authentication, IPv6, DNS-port,
private/admin destination and explicit resolution guards, and after all terminal
whole-client exit rules. Repeat the gateway hostname rejection after sniffing.

Sniffing supplies matching metadata only. Never enable the deprecated destination
override, resolve the recovered name, or replace the original numeric destination.
Explicit hostname and gateway FakeIP destinations retain their existing resolution
path and do not match the numeric IPv4 sniff condition. WireGuard interception is
outside this action's inbound scope. No destination sets means no sniff action.
Selected-client identities, destination predicates and unavailable-exit rejection
remain the existing compiler's responsibility.

The pinned TCP implementation uses a fixed read deadline and a bounded sniff
buffer. An absent, opaque, oversized or incomplete name retains existing fallback
behavior. The server cannot recover hidden names from arbitrary encrypted DNS or
ECH. Do not enable QUIC sniffing here: the pinned UDP sniff loop can request more
packets with a renewed per-packet deadline, so the timeout alone is not a total
resource bound. This decision makes no QUIC name-recovery claim.

## Client and verification boundary

Supported selective routing still requires gateway DNS through the authenticated
connection at `10.8.0.1:53`. The complete exported sing-box profile supplies that
route; a VLESS URI cannot enforce an application's DNS configuration. Clients that
import only JSON outbounds may also discard its DNS/TUN rules. Verify the actual
Android application's DNS and import mode separately. Gateway FakeIP preserves
names for supported TCP/UDP requests; visible TCP sniffing is a fallback, not a
replacement for that contract.

Regression checks must cover numeric IP plus visible TLS/HTTP names, original IP
preservation, selected-client isolation, private/admin guards and whole-client exit
precedence using the pinned engine where practical. A local synthetic inbound
adaptation does not establish Android TUN, QUIC or operating-system DNS behavior.
