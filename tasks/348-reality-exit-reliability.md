# Selected REALITY exit failure and recovery (#348)

Status: In progress; no current live outage acceptance is claimed by this specification.

## Goal and scope

Refocus [roadmap #338](338-client-connectivity-roadmap.md) on stable Internet access
for family and friends. Subscriptions, billing, tariffs, quotas and commercial
access distribution are excluded. Preserve completed milestones and historical
ADRs/tasks. Approved reserve exits and recovery are future work, not implicit
Direct fallback (ADR 0013/0028).

The bounded first task tests the existing RAC REALITY clients and applied exits
without changing profiles, clients, rules, credentials, application configuration,
committed authority, DO services or production. The running candidate is RC16,
source `a945231fbf78659c100df5049b5dc77f6a636839`. Exact runtime and protected-state
hashes must be checked immediately before and after the experiment.

## Intended fixture

A separate opt-in Linux helper uses `google/nftables`, never `nft`, `ip` or `wg`
subprocesses. It can affect only RAC `193.233.112.227` and the literal outgoing
TCP endpoints `165.22.196.130:443` or `165.22.196.130:2443`, one at a time. This
is an EXIT outage: other RAC rules sharing that endpoint are temporarily affected.
It is not a per-client firewall and not a failure of WARP inside DO. Both exits
share one DO host, so they do not provide independent-host redundancy.

`prepare` creates a fresh private run journal with a 128-bit run ID and an
absolute deadline no more than 90 seconds away, boot identity and network-namespace
device/inode identity. All observation/revocation requires that same prepared
namespace, even if its RAC interface address later disappears. The controller arms a separate
systemd watchdog timer/service before starting the fault unit. `hold` verifies
its own unit identity and the watchdog's active timer, triggered service, command
and bounded deadline. It then atomically adds one unpredictable owned inet table
with one early OUTPUT chain and exact IPv4/TCP destination counter/DROP rule.
The immutable deadline is checked immediately before and after installation.

Foreground timer/signal cleanup and a bounded fault-unit lifetime complement the
independent watchdog. Cleanup records durable revocation under the same private
file lock before deleting the exact verified table. Persistence or unit-stop
errors still attempt exact owned removal and remain explicit failures; address
loss cannot disable revocation. A delayed installer cannot
add a rule after revocation. The watchdog stops only its deterministic owned fault
unit and retries exact cleanup; it never stops application services. Table absence
and stopped fault unit must be established before canceling the watchdog. Foreign
or replaced tables are never deleted. No application routing or admission changes.

## Acceptance evidence

- Inventory current consumers of each exact outer endpoint. Announce the exit-wide
  sandbox impact and retain a functioning other-exit control.
- Use current exports of the two existing owned test REALITY clients with only
  loopback mixed inbounds substituted. No TUN, host route/DNS change, other user
  WG keys, old cloned production credentials or DO-side operations.
- Compare healthy baseline, bounded outage and recovery for new TCP/HTTPS and
  small UDP STUN requests. Keep one existing TLS socket and UDP association across
  the outage. Generate fresh nonces/transaction IDs after the fault barrier;
  buffered pre-barrier bytes are not a new response. Record retained-flow outcomes
  without promising uninterrupted survival.
- Require negative request results, positive DROP counter hits and an independent
  zero-drop finite WAN capture oracle: no direct target SYN/UDP or owned plaintext
  marker. Positive direct controls must not contaminate the negative interval.
  DNS observations are separate and must reflect actual applied/shared DNS policy;
  lack of one packet is not proof of all-DNS privacy.
- Fresh requests recover after removing the fault, without Apply, restart or
  client/policy changes. Compare protected user rows, config/authority hashes,
  runtime PIDs and released ingress guard before/after. Remove only owned profiles,
  sockets, client processes, captures, temporary session and fault resources.
- Run helper safety/lifecycle/ownership tests, relevant Go/vet checks and independent
  source/operations review before host mutation. Pin the helper source/binary hash.
  Record failed attempts honestly. Source readiness is not live acceptance.

No automatic failover is implemented. The results inform a separate reviewed
proposal for explicitly approved reserve exits; health observations never grant
permission to release selected traffic through Direct.

## Source verification before live use

The helper has 25 Linux safety tests, including exclusive netlink batch flags,
exact schema/counter ownership, foreign replacement refusal, persistent journal
failure cleanup, expired/stalled installers, concurrent cleanup, monotonic timer
bounds, future watchdog credentials, actual executable identity and same-namespace
revocation. Portable tests and focused race checks pass; the compiled Linux test
binary passes in a networkless local container. These tests do not install host
kernel rules. Complete CGO-disabled Go tests and vet pass; the project-pinned
Linux linter reports zero issues for the final helper after applying the pinned
formatters. Application runtime code and dependencies are unchanged.

The primary agent must still attest the pushed source/binary, perform the isolated
kernel roundtrip on RAC and independently review bounded operations before any
live fault. This record does not claim a current exit outage or recovery result.
