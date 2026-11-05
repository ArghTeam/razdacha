# #318 — Independent DO + WARP exit

Tracker: [#318](https://github.com/ArghTeam/razdacha/issues/318).

Status: Completed sandbox operational verification on 2026-11-06. This record
changes no application code and authorizes no release or production deployment.

## Goal and scope

Retain independent ordinary RAC tunnel entries for direct DO and DO + WARP.
WARP is freshly registered and runs on DO, outside RAC's native chain model:

`client -> RAC -> independent DO REALITY service -> DO userspace WARP -> destination`

Only RAC `193.233.112.227` and DO `165.22.196.130` participate. Production and
prior DO WireGuard services/configurations are excluded. No cloud resource was
created. No ADR or product contract is superseded. [Task #316](316-owned-reality-warp-chain.md)
remains the unchanged historical record of the earlier native RAC WARP experiment;
its retained topology is no longer the current sandbox topology.

## Checkout and runtime identity

- Designated primary: `/Users/romanzhigalkin/Web/razdacha`, branch `main`;
  unrelated local edits were preserved.
- Remote/base: freshly fetched `origin/main` at
  `5bd63caa4a4237983aefc3ca87d3613f1223877e`.
- Feature checkout: `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/docs-318-do-warp`,
  branch `docs/318-do-warp`.
- RAC application remains `0.11.0-rc.12.rac.071331182ff1`, source
  `071331182ff1e0477c4e0d12c0adfee5fb844cbf`. No daemon redeployment,
  public release or tag accompanies this record.
- Both DO exits use sing-box `1.12.25`. The independently copied Linux binary is
  pinned to SHA256 `d14cf69a7dd1d49e88a1f033ded3213a8f3503c57a8561ed6f166f3b388f4c41`.

## Independent DO service

The original `/opt/razdacha-reality-exit-310` and
`razdacha-reality-exit-310.service` retain TCP443 and their original credentials.
MainPID `162617`, configuration SHA256
`675f22d30e7cae8f9dede8fbb5f1931208dca9d98fe29450250c9f921876e260`
and unit SHA256
`b11bf329c55965d8777cf404e61b898110ff791ded0d83a4e8dd455367f18627`
were unchanged through the new-service stop/recovery. Existing interfaces and
default routes were preserved; prior WireGuard credentials were not reused.

The new `/opt/razdacha-reality-warp-318` and
`razdacha-reality-warp-318.service` own TCP2443 with fresh UUID, REALITY key pair
and short ID. Its cover handshake uses the owned RAC TLS endpoint. The unit has
no dependency on the original exit and needs no network administration capability
or TUN device. Cloud Firewall and host input allow2443 only from RAC
`193.233.112.227/32`; review found no broader cloud allow for that port. Unrelated
host rules and their order were preserved.

One fresh WARP identity was registered from DO after direct HTTPS confirmed
source `165.22.196.130`. Keys and token remain root-private. Durable pre-request
attempt markers prevent automatic registration/unregistration retries after
ambiguous outcomes; saved identity resumes without re-registration. An unregister
attempt blocks activation/probing pending manual identity recovery.

The userspace WireGuard endpoint uses MTU1280, its assigned IPv4 address and
validated literal peer `162.159.192.10:2408` from registration. Pinning avoids
startup DNS recursion; the provider endpoint has no promised lifetime. DoH to
`1.1.1.1`, using the Cloudflare DNS TLS name, detours through WARP. DNS53 hijack,
resolution and private/special/self-address/IPv6 rejection precede final WARP.
There is no direct fallback, kernel WireGuard interface or host-wide WARP route.

## Current RAC policy and cleanup

The ordinary VLESS tunnel `c6c37938-92c3-456f-b615-07f76a66f5e9`, named
`DO + WARP`, points at DO2443. Selected Test Client rule
`89557432-c415-414e-b3a9-79248a4d3d1e` now targets it with empty `via_tunnel_id`
and retains only `www.cloudflare.com`. RAC sees one ordinary VLESS hop; WARP
belongs to the independent DO service.

Original direct DO tunnel `57621be3-0683-4dc8-89e8-db0b87997e94` and IPinfo
rule `f5b1b568-247c-4888-a552-872900d319f7` remain unchanged. Both clients,
password, keys and unrelated persistent settings/policy fingerprints were
preserved. Existing APIs and guarded Apply were used after consistent private
SQLite backups.

After positive client verification and removal of all references, old native RAC
WARP tunnel `8b74f807-9293-4961-82c7-cd13e6055cc0` was deleted and Apply completed.
The daemon attempts provider unregistration after local deletion; local deletion
does not prove provider cleanup success. Final RAC had zero WARP registrations
and no WireGuard runtime endpoints. Daemon and sing-box were active, ingress was
released and Apply was clean. Temporary local exported profiles and the owned API session were removed.

## Observed verification

The new configuration passed `sing-box check`. An isolated DO mixed-listener
child then passed positive TCP/UDP WARP checks. TCP trace reported `warp=on`; SOCKS UDP STUN mapped `104.28.219.139`. Host direct
HTTPS remained DO `165.22.196.130`. The child was terminated after its probe.

The actual exported REALITY client retained outbound, DNS and route sections;
only inbounds were replaced with loopback diagnostic listeners. It started no
local TUN and changed no local system route or DNS settings.

| Check | Observation |
|---|---|
| Selected hostname HTTPS trace | `warp=on`, `104.28.219.140`, `loc=NL`, `colo=AMS` |
| Public IPv4 override with original URL/TLS name | `warp=on` |
| Gateway FakeIP followed by selected HTTPS | `warp=on` |
| Actual client UDP STUN with temporary owned STUN matcher | Mapped public address `104.28.251.139` |
| Original IPinfo DO-only route | `165.22.196.130` |
| Unselected ipify control | RAC `193.233.112.227` |
| Stop only the new service | Selected request failed; original DO IPinfo and RAC direct controls worked |
| New-service recovery | Selected trace passed; original DO443 PID/config/unit unchanged |
| After old native RAC WARP deletion | Hostname, numeric TLS-name and gateway FakeIP traces passed again; both controls retained their exits |

UDP was verified before restoring the temporary `stun.cloudflare.com` matcher to
WWW-only. Final post-deletion HTTPS results do not constitute a new UDP test.
The recovered new-service MainPID was `167402`.

## Limits and rollback

Fresh DO registration did not establish a location change: observed NL/AMS
matched the earlier native chain. Server location and registration source do not
guarantee WARP egress country. WARP is a Cloudflare network exit, not a residential
IP service. Observed addresses and locations are point-in-time, not reserved IPs.

The DO service resolves through its own WARP DoH. RAC shared REALITY destination
rules retain their existing upstream contract; this does not prove all destination
DNS exits through WARP. The foreign local FakeIP diagnostic from #316 remains an
unsupported input. No claim covers Android TUN startup, arbitrary applications or
L3/ICMP, IPv6 Internet access, indefinite stability, general DPI resistance or
universal leakage absence.

Rollback first removes or deliberately retargets only the owned CF rule and
Applies through the guarded publisher. Remove the new ordinary RAC tunnel only
when unreferenced. Stop/disable/remove only the new DO unit and its owned firewall
rules. Preserve original DO443 and both client identities. Retain the private
WARP identity for recovery unless explicit provider cleanup is requested;
unregister is separate from rollback. Never restore clone archives, activate old
credentials or manually release a failed publication guard.

Private helpers, backups and proof JSON remain outside the repository under
`/private/tmp/razdacha-do-warp`. No authentication UUID, private key, token, session or imported
profile is published. Documentation checks cover local Markdown links and
`git diff --check`; no application build or Go test is needed for this docs-only PR.
