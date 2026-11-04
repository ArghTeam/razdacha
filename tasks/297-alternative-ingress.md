---
type: task
tracker: "#297"
title: Parallel VLESS REALITY client ingress
status: design
---

# Parallel VLESS REALITY client ingress

Tracker: [#297](https://github.com/ArghTeam/razdacha/issues/297).
Architecture: [proposed ADR 0032](../docs/decisions/0032-parallel-authenticated-client-ingress.md).

## Scope

Propose optional VLESS + REALITY TCP ingress beside WG with the same client policy.
No feature or deployment is claimed. Outgoing additions, automatic switching,
reverse tunnels, arbitrary IP, ICMP and IPv6 support are excluded. Only designated
staging is in scope; production is excluded from discovery, monitoring and recovery.
Operational connection details, credentials and raw output remain private.

## Checkout and observed baseline

| Item | Value |
|---|---|
| Authorized repository / designated primary | `/Users/romanzhigalkin/Web/razdacha` |
| Remote | `origin`, `git@github.com:ArghTeam/razdacha.git` |
| Target | `main` |
| Freshly fetched base | `61afab058acbbf02febf8adf2eb225786d4bc433` |
| Design branch | `docs/297-alternative-ingress-design` |
| Isolated worktree | `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/docs-297-alternative-ingress-design` |
| Runtime dependency | sing-box `v1.12.25` from `go.mod` |

Preserve unrelated primary edits. Fetch again before new implementation branches.

At initial design capture on 2026-11-04, the designated staging HTTPS panel returned
200 with successful TLS verification, and `/api/version` required authentication
(401). That observation established HTTP reachability only; SSH inventory, backup
and runtime experiments had not yet been completed.

Subsequent authorized SSH inventory confirmed the installed sing-box 1.12.25. A fresh
stopped snapshot passed private file-restoration and standalone SQLite integrity
checks at schema 12. After restart and the experiment, all peer, rule, tunnel, setting
and registration rows and daemon/runtime/configuration bytes matched the snapshot
exactly. All three services were active, and panel HTTPS returned 200 with valid TLS.
This is file-level recovery evidence, not functional daemon rollback proof.

The independently reviewed [capability proof in PR #299](https://github.com/ArghTeam/razdacha/pull/299)
passed twelve subtests and the top-level REALITY test in an isolated Linux network
and mount namespace. Handwritten runtime rules preserved TCP/UDP route and DNS
identity for two users sharing a source address and rejected invalid credentials.
The experiment opened no public listener. Product-generated policy, private/IPv6
boundaries, guarded revocation, startup/recovery and WG coexistence remain unproven;
ADR 0032 remains proposed and the implementation stages below remain open.

Repository baseline evidence:

- `internal/singbox/generate.go`: `GenerateWithDiag`, `sourceCIDRs`, `routeRule`
  and `dnsRuleFor` compile peer-address scope into route/DNS policy; current inbounds
  are DNS and TProxy.
- `internal/singbox/peer_egress.go`: `peerEgress` uses source-address selectors for
  same-exit DNS, resolution, private-destination rejection and terminal exit routing.
- `internal/netstack/guard_linux.go`: `SetClientGuard` is `wg0`-scoped.
- `cmd/razdachad/network_generation.go`: serialized guarded publication and recovery.
- `internal/packaging/units.go`: the generated runtime can start independently;
  package-provided units are retained. Both installation paths need guard ordering.
- Pinned sing-box source: `protocol/vless/inbound.go` assigns authenticated user
  metadata for TCP and UDP; `option/rule.go` and `option/rule_dns.go` expose
  `auth_user`; `route/dns.go` passes metadata to DNS exchange. Source inspection
  is evidence of available hooks, not a passing end-to-end experiment.

## Delivery sequence and proof gates

1. **Inventory and recovery preparation.** Obtain confirmed staging access and host
   identity. Privately record application/build/runtime versions, architecture,
   service units/drop-ins, listeners, effective flags, policy state, disk/RAM and
   pending changes. Follow the [staging runbook](../docs/11-staging-validation.md)
   for a fresh consistent database/runtime backup and contained recovery rehearsal.
   Do not mutate staging before a reliable recovery path is demonstrated.
2. **Pinned-runtime proof.** In an isolated Linux fixture with disposable credentials,
   prove REALITY handshake, TCP and UDP proxy identity, route/DNS `auth_user`
   propagation, same-NAT client isolation, private/IPv6 rejection and active-session
   termination by guarded restart. Keep actual credentials out of logs. A failed
   capability proof revises the proposal before feature implementation. PR #299
   completes the bounded engine identity/handshake checks only; it does not complete
   the protection, guarded restart or product-integration checks in this gate.
3. **Authority and lifecycle foundation.** Implement schema/credential storage,
   committed admission/live-auth intersection, authority versioning, global and
   per-client enablement, serialized revocation, persistent endpoint guards and
   startup ordering. Validate both generated and stock package unit paths; no public
   listener is enabled before failure/recovery tests pass.
4. **Policy integration.** Implement shared source selectors, VLESS inbound, DNS
   hijack/same-exit resolution and explicit destination boundaries. Preserve WG
   interception and native artifact contracts. Run meaningful generator, store,
   lifecycle and contained Linux runtime tests.
5. **Profile/API/panel integration.** Add authenticated secret delivery, credential
   rotation/revocation and explicit pending/reconciling/error states. Keep the client
   editor coherent, preserve modal/session secret lifetime, and verify one selected
   device client and one router configuration with pinned versions.
6. **Review and controlled staging.** Review code, failure tests and maintained context
   before a candidate deployment. Deploy only to designated staging with disposable
   test clients, repeat the acceptance matrix, review evidence and verify cleanup.
   Capture redacted results and qualifications. Merge and release remain separate
   authorization steps.

Stages 3–5 should become bounded implementation tasks after the proof gate settles
the design. Choose listener address/port, REALITY handshake target/parameters and
the first client/router compatibility set from the inventory and proof results.
Do not assume nginx's TCP 443 is free or add shared-port multiplexing implicitly.

## Acceptance matrix

| Area | Required positive and negative evidence |
|---|---|
| Parallel operation | WG and VLESS clients work concurrently; unrelated WG profiles and policy remain valid. |
| Identity | Two VLESS users behind one NAT get different selected rules/exits; absent/unknown auth, duplicate credentials and spoofed source addresses never acquire another client's policy. |
| Rules | Selected/all-client domain and CIDR rules, priorities, chains, manual/plain/native sources and transport predicates agree across supported ingress paths; empty selected orphans never broaden. |
| DNS | TCP/UDP DNS retains auth scope, domain-only projections stay exact, CIDR-only lists add no match-all DNS action; same-exit/bootstrap DNS and stale FakeIP do not leak to direct resolution. |
| Boundaries | Direct and resolved private/loopback/link-local destinations, IPv6, protected host services and forbidden DNS transports are rejected; the explicit gateway DNS exception is narrow. |
| Unavailable exit | Missing, disabled, empty or failed target does not cause direct fallback; an independent sink verifies forbidden direct traffic is absent. |
| Credential lifecycle | Admission requires Apply; disable/delete/rotate closes new and existing TCP/UDP sessions after reconciliation; failed or uncertain reconciliation is visible and remains guarded. |
| Publication | Prepare failure preserves last-good service; each post-guard failure closes both paths. Test old/new endpoint union, port changes, already-established connections, retry, no-op repair and native artifact corruption. |
| Startup/recovery | Cold boot, independent runtime start, daemon/runtime crash, package unit drop-in, corrupt/missing authority and bootstrap cleanup failure cannot open an unguarded stale listener or revive credentials. |
| Migration/restore | WG-only records retain their contract; new authority rejects older readers; restore/rollback never activates cloned credentials or pending policy. |
| Delivery | Dedicated secret APIs require panel authentication; logs/ordinary responses omit secrets; modal close, replacement, logout and stale responses clear delivered profiles. |
| Network diversity | Controlled UDP denial leaves the TCP alternative usable while WG fails as expected; measure reconnects, throughput, delay and UDP application behavior separately. |

A local fixture proves bounded protocol/policy behavior, not resistance to every DPI.
An external client-network test must record the tested path, client version, time
and observed symptoms privately; server-only reachability cannot establish that path.
IP-address blocking, actual DPI classification, arbitrary WebRTC success and universal
performance claims require separate evidence. No successful external test is claimed.

For this docs-only PR check links, symbols and `git diff --check`; Go behavior is
unchanged. Review identity, DNS and startup boundaries independently. The context
index links the proposal without changing current invariants. Implementation PRs
must update affected layer context/invariants with final behavior and evidence limits.


## Implementation contract

The application implementation adds independent WG/REALITY transport admission,
private live credentials and a version-5 policy authority. Server keys and UUIDs
are not stored in policy generations. Every publication resolves committed
admission against current live credentials under the existing gate. Public ingress
is guarded across checked restart, durable authority and startup. Existing outgoing
tunnel definitions, keys and enablement are outside the change/test scope.

## Backend implementation and verification boundary

Implementation branch `feat/297-reality-ingress` starts from verified
`origin/main` `fc0965eb0a9cf9ef9ebf5b9bd0f718536559fc3a` in
`/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/feat-297-reality-ingress`.
The designated primary remains `/Users/romanzhigalkin/Web/razdacha`; remote is
`origin`, target is `main`. Foundation helpers were consumed through merge commits;
no rebases or primary synchronization were performed by the backend worker.

Implemented: additive private credentials and transport flags; v5 committed
admission; authenticated no-store URI/full JSON delivery; dedicated REALITY inbound
and authenticated source policy; DNS/whole-exit/native projection preservation;
IPv6/private/gateway rejection; union endpoint manifest/guard publication and
pre-start hook; serialized active-flow revocation through checked restart; restore
identity reset and uninstall refusal. Existing outgoing tunnel records/selection,
WG keys and addresses are not rewritten.

Verification includes ordinary Go tests, credential history/restore/API privacy,
guard/restart/recovery/port collision failures, and a generated-config Linux fixture
using only synthetic owned TLS/DNS/SOCKS/echo services in a fresh network namespace.
The fixture covers two same-source users with TCP/UDP/native-selected/shared/DNS,
private/resolved/mapped destinations, checked client profile, healthy and unavailable
whole exits, cached FakeIP, session closure and stale credentials after restart.
Runtime execution, independent review and staging evidence must be recorded before
claiming those checks passed or broad application compatibility. The earlier PR299
capability proof remains a separate historical handwritten-policy result.

### Generated product proof (2026-11-04)

The coordinator ran the independently reviewed frozen generated-product fixture in
an isolated Linux network/mount namespace under a 384 MiB memory limit. It passed
in 24.76 seconds using sing-box 1.12.25. Installed daemon/runtime/config hashes and
existing service process identities remained unchanged; no provider was contacted.
Frozen test binary SHA256:
`e5e6abc2efd442186177840a8efe869c18c6d7c032d9553be9361f1b8a87d2f0`;
frozen product fixture source SHA256:
`1b99cb16691a97645b675717d3acd87213b2d442bdbe013b9ee8403035a560f5`.
This proves generated server policy with isolated proxy wrapper clients and a
checked complete JSON profile. Running the delivered TUN profile itself is a
separate namespace proof; it is not implied by this pass. Later fixture additions
add unavailable-exit DNS assertions and bounded per-client cleanup and need their
own execution record.
