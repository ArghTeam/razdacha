# Architecture

## Components

```text
Browser: static HTML/CSS/ES modules from ui/dist
              │ HTTPS
nginx: authenticated HTTPS on all interfaces for fresh setup; explicit private mode uses 10.8.0.1:443
              │ proxy_pass to 127.0.0.1:8080
razdachad: Go daemon, root, systemd
    ├─ store       SQLite: peers, tunnels, rules, settings
    ├─ netstack    wg0, nftables, policy routing via netlink
    ├─ singbox     typed config generation, validation and apply
    ├─ lists       rule lists and pool catalogs, cache and schedules
    ├─ clash       sing-box runtime API client
    ├─ diag        health and routing diagnostics
    ├─ api         authenticated REST and embedded panel
    └─ packaging   installation, nginx, TLS and systemd integration
              │ full config write + systemctl reload-or-restart
sing-box: separate systemd service
```

The daemon embeds committed panel assets through `go:embed`. There is no Svelte,
Tailwind or frontend build pipeline. The panel polls REST rather than a WebSocket.

sing-box is a separate process so its failure does not take down the management API.
The daemon builds `option.Options` from database state, validates with `sing-box check`,
and keeps the previous configuration on validation failure
([ADR 0006](decisions/0006-go-daemon.md)). The generated service has no `ExecReload`,
so `reload-or-restart` restarts it. Outgoing WireGuard runs in sing-box userspace
`endpoints`, not additional kernel WireGuard interfaces
([ADR 0002](decisions/0002-userspace-wireguard-outbound.md)).

## Traffic flow

### Selected domain

1. A WireGuard client queries DNS at `10.8.0.1`.
2. sing-box matches the domain to a rule and returns a FakeIP from `198.18.0.0/15`.
3. Client traffic to that address arrives through `wg0`.
4. nftables marks selected traffic with `0x00100000`; policy routing table 105
   sends marked packets to local delivery through loopback.
5. nftables tproxy redirects TCP/UDP to `127.0.0.1:1602`.
6. sing-box recovers the domain and opens the connection through the rule's tunnel.

Peer-scoped rules restrict matching to selected client addresses. Subnet lists do not
need FakeIP: their IPv4 networks are also installed in the nft set `razdacha_subnets`
so traffic to those real addresses reaches tproxy. See [networking](03-networking.md)
and [DNS/FakeIP](04-dns-fakeip.md) for rule ordering and interception details.

An unavailable tunnel does not remove its rule: both DNS and routing retain an explicit
rejection ([ADR 0013](decisions/0013-unavailable-tunnel-rejects.md)). This prevents an
unavailable selected path from silently becoming a direct path.

### Direct traffic

A domain outside the selected rules receives its real IPv4 address. Traffic that does
not match a selected subnet or other proxy rule is forwarded and masqueraded on the
server's outgoing interface without traversing the sing-box proxy path.

This forwarding path avoids a proxy hop, but DNS for clients still depends on sing-box.
A sing-box outage can therefore break new name lookups as well as selected traffic;
it does not guarantee uninterrupted general internet access. Kernel forwarding state
and `wg0` survive a daemon restart.

## Pool, health and backups

There is one built-in igareck pool, created disabled. It combines supported keys from
three subscriptions and falls back from githack to GitHub if needed. The pool filter
excludes blocklisted countries (RU/BY by default) based on key titles and rejects keys
without transport encryption; it does not perform geo-IP lookup
([ADR 0018](decisions/0018-unified-pool.md), [ADR 0020](decisions/0020-pool-country-blocklist.md)).
`urltest` selects a live member. Background catalog refresh applies pool membership
against the last applied snapshot without publishing unrelated pending rule edits.

Health checks persist their results. Optional Telegram notifications confirm a change
across three checks. Full backups include peer keys; external delivery is encrypted.
See [notifications](09-notifications.md) and [installation/restore](08-install-upgrade.md).

## Privileges, panel access and TLS

