# Staging security validation

Status: #222 completed its isolated wire regression. #214 completed the controlled
attempt-5 matrix. Distinct final review and current CI are required before merge.
Later delivery tasks remain pending.
Only linked evidence is complete. No product or production deployment is claimed.

## Environment boundary and privacy

Only the new environment explicitly designated out of band as staging is authorized
for these checks. Production, including the source host, is excluded from every test,
installation, restart, monitoring, discovery and cleanup action. Do not discover hosts
or infer a target from a reachable address. Stop if the designated target is ambiguous.

Supply connection values through private environment configuration or an approved private
channel. Never commit or publish actual addresses, hostnames, SSH users, device/tunnel
names, keys, tokens, configurations or raw server output. Do not enable shell tracing
or echo secret environment values. Public evidence contains synthetic identifiers and
redacted results only. Keep private snapshots outside repositories with root-only access;
use encrypted transfer/storage if moving them outside the host.

## Ordered delivery

Complete local regression checks, review, controlled staging validation and cleanup for
one change before advancing to the next. These are sequential delivery dependencies.

| Order | Specification | Primary staging observation | Current status |
|---|---|---|---|
| 1 | [#222: domain-only DNS matching](../tasks/done/222-dns-rule-scope.md) | Unrelated DNS succeeds while matching domains/CIDRs remain protected | Validated: 8 expected baseline failures, 8 fixed passes; no product deployment |
| 2 | [#214: coherent network state](../tasks/done/214-coherent-network-state.md) | List refresh and Apply publish coherent policy; failures cannot expose direct traffic | Attempt 5: fixed matrix PASS and four expected original-watcher failures; distinct final review and current CI required before merge; no product deployment |
| 3 | [#220: native rule-set coherence](https://github.com/ArghTeam/razdacha/issues/220) | Native SRS/JSON DNS and destination predicates preserve semantics and refresh coherently | Planned, not executed |
| 4 | [#215: safe peer deletion](../tasks/215-safe-peer-deletion.md) | Removing the selected client never broadens its rule | Planned, not executed |
| 5 | [#216: clear UI secrets](../tasks/216-clear-ui-config-secrets.md) | Synthetic key/configuration disappears on modal close, replacement and logout | Planned, not executed |
| 6 | [#217: safe restore](../tasks/217-safe-cross-host-restore.md) | Synthetic restore preserves identity and starts outgoing WireGuard paused | Planned, not executed |
| 7 | [#218: isolated health](../tasks/218-isolated-tunnel-health.md) | DNS, transport and HTTP/TLS signals stay distinct; client rejects remain effective | Planned, not executed |

The first three unsuccessful #214 attempts remain failed: initial UDP echo, initial DNS with unknown response status, and initial DNS REFUSED (5). Core refresh/failure observations were not reached. #222 does not replace its four
original-watcher baseline cases. After merging the prerequisite, merge the updated
base into #214 without rebasing and rebuild that baseline with its original watcher.
The fourth #214 fixed attempt ended with top-level Go PASS but hid all child evidence
markers because verbose mode was not forwarded. It remains uncertified; all four
baseline cases were not run. Forwarding verbose mode changes visibility only and
requires a new reviewed execution with the same marker contract.
For #222, attempt 1 remains incomplete (one baseline failure, seven baseline cases
and all fixed cases not run); only attempt 2 completed the 8/8 matrix.

## Preconditions before each staging run

1. Reproduce the defect with deterministic local unit/browser/integration fixtures,
   then verify the fix and its negative/failure cases. Use appropriate Linux namespaces
   or disposable instances for networking and restore checks. Run required build/static
   checks and review maintained context before deployment.
2. Confirm the explicitly designated staging target out of band. Record privately its
   current version, binary/configuration identities, service state, effective flags,
   tunnel activation states, panel access mode and available CPU/RAM/disk headroom.
   Do not inspect or monitor production as part of validation.
3. Capture a consistent fresh snapshot of CURRENT staging database and relevant runtime
   configuration, binaries, service units/drop-ins, effective flags, certificates and
   network state. Use a supported consistent database backup mechanism rather than
   copying a live SQLite file without its journal/WAL. Restrict snapshots to root.
4. Establish a reliable recovery path before changing anything: retain administrative
   access, validate snapshot integrity/completeness and rehearse recovery in an isolated
   disposable environment. Account for version/schema compatibility and network access.
   If recovery cannot be demonstrated, stop the staging run.
5. Never use the original cross-host migration archive as rollback. It may reactivate
   credentials still used elsewhere. Preserve current staging enablement/paused states;
   never activate cloned real outgoing WireGuard credentials, even briefly.
6. Prepare disposable synthetic clients, rules and isolated WireGuard server/client
   fixtures, plus controlled local HTTP list, DNS and HTTP/TLS sources as needed.
   Contain their endpoints in disposable namespaces/instances; do not point fixtures
   at real provider endpoints. Do not use provider credentials as test fixtures.

## Controlled execution and evidence

Use low-volume fixture traffic and bounded timeouts. No external IP-echo services,
third-party test sites, production traffic, load tests or penetration tests are part
of this plan. Do not change actual client routing to make a diagnostic check pass.

For #214, observe selected and forbidden traffic paths while refreshing a fixture list,
saving an unapplied edit, injecting validation/publication failures and overlapping Apply.
For #215, use two disposable clients and an isolated private outbound or reject rule;
compare the unassigned client's behavior before and after deleting the assigned client.
For #216, use a generated fixture key, test download, all close paths, modal replacement
and normal/expired-session logout without a page reload. Complete delayed configuration
responses after close, replacement, logout and session expiry; none may repopulate keys.
Avoid capturing real configs.
For #217, restore synthetic archives only into disposable destinations and exercise first
boot, host adaptation, failed validation and rollback without contacting a source host.
For #218, run the DNS/transport/HTTP/TLS failure matrix while proving that fixture client
reject rules still reject. Label unavailable observations as unknown rather than healthy.

Record the tested commit/version, fixture case, expected/observed result, and rollback
outcome with all connection and secret values removed. Mark each case passed, failed,
blocked or not executed; do not replace evidence with an unsupported green status.
Keep raw traces, browser captures and configuration snapshots private and access restricted.

## Independent security review gates

The author cannot approve their own implementation. Use two gates:

1. Before deploying risky code even to an isolated staging canary, an independent reviewer
   checks the threat/failure model, reproducible local baseline-fails/fix-passes evidence,
   adversarial matrix, approved scope and recovery plan. Only controlled fixture testing
   may proceed after that gate.
2. After staging evidence is available, review the final diff, tests, maintained context,
   negative results and cleanup/recovery outcome before declaring merge/release readiness.

For #222, #214, #220 and #217, require two independent reviewer passes: a threat/failure-model review
and a final diff/tests/docs/evidence review, performed by two distinct reviewers
independent of the author and each other. Two rereads by one reviewer do not qualify. For #215,
#216 and #218, require a reviewer separate from the author plus primary owner review.
Re-review subsequent material changes. No unresolved P0/P1 finding may be marked ready to
merge; unknown or untested behavior is recorded explicitly. Approval grants no automatic
merge, release or production deployment authority.

Review failure, concurrency, rollback, restart/first boot, unapplied edits and stale
asynchronous responses where relevant. A green panel alone is insufficient: routing/leak
cases need wire-level observations at a controlled fixture sink proving forbidden direct
traffic is absent and pre-existing protections still hold. Keep captures private; publish
only redacted assertions. Avoid claiming that an unobserved path was tested.

The review evidence records redacted test commands, revision, expected/observed results,
review findings and disposition, approved scope, and a rollback summary. Never include
private host output or connection/credential values. These gates apply to each
implementation; completion is claimed only by its linked revision-specific evidence.

The #214 observer consumes a fresh random UDP barrier in the same AF_PACKET loop
that records forbidden packets, checks `PACKET_STATISTICS` at every barrier, and
requires the stdout scanner to consume its matching ACK before an evidence snapshot.
Any reported drop, receiver/statistics error, output EOF/scanner failure or missing ACK
invalidates the observation. These low-volume finite checks cover packets queued before
the barrier; they do not claim universal lossless capture or future traffic protection.
The rebuilt baseline uses prerequisite main `085cbf7d4a5898615da343453755ab13f3bd4e7a`
with its unchanged original watcher. All four baseline cases completed with the expected failures in canonical attempt 5.

## Cleanup and rollback

Remove disposable peers, rules, processes, namespaces and fixture artifacts. Restore
current pre-test user configuration and activation flags without undoing the reviewed fix
when validation succeeds. Verify configuration identity and service health locally on
staging. On failure, restore compatible binaries/database/configuration from the fresh
pre-test snapshot and recheck its effective flags, panel access and activation states.
Never change production to recover staging. If recovery fails, stop subsequent tasks and
report the blocker with redacted evidence; keep the recovery snapshot available privately.

## Documentation gate

Each implementation PR updates relevant maintained context and behavior documentation.
Write a new ADR before changing an accepted decision; historical ADRs are not rewritten.
This planning change does not alter runtime invariants or introduce an architecture decision.


## #214 isolated fixture scope

Cross-compile `cmd/razdachad` tests locally with `CGO_ENABLED=0 GOOS=linux` and the
staging architecture. Supply an installed sing-box binary using private environment
configuration. The opt-in `TestNetworkGenerationWire` requires `RAZDACHA_NETNS_TEST=1`
and `RAZDACHA_TEST_SINGBOX`; it self-executes in fresh network and mount namespaces,
verifies both identities differ from the parent, makes mounts private and overlays
`/var/lib` with disposable tmpfs before any network mutation. The fixture never invokes
systemd or connects to provider endpoints. A veth named wg0 exercises production ingress
classification; it is not a real WireGuard/provider credential test.

The local list, DNS and echo sources use synthetic addresses. Positive pre-guard echoes
prove direct and initially intercepted tproxy paths work. AF_PACKET sink observations
check forbidden guard-epoch TCP/UDP packets and direct DNS, including TCP SYNs; established
TCP/connected-UDP sockets carry distinct before/during/after epochs. A publication failure
after runtime restart must retain the real guard until recovery. The fixture also
changes a disposable veth resolver address under the guard and proves recovery makes
the committed resolver local before release, with no direct DNS packets. Explicit
unchanged Apply repairs deliberately removed fixture route/nft state without a runtime
restart. Runtime restart may
break established proxy connections; the acceptance condition is absence of forbidden
egress, not connection survival. A packet admitted before the guard acknowledgment may
already be in flight. Raw captures/output stay private; publish redacted assertions only.

TCP payload detection retains a bounded tail per connection and matches epoch markers
across fragmented/coalesced reads; it does not rely on TCP read boundaries.

Run an archived baseline with the original list watcher and shared fixture helpers to
show new selected traffic still reaches the direct sink before the fix. Baseline failure
and fixed success remain required. Compilation alone does not satisfy wire acceptance.

The baseline archive copies `network_fixture_linux_test.go`,
`network_payload_test.go` and `network_baseline_linux_test.go` into `cmd/razdachad`
and builds with `-tags security214_baseline`. The current fixed runner uses the default
build tags and `network_generation_linux_test.go`; do not copy that runner into baseline.

UDP echo listeners bind each synthetic destination address explicitly. Connected UDP
probes reject replies from a different source; a wildcard sink must not turn that fixture
error into a product failure. Every alias must pass a positive UDP echo precondition
before application policy is installed. Deadlines and negative assertions remain fixed.

The disposable gateway has a default WAN route only to its isolated fixture sink; no
namespace is connected to a host uplink. This supplies the default interface required
by sing-box's network monitor rather than changing production DNS behavior for a test.
The local UDP DNS socket is bound before serving and must pass its own positive query
before sing-box is started. DNS diagnostics distinguish transport failure, no response,
and response rcode/answer count. Probe deadlines and negative assertions are unchanged.

The fixed runner saves a real database subnet edit without Apply and observes both
sides of the boundary: the applied manual target still rejects, while the pending new
target remains direct. Only after these assertions does explicit Apply remove that
manual block and restore its direct control. A separate second list generation queues
`ApplyCurrent` behind a paused watcher, proves it cannot finish or open the guard,
then waits for two acknowledged publications before stable wire probes. Established
TCP/UDP observations and post-restart publication-failure controls remain in the matrix.


## Issue 214 accepted wire observation and limits

Canonical attempt 5 used fixed source `50aa62a` and original-watcher baseline
`085cbf7`. Fixed Go PASS included exactly four zero-drop capture ACKs and one final
matrix PASS marker. Each baseline case (`list-tcp`, `list-udp`, `list-dns`, `pending`)
emitted one ordered positive-control/watcher/zero-drop-ACK/desired-leak sequence and
Go FAIL. No observer errors, panics or runtime errors were reported. Downloaded result hashes
matched; all uploaded payloads and owned processes were removed, and pre-run service,
file and host inventories were unchanged. The [completed specification](../tasks/done/214-coherent-network-state.md)
retains all earlier failed or uncertified attempts. Distinct final review and current CI are required before merge.

This matrix exercises actual nft, route and sing-box behavior with a veth adapter for
WG address reconciliation. It does not exercise an encrypted WG handshake. Zero-drop
collector barriers cover a finite queued observation, not universal lossless capture.
Native SRS/JSON semantics and refresh remain unresolved in #220. No active application
or production deployment is part of this evidence.

## Native rule-set fixture and recovery contract (#220)

The native fixture is opt-in `TestNetworkGenerationWire`, with
`RAZDACHA_NETNS_TEST=1`, an absolute `RAZDACHA_TEST_SINGBOX`,
`RAZDACHA_WIRE_NATIVE_FORMAT=json|srs`, and
`RAZDACHA_WIRE_NATIVE_CASE=daemon-new-runtime-old|runtime-new-daemon-old`.
Run four fixed combinations (JSON first, then SRS; daemon-new first in each), then
four original-base combinations in the same order. Each test has an 80-second Go
limit, a 60-second internal context and a separately supervised 90-second limit.
Use the existing private exact-process supervisor; clear inherited wire-role values.
The gateway receives verbose mode. No fixture may use host systemctl or provider keys.

Baseline binaries archive the pristine prerequisite base and overlay only shared
fixture helpers, payload detector and the `security220_baseline` adapter. They use
the original remote generator and flattened daemon snapshot. A synthetic daemon-only
HTTP header separates the two consumers; explicit counters plus actual nft state
prove the selected skew before probes. Both forbidden TCP and UDP echo must succeed,
then the same raw collector must ACK a fresh nonce with zero kernel drops before
`220-BASELINE-NATIVE-LEAK-OBSERVED` is emitted. Generic failures, missing prerequisites,
partial leak proof or collector errors are never desired baseline failures.

Fixed runs assert zero autonomous runtime source requests, exact accepted-generation
protection, unrelated DNS success and selected-domain REFUSED, then forced committed
recovery. An unseen upstream body has no authority until the daemon accepts it.
`220-FIXED-NATIVE-WIRE-PASS` follows the final zero-drop capture barrier and absence
checks. The disposable veth exercises WG address control flow; it is not an encrypted
WireGuard handshake. Wire execution/evidence remains pending until separately reviewed.

On upgrade, a legacy v1 native authority cannot reopen an autonomous remote config.
The daemon retains committed WG settings for administrative access, keeps clients
guarded and exposes explicit Apply as the recovery path. Fetch supported sources,
inspect source errors, then explicitly Apply the intended policy through out-of-band
administrative access. A v2 missing/corrupt artifact keeps ingress guarded; never repair
or overwrite a referenced hash path. A newly accepted immutable generation or explicit
policy replacement can recover through the gate after the active failure is detected.
Retain artifact history for rollback; there is no garbage collection. At the native
256-MiB/4,096-file quota, new acceptance fails visibly and existing references remain.
Do not delete history until its active/prepared/recovery reachability is established.

Database backups exclude native blobs. Restore invalidates routing authority and
requires reacquisition and explicit Apply; it cannot replay absent references.
Safe cross-host restore remains #217. No staging, release or deployment claim follows
from local tests or a draft PR.
