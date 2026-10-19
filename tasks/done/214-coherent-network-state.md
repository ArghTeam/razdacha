---
type: spec
tracker: "#214"
---
# Coherent applied network state

## Goal and problem
A scheduled plain-list refresh must update sing-box matching and nft subnet sets from
one applied policy. Today, inline sing-box lists can remain stale while nft is updated;
nft also reads pending database edits that the operator has not applied.

## Domains touched
- [Lists](../../context/lists/index.md): list snapshots and refresh notification.
- [Netstack](../../context/netstack/index.md): subnet publication and daemon wiring.
- [Singbox](../../context/singbox/index.md): full configuration generation and apply gate.

## Decisions
First in the delivery sequence. Preserve [ADR 0013](../../docs/decisions/0013-unavailable-tunnel-rejects.md)
and existing pending-edit semantics. Document the chosen applied snapshot, serialized
publication boundary, and failure behavior before implementation. Add an ADR if an
accepted decision must change; this specification does not select a transaction design.

## Acceptance criteria
- [x] A deterministic regression reproduces stale inline list matching before the fix.
- [x] A refresh changes domain and subnet matching coherently in both routing layers.
- [x] Saved but unapplied rule/subnet edits remain unapplied during scheduled refresh.
- [x] Validation, sing-box apply, or nft failure retains the prior effective policy or
      fails closed without a direct-route interval.
- [x] Concurrent Apply and refresh cannot publish mixed generations.
- [x] Lists, netstack and singbox context, plus apply documentation, reflect the result.

