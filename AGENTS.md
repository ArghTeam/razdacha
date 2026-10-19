# razdacha — project instructions

## Product and current versions

Selective VPN gateway for Debian/Ubuntu. Standard WireGuard clients send traffic to
`wg0`; selected resources go through configured tunnels and everything else directly.
No additional client software is required.

The latest published daemon release is **v0.10.0** (2026-08-21). This repository also
contains **razvedka 0.3.0**, a Chrome DevTools extension developed after that release.
It has its own version and is not part of the daemon build or release archive.

One built-in pool rotates free igareck keys ([ADR 0018](docs/decisions/0018-unified-pool.md)).
It excludes blocklisted countries (RU and BY by default) and keys without transport
encryption ([ADR 0020](docs/decisions/0020-pool-country-blocklist.md)). It starts disabled;
no catalog requests are made until enabled. Assign clients through peer-scoped rules.

Tunnel health is checked on a schedule and persisted ([ADR 0011](docs/decisions/0011-tunnel-check-schedule.md)).
Optional Telegram notifications confirm changes across three checks and start disabled.
Full backups include peer keys and are encrypted before external delivery
([ADR 0016](docs/decisions/0016-backup-full-and-encrypted.md)).

The installer generates a self-signed certificate. Incoming WireGuard obfuscation,
published `.deb` packages, and WebSocket updates are not implemented. The panel polls REST.
The dormant `make deb` target is not a supported packaging path.

## Stack and layout

- Go: `go.mod` is authoritative (currently 1.23.1); `CGO_ENABLED=0`.
- SQLite: `modernc.org/sqlite`; networking: `google/nftables`, `netlink`, `wgctrl`.
- Typed configuration: `sing-box/option`; sing-box runs as a separate service.
- Panel: HTML, CSS and ES modules in `ui/dist`, embedded with `go:embed`.
  No frontend build step or Node dependency for the daemon.
- Module: `github.com/ArghTeam/razdacha`; remote: `git@github.com:ArghTeam/razdacha.git`.

| Path | Responsibility |
|---|---|
| `cmd/razdachad/` | Daemon and scheduler wiring |
| `cmd/razdacha/` | Setup, restore, uninstall CLI |
| `internal/store/` | SQLite, migrations, peer keys, settings |
| `internal/netstack/` | `wg0`, nftables, routing via netlink |
| `internal/networkstate/` | Private committed policy/list records and fresh bootstrap authority |
| `internal/singbox/` | Full config generation, URL parsing, validation and apply |
| `internal/lists/` | Rule lists, pool catalogs, cache and scheduling |
| `internal/clash/`, `internal/diag/`, `internal/api/` | Runtime API, diagnostics, authentication and panel |
| `internal/packaging/` | nginx, TLS, sysctl, systemd, sing-box installation |
| `internal/qr/` | Terminal QR codes without an external binary |
| `ui/dist/` | Embedded panel assets |
| `packaging/install.sh` | Release installer |
| `tools/razvedka/` | Independent Chrome DevTools extension |

Wrap errors with `fmt.Errorf("...: %w", err)` and log with `log/slog`.
All newly written repository content, comments, logs, commit messages, PR text and
tracker content must be English. User conversation is Russian. Existing Russian runtime
text is legacy content, not an exception for new text.

## Maintained context

Read relevant invariants before editing. [Context index](context/index.md) links seven
layers: store, singbox, netstack, lists, api, ui and packaging. The api layer includes
`internal/diag` and `internal/clash`. Update maintained invariants and context in the
same PR when final behavior, constraints or rationale change. Preserve historical
ADRs, task records and logs as records of their original work.

## Codex workflow

- Modify only repositories explicitly authorized by the user, including their worktrees.
  Inspect other repositories only for context; editing requires separate authorization.
- Use the Codex-native `codex-flow` skill and `.codex/workflow.json` when available.
  Do not consult `.claude/`, `CLAUDE.md`, or Claude-specific orchestration configuration.
- Track substantive tasks in GitHub Issues in `ArghTeam/razdacha` with `in-progress`
  and `layer:<name>` labels. Record goals and acceptance criteria; use
  `tasks/<number>-<slug>.md` for nontrivial specifications.
- Before branching, fetch the intended remote base. Record the absolute designated
  primary checkout, remote, target branch and verified base SHA. Explicitly offline
  work uses the agreed local base with its freshness limitation stated.
