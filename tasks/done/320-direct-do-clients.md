# #320 — Direct DO client profiles

Tracker: [#320](https://github.com/ArghTeam/razdacha/issues/320).

Status: Completed sandbox configuration and bounded client verification on
2026-11-06. No application code, public release or production deployment changed.

## Goal and scope

Provide two independently authenticated clients connecting directly to the owned
DO host, without using RAC as their configured proxy ingress:

- `DO Direct`: client -> DO TCP443 -> destination.
- `DO WARP`: client -> DO TCP2443 -> DO userspace WARP -> destination.

The existing RAC tunnel users remain available. These are direct server profiles,
not new RAC peers or RAC whole-client egress assignments. REALITY retains the
existing owned RAC cover TLS endpoint/SNI; this is separate from the authenticated
client's configured DO proxy hop. Production and legacy DO WireGuard
services/configurations are excluded. No cloud resource was created.

[Task #318](318-do-warp-egress.md) remains the historical record of the previous
RAC-only firewall scope. The new explicit direct-client requirement adds public
IPv4 permits for the two authenticated DO TCP listeners. No ADR or product
routing contract is superseded.

## Checkout and deployed identity

- Designated primary: `/Users/romanzhigalkin/Web/razdacha`, branch `main`;
  unrelated local edits remain untouched.
- Remote/base: freshly fetched `origin/main` at
  `034a5afda6e77665cfb860a335c080cf3546dcc7`.
- Feature checkout:
  `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/docs-320-direct-clients`,
  branch `docs/320-direct-clients`.
- DO uses the existing pinned sing-box `1.12.25` services from #310 and #318.
  RAC application remains the previously deployed RC12; no daemon rebuild,
  redeployment or release tag is required by this operational change.

## Authentication, publication and access

One fresh UUID user was appended to each existing service. Both services now
have two users: the retained RAC user and one independent direct client. Existing
user UUIDs, server REALITY keys, short IDs, SNI, DNS and route policies are unchanged.
No existing credential was reused for a new client. The two units and host
interfaces/default routes were preserved.

Candidates passed `sing-box check` before atomic publication. Only
`razdacha-reality-exit-310.service` and `razdacha-reality-warp-318.service` were
restarted, intentionally replacing their previous PIDs. Checked backups preserve
exact original configurations. Explicit activation/rollback always restarts the
owned services even when disk hashes already match: matching bytes alone do not
prove the live process admitted or revoked a user after an interrupted operation.
A failed activation restores checked original configurations and restarts the
changed owned services.

The direct service's new configuration SHA256 is
`fda3db290762b937eb3b1284e9eb162eb366f6d4612c805e2c265a09e9d2cab7`,
MainPID `168252`; the WARP service's is
`af145e11ad363b9459fe5fad46c7ace8dea1a04a48b8c52156dd343d837d872c`,
MainPID `168267`.

Cloud Firewall and host input received additive public IPv4 TCP443/TCP2443
permits. Existing restricted RAC permits remain. Unrelated host rule handles and
order were preserved. The cloud provider reordered its rule list; comparison
verified the original rule multiset plus only the two owned additions, with
outbound rules and firewall bindings preserved. No public UDP service port or
public IPv6 permit was added.

## Private deliverables and client policy

Each client has a private VLESS URI, a locally generated SVG QR and a complete
sing-box `1.12.25` TUN JSON profile under
`/private/tmp/razdacha-do-direct-clients/profiles`. QR generation uses the local
project implementation, without an external QR service. Files are owner-only;
no authentication UUID, private key, profile or QR content belongs in this record.

The JSON uses the literal DO address, the matching REALITY public key/SNI/short
ID, `xtls-rprx-vision`, `xudp` and MTU1280. Its DoH resolver reaches literal
`1.1.1.1` through the selected proxy with the Cloudflare DNS TLS name. Port53 is
hijacked before final proxy routing; AAAA/HTTPS DNS queries and IPv6 traffic are
rejected, with IPv6 captured by the TUN route. There is no direct client outbound
fallback. URI imports do not carry this full DNS/route policy; importer defaults
and device behavior remain separate compatibility boundaries.

## Observed client verification

Both exported full profiles passed local sing-box `1.12.25` schema checks. Actual
client probes replaced only inbounds with loopback mixed/DNS diagnostic listeners;
outbounds, DNS and route sections were unchanged. No local TUN was started or
operator system route/DNS configuration changed. Probe children were terminated.

The existing exported RAC client also retained working Cloudflare hostname,
numeric TLS-name and gateway FakeIP requests through DO + WARP. Original IPinfo
still exited DO and its direct control exited RAC. The complete protected RAC
saved-state fingerprint matched its pre-change value; Apply was clean and ingress
released. The owned temporary API session and RAC exports were removed; the new
direct-client profiles remain privately available for delivery.

| Check | DO Direct | DO WARP |
|---|---|---|
| Configured proxy endpoint | `165.22.196.130:443` | `165.22.196.130:2443` |
| HTTPS Cloudflare trace | `165.22.196.130`, `warp=off` | `104.28.219.140`, `warp=on` |
| HTTPS ipify | `165.22.196.130` | `104.28.219.140` |
| Real SOCKS UDP STUN | `165.22.196.130` | `104.28.219.139` |
| A query through the configured proxy DoH via loopback DNS bridge | Two answers for `example.com` | Two answers for `example.com` |

The profiles point directly at DO rather than RAC ingress. The operator machine
has an existing transparent VPN, so these results do not prove a particular
physical carrier path or original transport source address. Android on-device
TUN startup, v2rayNG import behavior, arbitrary applications, negative IPv6/private
address probes, indefinite stability and general DPI resistance were not tested.
WARP exits remain point-in-time Cloudflare addresses, with no country, static IP
or residential IP guarantee.

## Bounded rollback and evidence

Restore only each checked original DO configuration and restart its owned unit
to revoke the added direct-client UUIDs while retaining the original RAC users.
Refuse automatic restoration if another operator changed the configurations.
Remove only the two owned public firewall permits, retaining prior RAC rules and
all unrelated host/cloud policy. Keep service units, server keys and the existing
DO WARP registration intact; this rollback does not unregister WARP or stop the
original DO exit. Revoke/delete only the private new client deliverables after
server revocation is verified.

Private backups, helpers and sanitized proof JSON remain under
`/private/tmp/razdacha-do-direct-clients` and root-only DO
`/opt/razdacha-direct-clients-320`. The operational helper passed four local tests,
including preservation of existing users, forced restart despite matching disk
bytes and exact-original recovery after a partial start failure. This docs-only
PR checks local Markdown links and `git diff --check`; no additional application
build or Go test is required.
