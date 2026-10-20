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

- Authorized repository: `ArghTeam/razdacha`; absolute checkout provenance is retained privately.
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
  The primary agent completed the isolated attempt recorded below.

## Verification results before independent live checks

- Full `go test ./...` and `go vet ./...`: passed.
- Pinned sing-box 1.12.25 generated-config checks: passed for UDP/DoT/DoH.
- Existing nested `tests/dnsruntime` suite: passed, including actual pinned matcher
  checks for FQDN metadata with resolved private/loopback/link-local/mixed addresses.
- golangci-lint 2.12.2: native and Linux checks passed with zero issues.
- Static Linux amd64 daemon, CLI and opt-in test ELF: built.
- Synthetic browser suite: 7/7 passed on desktop/mobile, covering save, reload, Apply,
  explicit null, disabled targets, failed save and modal layout.
- At this initial checkpoint, live isolated Linux verification was pending; subsequent
  attempts and their exact boundaries are recorded below.

## Isolated wire attempt 1: failed fixture negotiation

The primary-run attempt on `14e55a49ee7cecfadb35af98895541bf828cbc89` failed
before cached-FakeIP traffic completed. Three capture barriers reported zero drops;
this is failed evidence, not routing acceptance. The synthetic SOCKS relay assumed
all control requests used IPv4 addresses. The pinned SOCKS client sends an
IPv6-unspecified UDP ASSOCIATE bind hint for a loopback DNS destination, so the
fixture closed that valid request and reset DNS lookup. This bind hint is not an
IPv6 client packet or an expanded permitted destination.

The fixture now parses bounded IPv4/IPv6 control-address lengths. Actual UDP
payload destinations remain the original fixed IPv4 fixture allowlist. A portable
`net.Pipe` regression exercises the actual pinned SOCKS handshake for both public
and loopback destinations, plus malformed/truncated request refusal. Production
routing code is unchanged. The corrected fixture required a fresh independently
reviewed attempt, recorded below.

## Isolated wire attempt 2: passed

- Tested source: `3f971684f3b65c9e338589d38e56535dc27df2e0`.
- Static Linux amd64 test ELF SHA256:
  `5c6c5e535127a306537de5248582b22ce415620075992d3b6a1fea2335c530ac`.
- Result: outer exit 0, no skipped test, 4.91 seconds. The
  `231-PEER-EGRESS-WIRE-PASS` marker and 31 zero-drop capture barriers were observed.
- Covered real nft/TProxy runtime TCP/UDP, normal DNS, persisted FakeIP resolution,
  rejection of a FakeIP resolving to a private target, disabled/unreachable exits,
  unaffected second-source controls, ICMP and raw protocol 253 rejection with positive
  second-source captures, pending clear, and explicit return to shared routing.
- Configured bounds were 256 MiB memory, 192 MiB memory-high, 40% CPU, 128 tasks
  and 180 seconds maximum; no observed memory-peak claim is made.
  No installed application deployment or service restart was part of this fixture.

The actual test uses two veth source identities and a synthetic SOCKS exit inside
private network/mount namespaces. It does not establish an incoming encrypted
WireGuard handshake, an outgoing WireGuard/DO client flow, a universal no-leak
property or lossless observations beyond the acknowledged finite capture windows.
The new client mode has not been deployed to the installed staging application.

## Final browser scope

Independent synthetic-browser verification passed 11/11 cases on the tested source,
including desktop/mobile routing save/reload/Apply, explicit shared-mode null,
disabled targets, failed save and the targeted tunnel/rule/probe explanations.
It used controlled API fixtures and repository UI assets, not installed staging state.
Broader dashboard observations are separate follow-up work and do not expand this PR.

## Delivery state

PR [232](https://github.com/ArghTeam/razdacha/pull/232) contains the implementation,
ADR 0028 and maintained context. This completed implementation/validation record is
not a merge, release or deployment claim. Independent final evidence review and the
final documentation-only CI run are coordinated by the primary agent.
