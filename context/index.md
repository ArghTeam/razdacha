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
