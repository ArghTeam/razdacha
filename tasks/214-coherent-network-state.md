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
- [Lists](../context/lists/index.md): list snapshots and refresh notification.
- [Netstack](../context/netstack/index.md): subnet publication and daemon wiring.
- [Singbox](../context/singbox/index.md): full configuration generation and apply gate.

## Decisions
First in the delivery sequence. Preserve [ADR 0013](../docs/decisions/0013-unavailable-tunnel-rejects.md)
and existing pending-edit semantics. Document the chosen applied snapshot, serialized
publication boundary, and failure behavior before implementation. Add an ADR if an
accepted decision must change; this specification does not select a transaction design.

## Acceptance criteria
- [ ] A deterministic regression reproduces stale inline list matching before the fix.
- [ ] A refresh changes domain and subnet matching coherently in both routing layers.
- [ ] Saved but unapplied rule/subnet edits remain unapplied during scheduled refresh.
- [ ] Validation, sing-box apply, or nft failure retains the prior effective policy or
      fails closed without a direct-route interval.
- [ ] Concurrent Apply and refresh cannot publish mixed generations.
- [ ] Lists, netstack and singbox context, plus apply documentation, reflect the result.

## Validation
Planned; not executed by this planning change. Start with focused snapshot/failure tests,
then a controlled local HTTP plain-list source and disposable Linux network namespace.
Check both positive routing and forbidden direct traffic during failed/concurrent apply.
Follow the [staging runbook](../docs/11-staging-validation.md) only after local checks pass.
Track delivery and evidence in [issue #214](https://github.com/ArghTeam/razdacha/issues/214).

## Security review gates
The implementation author cannot approve their own change. Before an isolated staging
canary, an independent reviewer must assess the local regression, threat/failure model
and proposed scope/rollback. Require two distinct reviewers, independent of the author and each other:
one reviews the threat/failure model; the other reviews the final diff, tests,
documentation and staging evidence. Two rereads by the same reviewer do not qualify.
Final acceptance requires baseline-fails/fix-passes evidence, adversarial negative
cases and controlled staging observations under the [runbook](../docs/11-staging-validation.md).
No unresolved P0/P1 findings may be marked ready to merge. Unknown results stay explicit.
Review approval does not authorize merge or release. These reviews are planned, not completed.


## Implementation and local evidence (2026-10-19)

The chosen coordination contract is [ADR 0023](../docs/decisions/0023-coherent-network-generation.md).
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
| Linux wire fixture | Compiled locally; execution pending independent threat gate and controlled staging |
| Archived original watcher baseline | Adapter prepared; compile/execution pending; no wire-level baseline result claimed |

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
