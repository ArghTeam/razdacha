# razdacha

Selective VPN gateway for Debian/Ubuntu. Install it on a server with one command;
standard WireGuard clients connect to it, selected resources use your outgoing tunnels,
and everything else goes directly from the server.

```text
[phone] ───┐
[laptop] ──┼─→ wg0 ─→ razdacha ─→ [WireGuard / VLESS / Hysteria2] ─→ selected resources
[desktop] ─┘                 └─→ direct ──────────────────────────→ everything else
```

## Current versions

The latest published daemon release is [v0.10.0](https://github.com/ArghTeam/razdacha/releases/tag/v0.10.0)
(2026-08-21). Releases contain Linux amd64/arm64 archives, an installer and checksums.
There is no published `.deb` package.

[razvedka](tools/razvedka/README.md), version **0.3.0**, is a separate Chrome DevTools
extension developed after that daemon release. Open its DevTools panel before reloading
a page to inspect blocked requests and country signals, then copy domains for a rule.
Optional A/B comparison lives in the collapsed comparison section. The extension is
loaded from `tools/razvedka/`; it is not embedded in the daemon or its release archive.

## Install or update

```sh
curl -fsSL https://github.com/ArghTeam/razdacha/releases/latest/download/install.sh | sh
```

The installer runs preflight checks before making changes. To run checks only:

```sh
curl -fsSL https://github.com/ArghTeam/razdacha/releases/latest/download/install.sh | sh -s -- --dry-run
```

It prints the external HTTPS panel URL and generated password before the first client's
QR code. Save the password; it is printed once. Fresh installations default to a public,
password-protected panel with a self-signed certificate, so access does not require an
existing VPN connection. `RAZDACHA_PUBLIC=0` explicitly chooses VPN-only access;
`RAZDACHA_PUBLIC=1` enables public access. Updates retain the saved mode when no
override is supplied. See [installation and upgrades](docs/08-install-upgrade.md).

The ordinary built-in free-key pool starts disabled. Optional fresh-install Free start
instead enables the API pool and selective starter rules for the first client only;
Skip lets you configure personal tunnels through the panel. Ordinary pool activation rotates igareck keys,
excluding RU/BY by default and keys without transport encryption. Configure tunnels and
rules in the panel; the starter rule preset is offered for explicit acceptance.

Fresh installations open a guided web setup after password login: choose optional
free tunnels or keep current policy, review and explicitly Apply, then show/download
the initial client's saved profile and Finish. Setup can be reopened from the header;
upgrades preserve existing clients, routing and completion state.

Download a full backup from the panel or schedule encrypted Telegram delivery.
`razdacha restore <file>` restores peer keys as well as settings, so issued client
configurations keep working. Remove the installation with `razdacha uninstall`.

Requirements: Debian 12+ or Ubuntu 22.04+, kernel 5.15+, KVM/Xen/bare metal (not OpenVZ),
`amd64` or `arm64`. See the [platform matrix](docs/07-platform-support.md).

## Develop

The required Go version is declared in `go.mod` (currently 1.23.1). Builds use
`CGO_ENABLED=0`. The panel is committed HTML/CSS/ES modules in `ui/dist`, embedded as-is;
no Node or frontend build step is needed.

```sh
make build
make build-cli
make test
make lint
```

Networking integration checks need a suitable Linux environment. Local linting requires
`golangci-lint`; CI runs the repository lint configuration.

## Documentation

| Guide | Contents |
|---|---|
| [Vision](docs/00-vision.md) | Product goals and boundaries |
| [Architecture](docs/01-architecture.md) | Current components and traffic flow |
| [Data model](docs/02-data-model.md) | Tunnels, rules, peers and settings |
| [Networking](docs/03-networking.md) | nftables, tproxy, MTU and IPv6 |
| [DNS and FakeIP](docs/04-dns-fakeip.md) | Selective routing |
| [API](docs/05-api.md) | Daemon API contract |
| [Panel](docs/06-ui-screens.md) | Panel behavior |
| [Platforms](docs/07-platform-support.md) | Supported systems and requirements |
| [Installation](docs/08-install-upgrade.md) | Install, update, restore and uninstall |
| [Notifications](docs/09-notifications.md) | Telegram setup |
| [Router clients](docs/10-router-client.md) | WireGuard router configuration |
| [Staging validation](docs/11-staging-validation.md) | Ordered security checks, privacy and rollback |
| [Decisions](docs/decisions/README.md) | Architectural decisions and rationale |
| [Maintained context](context/index.md) | Layer invariants and work history |
| [Podkop analysis](docs/research/podkop-analysis.md) | Architectural source material |

## License

GPL-2.0-or-later. See [NOTICE.md](NOTICE.md) for acknowledgments.
