# Context index

Current layer descriptions are maintained alongside each layer's invariants.

- [Store](./store/index.md) — SQLite, migrations, peer keys and settings.
- [Singbox](./singbox/index.md) — Typed configuration generation, proxy URL parsing, validation and reload.
- [Netstack](./netstack/index.md) — `wg0`, nftables and routing through netlink; reversible system changes.
- [Lists](./lists/index.md) — Rule lists and pool catalogs, cache, schedules and nft subnet sets.
- [API](./api/index.md) — REST, embedded panel, diagnostics and the Clash API client.
- [UI](./ui/index.md) — Static HTML/CSS/ES modules embedded from `ui/dist`; no frontend build step.
- [Packaging](./packaging/index.md) — nginx, TLS, systemd, install/restore/uninstall, release archives and supported platforms.

The independently versioned [razvedka extension](../tools/razvedka/README.md) lives outside
the daemon build. Its current single-page entry is specified in
[ADR 0022](../docs/decisions/0022-single-run-entry.md).

The [parallel client ingress decision](../docs/decisions/0032-parallel-authenticated-client-ingress.md)
and [task #297](../tasks/297-alternative-ingress.md) describe the optional VLESS +
REALITY backend alongside WireGuard. It defaults disabled, keeps private live
credentials outside committed routing authority and extends guarded publication to
public listeners. The task distinguishes the earlier handwritten capability proof
from generated-policy, full-client-profile and deployment validation; no general
DPI resistance or untested application compatibility is implied.

[ADR 0036](../docs/decisions/0036-pending-reality-profile-delivery.md) separates saved
REALITY profile delivery from activation: pending QR/URI/JSON can be imported before
explicit Apply, with persistent pending notices and unchanged runtime admission.
