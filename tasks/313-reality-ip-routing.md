# #313 — REALITY numeric-IP destination routing

## Goal

Recover visible TCP TLS/HTTP names when an authenticated REALITY client sends only
an IPv4 destination, preserving the actual destination, source identity and safety
guards. See ADR 0037. The designated primary checkout is
`/Users/romanzhigalkin/Web/razdacha`, remote `origin`, target `main`; implementation
started from freshly fetched `93bf487cd9242993bfa5f216edaba6b30fdbfa66` in
`.codex/worktrees/fix-313-reality-ip-routing`.

## Acceptance

- Numeric IPv4 TCP flows reaching destination rules receive bounded TLS/HTTP name
  inspection without destination override or DNS lookup of the recovered name.
- Explicit domain/FakeIP flows and whole-client exits retain their existing paths.
- Selected client scope, WireGuard behavior, protected destinations and unavailable
  tunnel rejection remain unchanged.
- QUIC, ECH and application DNS limitations are documented; actual Android support
  requires device verification with gateway DNS through the proxy.
- Generator regressions and pinned-runtime synthetic flow checks pass before a
  separately coordinated RAC sandbox deployment. Production is excluded.

## Evidence

Before the fix, the same sandbox profile and HTTPS URL returned the DO exit when
SOCKS carried the domain, and the RAC exit when SOCKS carried a locally resolved IP.
Gateway DNS returned a FakeIP whose numeric request correctly used the DO exit.
These controlled TCP observations do not verify Android configuration or QUIC.

Local verification passed with native sing-box 1.12.25: TLS SNI, delayed fragmented
ClientHello, HTTP Host, selected-user isolation, unrelated names, absent SNI, opaque
data, silent-stream release at 300 ms, recovered gateway-name rejection, private
and gateway IP rejection, and original numeric destination preservation. The
fixture replaces only inbound/egress transports with authenticated loopback SOCKS
fixtures; it retains generated routing policy and contacts no external destination.
Full `internal/singbox` tests, focused race checks and Linux-target package lint
passed. RAC deployment and actual Android verification remain separate work.
