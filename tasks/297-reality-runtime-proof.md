---
type: task
tracker: "#297"
title: Pinned REALITY authenticated-ingress capability proof
status: in-progress
---

# Pinned REALITY authenticated-ingress capability proof

Refs [#297](https://github.com/ArghTeam/razdacha/issues/297) and the design proposal
in [PR #298](https://github.com/ArghTeam/razdacha/pull/298). This branch adds a test
fixture only; the parallel ingress remains unimplemented in the product.

## Source and scope

Designated primary: `/Users/romanzhigalkin/Web/razdacha`; remote `origin`
(`git@github.com:ArghTeam/razdacha.git`), target `main`, freshly fetched base
`61afab058acbbf02febf8adf2eb225786d4bc433`. Feature branch:
`test/297-reality-runtime-proof`, isolated worktree:
`/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/test-297-reality-runtime-proof`.
Unrelated primary-checkout edits are outside this task.

The fixture executes the pinned sing-box 1.12.25 via an explicitly supplied binary.
It creates synthetic TLS/REALITY material and users, binds only loopback, and uses
local TCP/UDP sinks and DNS upstreams. No Internet service, installed configuration,
production host or real credentials are used. Staging execution belongs to the
primary operator after independent review; the implementation worker has no server
access. Operational hostnames, addresses, keys and raw evidence remain private.

## Acceptance and observations

| Capability | Required evidence |
|---|---|
| REALITY | Successful proxied flow with a local TLS 1.3 handshake target; plaintext VLESS and wrong public key cannot reach a data sink. |
| User identity | Two named users sharing one source address reach different sinks through `inbound` plus `auth_user` rules, over TCP and UDP. |
| DNS identity | TCP/UDP DNS hijack selects distinct controlled upstream answers for the two users. |
| Rejection | Unknown UUID and valid-but-unnamed user cannot exchange application data or obtain successful DNS answers. |
| Fallback controls | Direct sink and default DNS observer each pass an initial positive control, then receive no additional fixture requests. |
| Process lifecycle | An established, positively proven TCP flow closes within a deadline when the server runtime exits; child processes are reaped. |
| Isolation | Optional Linux netlink setup refuses the caller namespace and any topology containing more than loopback. |

Local validation with Go 1.25.8 passed:

- `go test -mod=readonly ./... -count=1` in `tests/dnsruntime`.
- Explicit `TestRealityIngressIdentity` run against a locally built pinned runtime:
  eight identity route/DNS subtests and four rejection subtests passed.
- The same explicit fixture passed under `-race`; the Darwin linker emitted an
  `LC_DYSYMTAB` warning, with no Go race report or test failure.
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -mod=readonly -c` produced a static
  ELF test executable for independent reviewed namespace execution.
- `git diff --check` passed.

Independent preexecution review approved the bounded fixture and supervisor. The
review found a nonblocking stream-framing weakness in the original TCP sink; both
requests and responses now use delimiters, with bounded TCP request reads. The
targeted race check passed again and the reviewer approved the corrected source.
These results do not authorize an exposed listener or product deployment.

The primary operator completed a fresh stopped staging snapshot, a separate
file-restoration rehearsal and SQLite integrity validation at schema 12. After
service restart, peer/rule/settings/tunnel/registration rows and daemon/runtime/
configuration bytes matched exactly; all three services were active and HTTPS
returned 200. Two earlier refused snapshot attempts were preserved privately.
This is file-level restoration evidence, not a live restore or functional rollback
proof, and it does not establish alternative-ingress readiness.

## Controlled Linux execution

On 2026-11-04, the primary operator ran frozen source `a526d8a` with static test
artifact SHA256 `af0437b9cbd1d435ec181f6d61ad489b457d929f87ae33213490cd19c3560d6d`
against the installed pinned 1.12.25 runtime. The first launch was refused before
the executable started because its initial temporary filesystem was mounted
`noexec`: zero subtests ran. Cleanup left no owned processes and the installed
runtime was unchanged. This was an infrastructure refusal, not a capability failure.

After the supervisor's execution-filesystem preflight was corrected and independently
reviewed, a fresh private network/mount namespace run passed all twelve subtests
and the top-level test in 11.71 seconds, with supervisor exit 0. The supervisor
verified that no owned processes/cgroup remained, the installed runtime was unchanged,
and the temporary fixture directory was removed. Every listener was local to the
disposable namespace. The fixture opened no public listener and changed no installed
application service or configuration. Raw output and supervisor/runtime identities
remain private. Final post-proof verification matched every snapshot peer, rule,
tunnel, setting and registration row and the daemon/runtime/configuration bytes
exactly. All three services were active; panel HTTPS returned 200 with valid TLS.
Both refused-attempt and successful-attempt temporary fixture directories were absent.
Independent final evidence review remains a PR-readiness gate.
The first local exploratory run failed because the synthetic TLS target used an
Ed25519 certificate incompatible with the chosen browser fingerprint. Using an
ECDSA P-256 certificate and X25519 key exchange corrected the fixture; no runtime
dependency or production source changed.

## Boundaries

This is handwritten runtime policy. It does not prove project-generated policy,
all-client/selected/native rule equivalence, full-exit failure isolation, DNS cache
or FakeIP behavior, IPv6/private destination guards, active credential revocation,
boot ordering, recovery, existing WG coexistence or browser/router compatibility.
Process shutdown is not proof of product revocation. No server-only fixture proves
the external client path or resistance to DPI. No current runtime invariant changes;
the maintained nested-module README documents the new test and its limitations.

Run commands and namespace requirements are in the
[runtime README](../tests/dnsruntime/README.md#authenticated-reality-capability-proof).
Merge, release and deployment remain separate from successful fixture execution.
