# #316 — Owned REALITY to WARP chain verification

Tracker: [#316](https://github.com/ArghTeam/razdacha/issues/316).

Status: Completed operational verification on 2026-11-06. This documentation
record does not authorize a release or production deployment.

## Goal and scope

Exercise the existing two-hop rule model with a real authenticated REALITY client:

`client -> RAC -> DO Reality Lab -> Cloudflare WARP -> selected destination`

This changes sandbox configuration only. It adds no runtime code, dependency or
policy contract and supersedes no ADR. [ADR 0012](../../docs/decisions/0012-tunnel-chain-detour.md)
retains its WARP endpoint clone model; [ADR 0030](../../docs/decisions/0030-wireguard-parent-startup-alias.md)
and [ADR 0032](../../docs/decisions/0032-parallel-authenticated-client-ingress.md)
retain their startup and authenticated ingress boundaries.

Only RAC `193.233.112.227` and the existing owned DO exit `165.22.196.130`
participated. Production, original cloned credentials and prior DO WireGuard
services/configurations were excluded. No droplet or paid resource was created.

## Checkout and runtime identity

- Designated primary checkout: `/Users/romanzhigalkin/Web/razdacha`, branch `main`.
  Its unrelated local edits remain outside this documentation worktree's scope.
- Remote/base: `origin/main`, freshly fetched at
  `f28d7606610f92bea721bf5222f6b174a8640478`.
- Documentation checkout:
  `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/docs-316-reality-warp-chain`,
  branch `docs/316-reality-warp-chain`.
- RAC ran `0.11.0-rc.12.rac.071331182ff1`, source
  `071331182ff1e0477c4e0d12c0adfee5fb844cbf`, with sing-box `1.12.25`.
  This documentation update needs no daemon redeployment or release.
- The existing dedicated DO REALITY service and host routes remained unchanged.
  WARP was not installed on DO. The RAC userspace endpoint adds no host-wide WARP
  default route; selected policy still uses the standard guarded publisher.

## Native chain and retained policy

One fresh free WARP identity was registered through the application API. The
generated userspace WireGuard endpoint clone owns the existing DO tunnel tag as
its `detour`; the ordinary WARP endpoint remains separate. The selected route
points at that clone, so WARP packets travel inside REALITY to DO before leaving
for Cloudflare. No kernel `wg1`/`wg2` interface was created; RAC retained `ens192`,
`lo` and `wg0`.

The reusable objects are `Cloudflare WARP Lab` and the selected-client
`Cloudflare WARP check` rule. The rule targets only Test Client and, after test
cleanup, only `www.cloudflare.com`. The existing `IPinfo via DO Reality` rule
keeps its original DO-only route: IPinfo returned HTTP 429 through the shared
WARP exit during verification. The unselected client and ordinary direct control
retain RAC direct routing.

The first-hop tunnel is `57621be3-0683-4dc8-89e8-db0b87997e94`; the fresh WARP
tunnel is `8b74f807-9293-4961-82c7-cd13e6055cc0`; the retained check rule is
`89557432-c415-414e-b3a9-79248a4d3d1e`. These are object identifiers, not client
credentials. Private keys, device tokens, imported profiles and sessions are not
published in this record.

## Observed evidence

The actual local sing-box client preserved the exported outbound, DNS and route
sections and replaced only its inbounds with loopback diagnostic listeners. It
did not start a TUN or change the local machine's system DNS or routes.

| Check | Observation |
|---|---|
| HTTPS Cloudflare trace through the selected chain | `warp=on`, exit `104.28.219.140`, `loc=NL`, `colo=AMS` |
| Numeric public IPv4 with the original TLS name | `warp=on` using a per-request address override |
| Gateway DNS/FakeIP followed by HTTPS | `warp=on` |
| UDP STUN binding through the selected chain, before temporary matcher cleanup | Public mapped exits `104.28.251.138` and `104.28.251.139` were observed |
| Unselected HTTPS ipify control | RAC exit `193.233.112.227` |
| Configured-disabled WARP leg plus Apply | Hostname and numeric selected HTTPS requests failed; the direct control still worked |
| Restored WARP leg plus Apply | Positive trace, FakeIP and UDP checks passed again |
| Final WWW-only WARP rule | Hostname, real-public-IP TLS-name and gateway FakeIP HTTPS traces still reported `warp=on` |
| Final original IPinfo rule | DO exit `165.22.196.130` |
| Unselected Sandbox client | IPinfo and ipify returned RAC `193.233.112.227`; Cloudflare trace reported `warp=off` |

The disabled-leg test exercises generated rejection for an unavailable configured
second hop. It is not a physical handshake/packet outage test. A separate DO
diagnostic during that window reported its one IP-literal HTTPS target up at
167 ms; its independent transport signal remained unknown.

The Mac's existing OS resolver initially returned foreign FakeIP `198.18.6.110`
for `www.cloudflare.com`. Passing that address to RAC produced a TLS failure.
RAC's OS resolver returned public IPv4 addresses `104.16.123.96` and
`104.16.124.96`; supplying one only to the diagnostic request preserved the URL
and TLS name and passed. This environment-dependent foreign-cache failure does
not establish a WARP or visible-name inspection regression. System DNS was not
modified to obtain the successful result.

## Stability, cleanup and limits

Four bounded samples over 143.6 seconds retained `warp=on` for hostname and
public-numeric HTTPS requests and successful UDP STUN responses. The configured
health interval was 120 seconds. The ordinary WARP endpoint's health timestamps
advanced twice during that window, while its status remained `unknown`; those
standalone health observations are distinct from the successful detoured client
chain. This finite window does not establish indefinite stability or exclude all
possible interactions between the original endpoint and clone. Mac default IPv4
routes, default IPv6 routes and system DNS were unchanged across the window.

The temporary `stun.cloudflare.com` matcher was removed before the final Apply;
the retained check rule contains only `www.cloudflare.com`. The final UDP field
therefore refers to the earlier positive and stability observations, not a new
post-cleanup STUN test. Final Apply was clean, both exported client profiles were
byte-identical and the protected saved-state fingerprint was preserved apart
from the explicitly owned new records. The original IPinfo rule was restored
exactly, including its empty second hop. Temporary-session/export cleanup
removed the owned session and local private profile copies, and all local probe
processes stopped. The password, peer keys and unrelated saved-state hashes were
preserved. Daemon, sing-box and nginx were active; ingress was released, runtime
configuration matched its checked authority and only `ens192`, `lo` and `wg0`
were present. No original clone archive was restored.

The actual HTTPS panel displayed `DO Reality Lab -> Cloudflare WARP Lab` for the
selected Test Client and WWW-only rule. Its editor selected the correct first
and second hops. This read-only browser check performed no server writes and
reported no uncaught errors.

These observations do not establish that all destination DNS exits through WARP:
shared REALITY resolution still uses RAC's existing upstream, and selective DNS
policy retains its existing contract. Gateway FakeIP testing establishes the
client-facing name mapping, not same-exit resolution on every hop. No claim covers
Android TUN startup, arbitrary applications/L3/ICMP, IPv6 Internet access, idle
online presence, general DPI resistance or universal leakage absence. Observed
exit addresses and location are point-in-time results, not assigned static IPs.

## Bounded rollback

Keep the original IPinfo rule's `via_tunnel_id` empty. Remove only the retained
check rule, Apply through the standard guarded publisher, then remove only the
fresh WARP tunnel if no other rule references it and Apply again to remove its
standalone runtime endpoint. The deletion lifecycle attempts provider
unregistration after local deletion; that request is best-effort and a provider
failure is logged rather than returned as a failed local deletion. Check the
result/logs before claiming provider cleanup. Keep the DO
tunnel and both client identities intact. If publication remains guarded, repair
checked Apply rather than manually releasing the guard. Never restore the old
clone archive or activate old credentials as a recovery shortcut.

Private operational evidence is retained outside the repository under
`/private/tmp/razdacha-warp-chain`. A current-state backup and non-secret
verification record remain in the owner-only RAC directory
`/var/backups/razdacha/warp-chain-316-20261105T204607Z`; the backup was not restored.
This document includes only non-secret outcomes.
Documentation verification checks local Markdown targets and `git diff --check`.
No Go code changed, so additional Go or browser test runs are not required for
this documentation-only change.