`razdachad` runs as root for WireGuard, nftables and route administration. The generated
systemd unit restricts capabilities to `CAP_NET_ADMIN`, `CAP_NET_RAW` and
`CAP_NET_BIND_SERVICE`, uses `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`
and `NoNewPrivileges=yes`, and allows writes to `/var/lib/razdacha`, `/etc/razdacha`
and `/etc/sing-box`. See `internal/packaging/units.go` for the complete unit.

nginx terminates TLS; the daemon only listens on loopback. Fresh installs default to
public authenticated HTTPS before VPN setup. `RAZDACHA_PUBLIC=1` selects all-interface access,
`RAZDACHA_PUBLIC=0` explicitly selects VPN-only access, and updates retain the saved mode without
an explicit override. Authentication is required in both modes: Argon2id password hash,
session cookie and per-address failed-login throttling. The daemon refuses to start
without a password. It trusts nginx client-address headers only from loopback requests.

The installer generates a **self-signed** ECDSA certificate using Go. There is no
implemented Let's Encrypt/certbot or ACME flow; that part of the earlier access design
has not been delivered. The certificate's SANs include the VPN address and, in public
mode, the detected external address. The certificate lasts ten years; the key is `0600`
and its directory `0700`. The installer prints the external panel URL and initial
password before the first peer's QR/configuration instructions (ADR 0034).

## Default addresses

| Component | Address | Exposure |
|---|---|---|
| Client WireGuard ingress | `0.0.0.0:51820/udp` | Public |
| HTTPS panel | `10.8.0.1:443` | VPN; all interfaces when public mode is enabled |
| HTTP redirect | `10.8.0.1:80` | Same mode as HTTPS; redirects to HTTPS |
| Daemon UI/API | `127.0.0.1:8080` | Loopback behind nginx |
| Client DNS | `10.8.0.1:53` | sing-box DNS inbound |
| tproxy | `127.0.0.1:1602` | Local |
| sing-box Clash API | `127.0.0.1:9090` | Local; accessed through the daemon |
| Client address pool | `10.8.0.0/24` | Configurable in stored settings |

Default client and server MTU is 1280; both use the configured `client_mtu`, bounded
at 1280–1420. Client configs include DNS `10.8.0.1`, IPv4/IPv6 default routes and
`PersistentKeepalive = 25`. Client IPv6 is intentionally disabled.

## State on disk

| Path | Contents |
|---|---|
| `/var/lib/razdacha/state.db` | SQLite: settings, peers and private keys, tunnels and rules |
| `/var/lib/razdacha/lists/` | Downloaded list cache |
| `/var/lib/razdacha/backups/` | Database copies made before upgrades |
| `/var/lib/razdacha/client-1.conf` | First client configuration |
| `/etc/razdacha/tls/cert.pem`, `key.pem` | Self-signed panel certificate and private key |
| `/etc/razdacha/nginx-default-disabled.json` | Record of disabling the distribution's default nginx site |
| `/etc/sing-box/config.json` | Entirely generated runtime config; manual edits are overwritten |
| `/etc/nginx/sites-available/razdacha` | Generated nginx site, linked from `sites-enabled` |
| `/etc/sysctl.d/99-razdacha.conf` | Forwarding and nonlocal-bind settings |
| `/etc/systemd/system/razdachad.service` | Daemon service |
| `/etc/systemd/system/sing-box.service` | Runtime service, only if no packaged unit exists |
| `/var/lib/sing-box/` | Runtime working directory and remote rule-set cache |
| `/usr/local/bin/razdachad`, `/usr/local/bin/razdacha` | Daemon and management CLI |

Settings are stored in SQLite, not `config.yaml`. The database is `0600 root:root`.
Storing peer private keys allows existing client configurations to be downloaded again
and preserved through full-state restore.

## Independent browser tool

[razvedka](../tools/razvedka/README.md) is a separately versioned Chrome DevTools
extension under `tools/razvedka/`, outside the daemon build. Its current entry shows
blocked requests and country signals for the current page; optional A/B comparison is
collapsed ([ADR 0022](decisions/0022-single-run-entry.md)). It does not switch tunnels
or require a connection to the daemon.
