# Staging security validation

Status: #222, #214 and #220 completed their controlled validation and were merged.
#220 merged through [PR #224](https://github.com/ArghTeam/razdacha/pull/224) at
`8b149e0`. Safe peer deletion #215 has two fixed passes and two expected original-base
broadening failures in [PR #225](https://github.com/ArghTeam/razdacha/pull/225).
Final #215 documentation/evidence review and current CI remain merge gates.
Tasks #216, #217 and #218 remain pending. Only linked evidence is complete.
No installed application or production deployment is claimed.

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
| 2 | [#214: coherent network state](../tasks/done/214-coherent-network-state.md) | List refresh and Apply publish coherent policy; failures cannot expose direct traffic | Validated and merged in PR #221: attempt 5 fixed PASS and four expected original-watcher failures; no product deployment |
| 3 | [#220: native rule-set coherence](../tasks/done/220-native-ruleset-coherence.md) | Native SRS/JSON DNS and destination predicates preserve semantics and refresh coherently | Validated on `d5cb9c1` and merged in PR #224: four fixed passes and four expected baseline failures; no product deployment |
| 4 | [#215: safe peer deletion](../tasks/done/215-safe-peer-deletion.md) | Removing the selected client never broadens its rule | Validated on `14e28b3`: two fixed passes and two positively proven baseline broadening failures; final docs/evidence/current CI before merge |
| 5 | [#216: clear UI secrets](../tasks/216-clear-ui-config-secrets.md) | Synthetic key/configuration disappears on modal close, replacement and logout | Planned, not executed |
| 6 | [#217: safe restore](../tasks/217-safe-cross-host-restore.md) | Synthetic restore preserves identity and starts outgoing WireGuard paused | Planned, not executed |
| 7 | [#218: isolated health](../tasks/218-isolated-tunnel-health.md) | DNS, transport and HTTP/TLS signals stay distinct; client rejects remain effective | Planned, not executed |

The first three unsuccessful #214 attempts remain failed: initial UDP echo, initial DNS with unknown response status, and initial DNS REFUSED (5). Core refresh/failure observations were not reached. #222 does not replace its four
original-watcher baseline cases. The prerequisite was merged before #214 was rebuilt
with its original watcher for canonical attempt 5.
The fourth #214 fixed attempt ended with top-level Go PASS but hid all child evidence
markers because verbose mode was not forwarded. It remains uncertified; all four
baseline cases were not run. A new reviewed execution with verbose forwarding
completed canonical attempt 5; the fourth attempt remains uncertified.
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
An accepted architecture decision does not itself prove validation, merge or deployment.


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
retains all earlier failed or uncertified attempts. Distinct final review and CI
passed before [PR #221](https://github.com/ArghTeam/razdacha/pull/221) merged at
`d5b8ad3`; the designated primary checkout was synchronized afterward.

This matrix exercises actual nft, route and sing-box behavior with a veth adapter for
WG address reconciliation. It does not exercise an encrypted WG handshake. Zero-drop
collector barriers cover a finite queued observation, not universal lossless capture.
Native SRS/JSON semantics and refresh are addressed separately by #220. No active application
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
WireGuard handshake. Canonical B4 completed all eight cases on source `d5cb9c1`
with 16 zero-drop collector barriers and distinct independent evidence acceptance.
One earlier attempt stopped when its supervisor rejected nested/outer Go
summaries after the first fixed case returned Go PASS; the remaining seven cases
were not run. That attempt remains uncertified. Prelaunch state-check stops do not
count as executed fixture cases.

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


The [accepted native implementation record](../tasks/done/220-native-ruleset-coherence.md)
contains the exact source/baseline pins and case-log hashes. All 11 downloaded results
matched the remote inventory. Snapshot-to-BEFORE changed only seven `checked_at`
and three `ok_at` health-timestamp cells; BEFORE-to-AFTER full and policy database
semantics were exactly equal, with no timestamp changes. Host/files, installed source,
services and policy identity were stable; four pinned payloads were removed and no
owned process/group or private case-directory process remained. These assertions do
not claim an application deployment or production change.

## Safe peer deletion controlled fixture (#215)

The [completed specification](../tasks/done/215-safe-peer-deletion.md) records the exact
four-case contract and case-log hashes. Fixed source `14e28b3` was compared with pristine
prerequisite `8b149e0` plus exactly three reviewed fixture overlays; source and binary
provenance were checked before the single controlled wrapper execution. Both fixed
cases passed; both baseline cases failed only after their explicit positive broadening
proof. No generic probe, capture or supervisor error was accepted as baseline evidence.

The grant case distinguishes direct delivery from a contained SOCKS5 relay by fresh
nonce-matched TCP/UDP sink delivery and stamped private reply identity. The reject case
requires valid selected-domain DNS REFUSED, protected-target TCP/UDP denial, simultaneous
same-client unrelated DNS/TCP/UDP success and inspected loss of generated source scope.
Every TCP, UDP and DNS probe binds and verifies its synthetic source identity.

Fixed cases retain B's direct behavior through deletion, list and pool refresh, committed
recovery, explicit Apply, applied recovery and disabled reassignment. Explicit B activation
is a positive control. Before Apply, the deleted A address remains reserved. After Apply
and applied recovery, new D reuses that address without inheriting the orphan's policy;
A's live database identity is not restored. A further refresh/recovery after D allocation
was not separately exercised. The adapter uses disposable veth source identities and
actual nft/route/sing-box behavior, not an encrypted WireGuard handshake.

Capture barrier counts were 49, 45, 12 and 8 for fixed grant, fixed reject, baseline grant
and baseline reject: 114 same-receiver nonce ACKs with zero reported kernel drops.
These bound finite queued observations; they do not prove universal lossless capture.
Both nested and outer Go summaries and the exact ordered proof markers were checked.
No installed daemon deployment, service restart or real provider credential activation
was part of the fixture. Current staging snapshots and private evidence remain retained.

All seven downloaded #215 results matched the checked remote size/SHA inventory;
private evidence manifest SHA-256:
`109d17c5cba48fe29c6d21fd8ed9e1b6fc019db04d83bcdd3b2d0f3788d0de7b`.
Snapshot-to-BEFORE and snapshot-to-AFTER changed only seven `checked_at` and three
`ok_at` health-timestamp cells. Immediate BEFORE-to-AFTER full database and policy
semantics were exactly equal, with zero timestamp changes. Host/file inventories,
installed source files, services and policy identity remained stable. Four pinned
uploads were removed; no owned process/group or private case-directory process remained.
Independent source/provenance/runner and actual evidence review passed. Synthetic
Playwright UI validation passed on the frozen UI hashes. Per-probe path checks are
fail-fast assertions in the pinned binaries; their individual raw transcripts are
not printed in the case logs. Final documentation review and current CI remain gates.


## Configuration UI lifetime fixture (#216)

Use `TestUISecretsFixture` in `internal/api/ui_secrets_fixture_test.go` only with
synthetic state. The fixture runs the actual API handler and embedded panel with
its own SQLite database, two deterministic test peers and one disabled synthetic
SOCKS tunnel. It does not call `Server.Run`, start schedulers or initialize netstack.
Its explicit endpoint allowlist rejects Apply, WARP, backups, logs, route probing,
domain probing and unrelated mutations. The configured runtime address is loopback
port zero, never an installed service. Do not replace fixture data with real keys.

For a local browser check, set `RAZDACHA_UI_SECRETS_FIXTURE=local` and run the API
test binary with `-test.run '^TestUISecretsFixture$' -test.v -test.timeout 9m`.
Read the ephemeral loopback address from `UI-SECRETS-FIXTURE-READY`. Authenticate
using the public fixture-only password `synthetic-ui-password-only`. The fixture
fails after eight minutes without an authorized graceful stop. Browser scripts remain optional verification
tools; Node is not part of the daemon build. Block the release-check request to
GitHub so the test performs no external browser request.

On staging, the root operator must first approve and pin source, test adapter,
binaries, supervisor and browser script. Use a pristine prerequisite archive plus
exactly the same three test adapter files for baseline; do not patch baseline UI assets.
Isolated mode requires Linux, `RAZDACHA_UI_SECRETS_FIXTURE=isolated` and an existing,
empty, owner-only `RAZDACHA_UI_FIXTURE_ROOT`. The tracked test-only bootstrap self-execs
with fresh network and mount namespaces. Its child proves both namespace identifiers
differ from the parent before making propagation private recursively and mounting
tmpfs at `/var/lib`. Parent-death protection and a parent-identity check precede those
mutations; parent and child retain the operator-owned process group. The child then
validates private propagation, tmpfs and the empty private root before SQLite or
socket creation. It exposes only a private Unix socket, mode 0600. The approved
operator can forward this socket to an ephemeral local loopback port. Never expose
another host TCP listener or invoke installed services. READY includes a private
`stop_token`; after verification, POST to `/__fixture/stop` with
`Authorization: Bearer <stop_token>` and require 204 plus child/parent Go PASS.
The control accepts only that path without a query and exact nonce authorization;
its token must never be placed in a URL, repository or public evidence. The fixture
waits for HTTP shutdown, closes SQLite and removes only its known owned database,
sidecars and socket, requiring its private root to be empty. Remove that empty root
with an empty-directory operation only. A deadline or generic process error cannot
count as successful verification. Retain exact process/group provenance; fallback
TERM/KILL/reaping targets only owned processes/groups and SSH forwarding.

The browser matrix must inspect state without another authenticated request after
closing by button, backdrop or Escape; replacing the modal; starting logout; failing
logout with 401; or receiving a current-session session/poll 401. Verify dataset,
text/form values,
QR canvas dimensions and owned download URLs. Hold actual synthetic config responses
across each transition, including success, failure and session expiry. Old request A
must not replace a newer valid request B. Deliver an old-session 401 after logout,
new login and config B; B must remain active, while a genuine current-session 401
still clears it. This stale-response case injects no `Set-Cookie` and proves only
frontend unauthorized-notification and rendering lifetime. The existing backend
invalid-session 401 expires the session cookie; delayed browser processing of that
cookie can still require another login. Backend cookie handling is unchanged.
This residual availability limitation does not expose configuration or bypass
authentication. Hold logout response completion, confirm button and Enter cannot
send a login request until completion, then verify a usable new login. Browser route
interception proves this request ordering; it does not by itself prove the timing
of browser cookie processing. Incorrect-password login must retain its normal
local error handling. A held successful peer creation must still
record dirty state after ordinary close/replacement while never opening its stale
configuration. A completed creation from a previous session must not repopulate UI
state or mark pending changes in a new session. Exercise tunnel raw loading, pending
input debounce, parse completion
ordering and reopening after cleanup. Check a normal active config download against
the actual fixture API content and filename. Baseline failures require explicit
retained-secret or stale-render observations; browser launch/network/test errors are
not evidence of the defect.

This lifetime removes persistent application references and revokes object URLs.
It cannot erase immutable strings from the JavaScript heap, browser-managed response
buffers, developer tools or files the operator chose to download. No deployment or
installed-server behavior claim follows from local fixture results. Independent
review and controlled staging evidence remain acceptance gates for #216.