## Validation
Planned; not executed by this planning change. Start with focused snapshot/failure tests,
then a controlled local HTTP plain-list source and disposable Linux network namespace.
Check both positive routing and forbidden direct traffic during failed/concurrent apply.
Follow the [staging runbook](../../docs/11-staging-validation.md) only after local checks pass.
Track delivery and evidence in [issue #214](https://github.com/ArghTeam/razdacha/issues/214).

## Security review gates
The implementation author cannot approve their own change. Before an isolated staging
canary, an independent reviewer must assess the local regression, threat/failure model
and proposed scope/rollback. Require two distinct reviewers, independent of the author and each other:
one reviews the threat/failure model; the other reviews the final diff, tests,
documentation and staging evidence. Two rereads by the same reviewer do not qualify.
Final acceptance requires baseline-fails/fix-passes evidence, adversarial negative
cases and controlled staging observations under the [runbook](../../docs/11-staging-validation.md).
No unresolved P0/P1 findings may be marked ready to merge. Unknown results stay explicit.
Review approval does not authorize merge or release. The pre-canary threat review and primary-owner review passed. The distinct final
diff/documentation/actual-evidence review remains pending on the final documentation commit.


## Implementation and local evidence (2026-10-19)

The chosen coordination contract is [ADR 0023](../../docs/decisions/0023-coherent-network-generation.md).
The API's manual Apply, plain-list refresh and pool refresh share immutable policy/list
content and one guard-protected publication gate. WG settings and address allocation are
part of this boundary; live key/peer revocation and authentication remain database-owned.
Fresh installation uses a distinct checked bootstrap seed; legacy and restore paths
without authority stay guarded until explicit Apply. Private records are owner-only and
restore invalidation is ordered and fsynced. A healthy background no-op skips mutation;
explicit unchanged Apply repairs network state without restarting a known runtime.

| Check | Observation |
|---|---|
| `go test ./cmd/razdachad -run '^TestPlainListsGenerationIsImmutable$' -count=1` on original base plus regression | Failed: a captured generation changed from old domain/subnet to refreshed contents |
| Same regression after snapshot implementation | Passed |
| Daemon publication unit matrix | Passed locally: ordered guard/WG/route/runtime/nft, validation/guard/WG/route/restart/nft/manifest/bootstrap/release faults, identical-byte retry, cancellation, restart, pending edits and concurrency |
| Peer identity/address matrix | Passed locally: deleted address remains reserved across restart, both allocation/Apply orderings, concurrent creates, legacy refusal, pending allocation mismatch, stored-peer/server/pool validation |
| Restore/bootstrap unit matrix | Passed locally: fresh orphan authority refusal, distinct seed/commit kinds, private permissions, interrupted invalidation ordering and fsync retry, guarded restore restart |
| Linux wire fixture | Canonical attempt 5 passed at `50aa62a`: four zero-drop capture ACKs and one matrix PASS marker |
| Archived original watcher baseline | Four expected desired-protection failures on `085cbf7`, using its unchanged original watcher and three shared test helpers |

The baseline-only adapter invokes the original `startNetfilter` and `watchLists` from an
archive of the unfixed base, using shared disposable namespace helpers. Run separate
`list-tcp`, `list-udp`, `list-dns` and `pending` cases so one expected red assertion does
not mask another. The fixed fixture uses production gate/netlink/nft and an isolated
sing-box process reloader; it never invokes host systemd. Full local verification,
controlled staging evidence and final independent acceptance must still be recorded.

Scope is plain-list content and the publication boundary. The autonomous remote
`.srs`/`.json` synchronization gap remains [#220](https://github.com/ArghTeam/razdacha/issues/220),
which precedes #215 in the delivery queue. Do not claim all-format coherence from #214.


### Review follow-up

The final reviewer identified a fixture evidence gap at TCP read boundaries. The sink
now retains a bounded per-connection marker tail and detects forbidden epoch substrings
across arbitrary fragmentation/coalescing. A portable regression exercises every marker
split and one-byte reads. The baseline archive also copies `network_payload_test.go`.
CI formatting and minor static-analysis findings were corrected without product changes.


### First isolated fixture observation

The first fixed-fixture run stopped at the initial positive UDP echo timeout, after a
positive TCP echo and before list refresh. Baseline cases were not executed. Cleanup
completed and the host structural state matched its pre-run snapshot. The wildcard UDP
sink did not pin reply source identity; the fixture now binds a listener per synthetic
alias and checks positive UDP echo for each before installing application policy. No
product defect or successful wire acceptance is inferred from that failed precondition.
The corrected fixture requires a new build, review and controlled execution.


### DNS fixture precondition observation

The corrected UDP sink passed all alias preconditions and initial TCP/UDP application
probes. The next initial DNS positive failed before refresh; the runtime reported a
missing default interface. Its pinned source pauses network operation when no default
interface exists. The shared disposable topology now supplies a default route ending
at its isolated sink, and the baseline adapter uses that same topology. A separately
bound local upstream must answer before runtime startup; probe failures report rcode
and answer count. No product DNS behavior or probe deadline was changed. Baseline cases
were not run, cleanup completed, and the host structural snapshot remained unchanged.
The new fixture revision still requires review/build and controlled execution.


### Wire matrix completion before the next run

The fixed fixture now uses a real temporary store. It saves a manual subnet change
without Apply, refreshes the plain list, and asserts that the old manual target remains
blocked while the new pending target remains direct. It then explicitly removes that
manual rule and proves the independent failure-control target is direct again. A second
plain-list generation keeps subnet intent stable, adds a domain, and queues the actual
`ApplyCurrent` entry point behind a paused watcher. Both successful publication
acknowledgments are required before stable postcondition probes. These are prepared
observations, not executed acceptance evidence. Existing established-flow, DNS, repair,
restart, failure and recovery checks are retained with unchanged per-probe deadlines.


### DNS prerequisite and observer follow-up

The third fixed run at `fe16166` passed local DNS upstream readiness, all four UDP
alias preconditions and initial TCP/UDP controls, then its first application DNS query
returned REFUSED (5), no answers and no transport error. All four original-watcher
baseline cases remained not run; cleanup restored the same host structural snapshot
and left no fixture process group. A private local reproduction using the actual pinned
runtime identified premature CIDR-only DNS reject matching. Prerequisite #222 corrected
that independent defect and merged as `085cbf7d4a5898615da343453755ab13f3bd4e7a`.
Its successful matrix does not replace any #214 wire observation.

The prerequisite is merged into this branch without rebasing. A new baseline archive
must use that exact main revision with its original watcher unchanged, copying only
the three documented test helpers. The observer now requires a fresh random UDP nonce
seen by the same AF_PACKET receiver, zero `PACKET_STATISTICS` drops since the preceding
barrier, and scanner consumption of the matching ACK before each evidence snapshot.
Receiver/statistics errors, output EOF/scanner failure and missing or mismatched ACKs
invalidate evidence. Raw payload markers are checked per TCP flow, including split
markers, and UDP packet; forwarded DNS and fresh guarded packets remain monitored.
The bounds cover packets queued before the barrier in this low-volume fixture, not
universal lossless capture or unobserved future traffic.

Baseline result classification requires positive controls, original-watcher publication
and a successful capture barrier before the explicit `214-BASELINE-LEAK-OBSERVED` failure.
A generic test error is not an expected baseline failure. The fixed matrix emits its
success marker only after all assertions and the last observer barrier pass. No new
wire execution or acceptance is claimed by these preparations.


### Fourth isolated attempt: result visibility not certified

The fourth actual fixed-fixture attempt at `16c9dc2` ended with top-level Go PASS
in 9.77 seconds. It did not expose the four capture-barrier markers or the final
matrix marker: the gateway self-exec omitted `-test.v`, so successful child `t.Log`
output was discarded. This is not certified wire acceptance. All four baseline cases
remained not run. Cleanup removed pinned artifacts and owned processes, and service,
file and host structural checks matched their pre-run state.

The gateway now forwards `testing.Verbose()` as `-test.v` while preserving the exact
selector and 80-second child timeout. Marker requirements and negative assertions are
unchanged. New compilation, review and controlled execution are required; no pass is
inferred from the hidden child logs. The three earlier failed precondition attempts
retain their recorded outcomes.


## Final controlled evidence (2026-10-20)

Canonical attempt 5 validated source `50aa62ae69cbb412f41949e37c6f79590732bb4a`
against original-watcher baseline `085cbf7d4a5898615da343453755ab13f3bd4e7a`.
The baseline product sources were not edited; only the documented three test helpers
were copied into the archive. Private launcher path repair before execution was an
audit action, not another wire attempt. The earlier three precondition failures and
attempt 4's uncertified Go PASS remain recorded above; their baseline cases were not
run at those times.

| Case | Required and observed result |
|---|---|
| Fixed matrix | Go PASS; exactly four `214-CAPTURE-BARRIER-PASS drops=0` markers followed by one `214-FIXED-WIRE-MATRIX-PASS` |
| Baseline `list-tcp` | Ordered positive controls, original-watcher publication, zero-drop capture ACK, then desired-protection leak marker and Go FAIL |
| Baseline `list-udp` | Same ordered gates, then desired-protection leak marker and Go FAIL |
| Baseline `list-dns` | Same ordered gates, then desired-protection leak marker and Go FAIL |
| Baseline `pending` | Same ordered gates, then desired-protection leak marker and Go FAIL |

Each baseline emitted exactly one ordered precondition/watcher/ACK/leak sequence.
No observer error, panic or runtime-error gate was present. The eight downloaded
result files matched their remote hashes. Cleanup removed all four uploaded payloads,
left no owned processes and preserved the pre-run service/file/host inventory.
Private addresses, raw logs and connection data are not published.

The fixed observation covers new plain-list domain/subnet matching, real saved-but-
unapplied edits, queued Apply behind refresh, established-flow guard epochs, DNS over
TCP/UDP, unchanged manual repair, resolver-address recovery, post-runtime nft failure
and guarded retry. It exercises production nft/route/sing-box paths and a disposable
veth WG-address adapter; it does not test an encrypted WireGuard handshake. Packet
absence is a finite zero-drop observation of packets queued before each collector
barrier, not a universal lossless-capture or future-traffic claim. Autonomous native
SRS/JSON predicates and refresh remain unresolved in #220. No application deployment
or production change occurred.

Full tests, relevant race suites, vet and static Linux amd64/arm64 builds passed at
`16c9dc2`; production code is unchanged at `50aa62a`. Focused tests, both fixture builds
and CI run `39333671624` passed at `50aa62a`. The pre-canary threat gate and owner review
passed. The final distinct reviewer must assess this documentation commit and actual
evidence before merge readiness; this record grants no merge or release authority.
