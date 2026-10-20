# 231: Per-client whole-Internet routing

## Goal and contract

Existing clients retain shared-rule routing. A nullable `egress_tunnel_id` selects
one tunnel for all IPv4 TCP/UDP Internet traffic and gateway DNS from that client.
This source policy precedes ordinary destination rules and never falls back to direct.
The userspace WireGuard runtime does not carry arbitrary IP protocols: forwarded
ICMP and other unsupported protocols are rejected. IPv6 remains rejected. Gateway
DNS and administration remain accessible; private forwarding cannot bypass the exit.
Changes remain pending until explicit Apply.

## Plan

1. ADR 0028, restrictive tunnel references and nullable API patch semantics.
2. Source route/resolve and same-exit DNS, including cached FakeIP destinations.
3. Source-only nft interception and residual forwarding rejection.
4. Guarded generation v3, including bootstrap; committed refresh/recovery semantics.
5. Client mode/target controls, protocol help and pending Apply after reload.
6. Migrations, references, generator/runtime, nft, publication, API and browser tests.

## Verification boundary

Local checks are not live WireGuard evidence. The primary agent coordinates isolated
staging checks and independent review. No deployment or old-server access is included.

## Checkout provenance

- Authorized repository/primary: `/Users/romanzhigalkin/Web/razdacha`.
- Feature checkout: `.codex/worktrees/feat/231-peer-egress`.
- Branch `feat/231-peer-egress`; remote `origin`; target `main`.
- Fresh remote base `598a7777c30865457edd9b47498df4cec7a55e41`.

## Local verification and integration entry point

- Store/API references, migration, backup, nullable patch, concurrent metadata patch,
  status authentication/read-only behavior, generation envelope versions, source
  generator/DNS/bootstrap and nft policy tests cover acceptance/failure cases.
- `RAZDACHA_TEST_SINGBOX=/path/to/sing-box go test ./internal/singbox` checks
  generated UDP/DoT/DoH hostname-upstream configurations with pinned 1.12.25.
- Browser verification uses synthetic local state and actual static assets; no remote
  private data or deployed application is involved.
- Opt-in Linux integration: `RAZDACHA_NETNS_TEST=1 RAZDACHA_WIRE_EGRESS=1
  RAZDACHA_TEST_SINGBOX=/path/to/sing-box ./razdachad.test
  -test.run=^TestNetworkGenerationWire$ -test.v -test.timeout=90s`.
  The parent clones NET+MOUNT before mutations, mounts private 16 MiB `/var/lib`,
  and uses contained two-peer veth identity adapters, a controlled SOCKS exit, real
  sing-box/nft and independent sink capture with drop-accounted barriers.
  Coverage: TCP/UDP, normal DNS, persisted FakeIP, FakeIP resolving private, disabled
  and unreachable exit, unaffected client, unsupported ICMP/raw protocol rejection,
  pending clear and switching back. It is not an incoming encrypted WG handshake.
  Live execution remains pending until coordinated independently by the primary agent.

## Verification results before independent live checks

- Full `go test ./...` and `go vet ./...`: passed.
- Pinned sing-box 1.12.25 generated-config checks: passed for UDP/DoT/DoH.
- Existing nested `tests/dnsruntime` suite: passed, including actual pinned matcher
  checks for FQDN metadata with resolved private/loopback/link-local/mixed addresses.
- golangci-lint 2.12.2: native and Linux checks passed with zero issues.
- Static Linux amd64 daemon, CLI and opt-in test ELF: built.
- Synthetic browser suite: 7/7 passed on desktop/mobile, covering save, reload, Apply,
  explicit null, disabled targets, failed save and modal layout.
- Live isolated Linux fixture: pending primary-agent coordination; no live WG claim.
