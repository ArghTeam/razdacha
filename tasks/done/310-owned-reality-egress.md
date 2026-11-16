# #310 — Owned REALITY exit verification

Tracker: [#310](https://github.com/ArghTeam/razdacha/issues/310).

Status: Completed operational verification on 2026-11-05; documentation PR does
not authorize a merge, release or production deployment.

## Goal and scope

Verify an actual compatible client through RAC's authenticated REALITY ingress and
an independently owned VLESS + REALITY exit, including TCP, UDP and DNS, exit
failure without direct fallback, recovery and preservation of existing RAC state.
This is an operational verification record, with no runtime code change or new
policy contract. [ADR 0028](../../docs/decisions/0028-peer-egress.md),
[ADR 0032](../../docs/decisions/0032-parallel-authenticated-client-ingress.md) and
[ADR 0036](../../docs/decisions/0036-pending-reality-profile-delivery.md) retain
their existing contracts and historical validation boundaries.

Only the existing DigitalOcean Amsterdam host `165.22.196.130` (resource
`610815921`) and RAC `193.233.112.227` participated. Production
`raz-zanet.zhrkian.org` was excluded. No cloud resource was created; existing-host
traffic was the only incremental provider usage.

## Reproducibility and safety boundary

- Designated primary checkout: `/Users/romanzhigalkin/Web/razdacha`, branch `main`.
  Its unrelated local edits were outside this documentation worktree's scope.
- Remote/base: `origin/main`, freshly fetched base
  `a8530e3579f89f02bc2e941f2ac45368f3a8bb37`.
- Documentation checkout:
  `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/docs-310-owned-reality-egress`,
  branch `docs/310-owned-reality-egress`.
- RAC reported `0.11.0-rc.10.rac.6e615aa9f521`, source
  `6e615aa9f5219a83d60f6a98a27f436ce7c1dd09`; this is the deployed candidate,
  distinct from the documentation base and latest stable release.
- Client and owned exit used sing-box `1.12.25`. The static Linux amd64 exit
  binary SHA-256 was
  `d14cf69a7dd1d49e88a1f033ded3213a8f3503c57a8561ed6f166f3b388f4c41`.
- Exit files and unit were isolated under `/opt/razdacha-reality-exit-310` and
  `razdacha-reality-exit-310.service`. A fresh private REALITY identity and client
  UUID were used. No credential, private key or import URI belongs in this record.
- WireGuard files, interfaces and prior unit files were preserved; WireGuard was
  neither activated nor probed.

## Exit and test setup

The ordinary exit used TCP 443, VLESS Vision and the client's uTLS `chrome`
fingerprint. Its cover SNI was `razdacha-rac.zhrkian.org`, with an explicit
handshake target `193.233.112.227:443`. Normal resolution used DoH to `1.1.1.1`
with TLS name `cloudflare-dns.com`. Authenticated exit traffic rejected private
destinations, IPv6 and the exit's own public address.

During bounded proof windows only, exact owned-address exceptions allowed TCP
18080 and UDP 18081 to nonce-checked, source-restricted sinks. A private UDP DNS
fixture on loopback port 19053 answered only random subdomains of the owned test
namespace `.egress-lab.invalid`, returning the exit's address. This intentional
fixture exception does not demonstrate ordinary public DNS resolution.

Cloud firewall changes admitted TCP 443 only from RAC's `/32`; the temporary sink
ports were likewise limited to RAC. The host's existing INPUT DROP required tagged
source-restricted accepts installed with a private Go netlink helper. Original
rule handles/order were preserved. Only the marked TCP 443 block was persisted in
`/etc/nftables.conf`, retaining original bytes outside that block; sink port rules
were runtime-only. Firewall changes used netlink; existing tables were not flushed
or globally reloaded.

RAC's baseline had two clients, no rules, a disabled pool and no pending Apply.
An owned session, without resetting the panel password, created the ordinary
`DO Reality Lab` tunnel and temporary REALITY-only `Egress lab 310` client with
whole-Internet egress assigned to that tunnel. Actual API profile export was
pending before Apply and available afterward with identical connection material.
Hashes bracketed settings, peers, original tunnels/rules and live identities,
excluding only the explicitly owned records. Existing state was unchanged across
that Apply; these observations were not atomic against concurrent panel writers.

## Observed transport evidence

The successful positive run used an actual Darwin sing-box `1.12.25` client. It
preserved the API-exported outbound, DNS and route sections and replaced only
inbounds with loopback direct test ports. No OS route or TUN was installed.

| Check | Observed result |
|---|---|
| HTTP with fresh nonce | Passed; sink observed the DO source address |
| UDP with fresh nonce | Passed; sink observed the DO source address |
| Client-facing DNS over UDP | Passed; owned resolver observed loopback-origin queries on DO |
| Client-facing DNS over TCP | Passed; owned resolver observed loopback-origin queries on DO |
| RAC WAN capture | Readiness barrier completed; 128 packets, zero kernel drops |
| Exit outage | All four probe requests failed; no probe nonce reached either sink |
| Recovery | All four fresh-nonce requests passed with expected DO sources |

DNS TCP/UDP labels describe queries to the client's loopback test listener.
The preserved client DNS/route configuration hijacked port 53 and used a UDP
upstream; both probes ultimately produced `dns-udp` observations on DO. They do
not establish DNS-over-TCP transport on every hop.

Direct HTTP/UDP controls from RAC to the owned sinks passed both before and after
the outage, distinguishing the selected-exit failure from sink unavailability.
RAC's sing-box PID and guard state stayed unchanged during that test. The DO exit
was restored after the outage window.

Offline classification of the RAC WAN positive capture found all 128 packets on
TCP 443 toward DO. The outage capture contained ten packets, also all TCP 443
toward DO. Both windows had zero direct sink-port packets, DNS-port packets or
owned plaintext nonces, zero kernel drops and equal captured/received counts.
The recovery capture reported 102 captured packets, 137 received by the filter
and zero kernel drops; it does not support an exhaustive or lossless capture claim.
All 102 captured recovery packets were TCP 443 toward DO, with zero direct sink
port packets, DNS-port packets or owned plaintext nonces in the captured subset.

After restoring the normal DoH configuration and removing proof exceptions, a real
HTTPS ipify request through the actual client, RAC and DO returned
`165.22.196.130`. This separately confirms ordinary DNS/HTTPS operation rather
than relying only on the owned DNS fixture. The live panel showed the retained
tunnel card with zero page errors. The application diagnostic reported HTTPS up
at 111 ms for its single IP-literal target; its transport signal remained unknown.
That diagnostic alone is not proof of all protocols, clients or per-tunnel DNS.

The first positive attempt failed because the proof DNS stub refused the cover
hostname. The successful run used literal-IP handshake bootstrap, retaining the
same keys and service unit, with fresh nonces. An initial installation assertion
also encountered an unrelated PackageKit process; a strict retry within one SSH
session passed. This does not establish cross-session stability of every host PID.

## Limits

The bounded exported-profile experiment exercises authenticated proxy TCP/UDP and
DNS. It does not prove OS TUN startup, arbitrary applications, encrypted incoming
WireGuard, arbitrary IP/ICMP, IPv6 Internet access, DPI resistance or universal
absence of leakage. Selective destination-rule DNS behavior, reboot persistence,
performance and cross-application compatibility were not established. Finite
zero-drop packet windows are evidence for their observed traffic only. The
synthetic SOCKS/veth evidence recorded in ADR 0028 remains
historical evidence for that earlier fixture, rather than being rewritten as this
owned-exit experiment.

## Final retained state and cleanup

The normal DO exit remains active and RAC retains the reusable `DO Reality Lab`
tunnel, unassigned to clients. Only RAC's `/32` can reach the new TCP 443 listener.
The temporary client and owned session were deleted, including its local session
file. Test sinks, proof DNS, proof destination exceptions, temporary listeners and
local/cloud test-port rules were removed. Original cloud rules remain intact.

RAC returned to its original two clients with no pending Apply; razdachad,
sing-box and nginx were active and `wg0` remained present. Protected original
settings, peers and authentication material matched the final saved-state hash
`f9b2603a957c83869c04c151b7c3cb0547a14c97de3904a590dd2727154cd768`.
DO's prior WireGuard files/modes/interfaces, prior unit files and critical SSH/WG
service PIDs were preserved. Persistent firewall bytes outside the owned TCP 443
block remained unchanged. No old cloned VPN state was activated or restored.

Private operational evidence remains outside the repository at
`/private/tmp/razdacha-egress-lab/final-evidence.json`; its non-secret outcomes are
recorded here. Credentials, profiles, session data and raw private captures are
not published. Documentation verification checks local Markdown targets and
`git diff --check`; no Go code changed, so no additional Go test run is required.