- Keep the primary checkout available for builds. Implement in `.codex/worktrees/<branch>`;
  one task and one writing agent own each worktree. For substantial work, delegate
  independent exploration, mechanical checks, implementation and final review.
  Parallel writers must own disjoint files or domains; keep trivial work in the primary thread.
- Use explorer/mechanical for bounded checks, thinker for ambiguous analysis, strategist
  only for unusually difficult decisions, worker for implementation, reviewer for final review.
- Run relevant checks, inspect the diff, update maintained context, commit, push and open
  a PR when a GitHub remote is available. Implementation agents return an open PR and
  never merge. PR readiness does not grant merge authority.
- Never rebase, force-push, or amend a pushed commit. Merge the fresh target branch into
  an outdated branch or use `gh pr update-branch`.
- After an authorized, confirmed merge, the primary agent safely synchronizes the
  designated primary checkout with a fast-forward-only update, preserves local edits,
  verifies the merged commit is reachable and rereads updated instructions/context.
  Report blockers before dependent work. Follow `codex-flow` for the procedure.
- After successful synchronization, remove the completed worktree/local branch and prune
  stale refs. Move the specification to `tasks/done/`, update the layer log, close the
  issue and remove `in-progress` as appropriate. Preserve uncommitted work.

## Decisions and source material

Read [docs/decisions/](docs/decisions/README.md) before architectural proposals.
Name any decision a proposal supersedes and explain why. Add a new ADR and mark the
previous one superseded instead of rewriting it. Document intended behavior before
changing code; layer invariants link to ADRs rather than duplicate their rationale.

`~/Web/podkop` is a read-only architectural reference: never edit or commit there.
Verify Podkop `file:line` references with a search before writing them. Existing analysis:
[docs/research/podkop-analysis.md](docs/research/podkop-analysis.md). Code reuse is allowed
under GPL-2.0-or-later ([ADR 0007](docs/decisions/0007-license-gplv2.md)).

## Required runtime invariants

- Default client and server `wg0` MTU: **1280**. Configurable range: **1280–1420**,
  bounded by `store.MinClientMTU` and `store.MaxClientMTU` ([ADR 0004](docs/decisions/0004-client-mtu-1280.md)).
- sing-box DNS uses `strategy: ipv4_only`; FakeIP has only `inet4_range`. Client IPv6
  is disabled by DNS, `AllowedIPs = ::/0` without a client IPv6 address, and nft rejection.
- Outgoing WireGuard tunnels use sing-box userspace `endpoints`, never kernel `wg1`/`wg2`
  interfaces or `bind_interface`.
- Generate the entire sing-box configuration from the database. Run `sing-box check`
  before applying; keep the previous configuration on validation failure.
- A rule with an unavailable tunnel remains in both `route.rules` and `dns.rules` with
  `reject` ([ADR 0013](docs/decisions/0013-unavailable-tunnel-rejects.md)); never silently remove it.
- Publish plain-list policy through the serialized guarded generation protocol ([ADR 0023](docs/decisions/0023-coherent-network-generation.md)): prepare/check first, guard client ingress, reconcile WG/route/runtime/nft, persist authority, discard bootstrap authority, release last. Post-guard failure remains closed; recovery never restores live peers/authentication. Peer allocation shares the gate and reserves committed addresses. Autonomous remote-list coherence remains issue #220.
- Use netlink libraries instead of `nft`, `ip`, `wg`, or `wg-quick` subprocesses.
- Build static binaries with `CGO_ENABLED=0` and `modernc.org/sqlite`.
- Client configs always include `DNS = 10.8.0.1`, `MTU`,
  `AllowedIPs = 0.0.0.0/0, ::/0`, and `PersistentKeepalive = 25`; these are not per-peer settings.

## Commands and installation

```sh
make build      # Build the daemon
make build-cli  # Build the setup/restore/uninstall CLI
make test       # go test ./...
make lint       # golangci-lint run
```

`ui/dist` is embedded as committed. Install local lint/format tools separately; CI runs
lint. Runtime integration checks require the appropriate Linux environment.

```sh
curl -fsSL https://github.com/ArghTeam/razdacha/releases/latest/download/install.sh | sh
```

`RAZDACHA_PUBLIC=1` exposes the panel on all interfaces; `RAZDACHA_PUBLIC=0` restricts it
to the VPN again. With no override, the saved database mode is retained; a fresh install
is private. `--dry-run` performs preflight checks only. A `v*` tag triggers Linux
amd64/arm64 archives, `install.sh` and checksums.
