---
type: spec
tracker: "#217"
---
# Safe cross-host restore

## Goal and problem
A full backup carries outgoing credentials that may still be active elsewhere. A
cross-host restore must retain incoming identity, peers, rules and references while
preventing cloned outgoing WireGuard credentials from activating on first boot.

## Domains touched
- [Packaging](../context/packaging/index.md): restore, destination adaptation and rollback.
- [Store](../context/store/index.md): preserved identities and explicit activation state.
- [Singbox](../context/singbox/index.md): safe first generated configuration.
- [Netstack](../context/netstack/index.md): destination networking constraints.

## Decisions
Implement after [#216](done/216-clear-ui-config-secrets.md). Before code, write an ADR defining
cross-host restore and paused outgoing WireGuard first boot; preserve or explicitly
supersede [ADR 0016](../docs/decisions/0016-backup-full-and-encrypted.md).
Never contact, disable or rewrite the source. Activation requires an explicit destination
operator action. Keep unavailable-tunnel rules fail closed under
[ADR 0013](../docs/decisions/0013-unavailable-tunnel-rejects.md).

## Acceptance criteria
- [ ] Fixture restore preserves server/peer identity, rule IDs, conditions, scopes and references.
- [ ] Outgoing WireGuard tunnels remain paused through restore and first startup.
- [ ] Source hosts are never contacted or modified by restore or its validation.
- [ ] Failed validation leaves prior destination state recoverable from its pre-restore backup.
- [ ] Destination checklist covers hardware/interface differences, public endpoint,
      WAN/NAT mapping, panel exposure and certificate SANs before client cutover.
- [ ] Isolated synthetic restore/rollback checks prove activation and recovery behavior.
- [ ] Packaging, store, singbox and netstack context and backup/restore docs are updated.

## Validation
Planned; not executed by this planning change. Start with synthetic databases and disposable
instances/namespaces. Use independently generated fixture server/client credentials;
never enable cloned provider keys. Follow the [staging runbook](../docs/11-staging-validation.md)
for any smoke test, using current destination state for rollback, never the original
migration archive. Track evidence in
[issue #217](https://github.com/ArghTeam/razdacha/issues/217).

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

## Proposed contract for independent review

[ADR 0027](../docs/decisions/0027-safe-cross-host-restore.md) is proposed, not accepted
or implemented. It applies safe defaults to every restore, rejects unsupported service
topologies before mutation, preserves full incoming identity and policy, pauses all
effective WG variants using existing enabled flags and retains other protocol intent.
A metadata-only capability attestation prevents a new restore CLI from starting an
older installed daemon which ignores the trusted hold. No version-label inference.

The operation owns one installation lock and unique private operation storage. Durable
service inhibitors precede stopping and positively verifying both services. A coherent,
synced CURRENT destination recovery set precedes runtime quarantine and authority
invalidation. Journal-safe atomic DB publication precedes admin-only startup. Failure,
cancellation, uncertain systemd state or incomplete phase keeps boot/runtime inhibition
and client ingress protection; recovery is explicit and never automatically replays
credential-bearing config. The runtime inhibitor is removed only by an explicitly
prepared, checked and guarded Apply. Prior service/drop-in/hold states are recovery data.

A trusted hold outside the imported database suppresses automatic and manual external
work. Authenticated status and local edits remain available. A separate explicit Resume
external activity action audits destination settings and allows acquisition/schedules
before Apply; it cannot enable WG, publish policy or release ingress. Imported recipients
may receive scheduled encrypted backups after that explicit resume, which must be clear
in the UI. Ordinary PATCH, restart and Apply cannot clear the hold. Explicit enable plus
Apply is required for any chosen outgoing WG endpoint. A destination endpoint/WAN/NAT,
firewall, panel exposure and certificate SAN checklist precedes client cutover.

## Planned local proof matrix

No checks below have been executed for this design-only phase. Capture pinned baseline
and fixed revisions, observers and exact assertions; a generic command failure does not
prove forbidden activation or callback prevention.

| Cases | Required observations |
|---|---|
| Current-schema and older-schema fixtures | Exact server private/public identity, peer keys/IDs, rule IDs/order/conditions/scopes/enabled state and both references; compare older input against supported migration output |
| Ordinary WG, WARP second hop, unreferenced keepalive WG, raw parsed WG, disabled WG | Only effective WG enabled flags change; raw/parsed credentials and registrations remain byte-preserved; no endpoint starts |
| Non-WG protocols and pools, disabled peers, selected orphans | Saved non-WG intent preserved; scoped reject retained; no scope broadening or rule detachment |
| Wrong phrase, truncation, non-SQLite, future schema, bad foreign keys/JSON/type/source, duplicate protocol keys | Fail offline before service/runtime/authority changes; no imported URL contacted |
| Legacy/mutated daemon binary, packaged/custom unit, drop-in, alternate config paths, uncertain systemd state | Fail preflight before destination mutation; no legacy startup escapes hold |
| Two simultaneous restores; setup/uninstall overlap | Second lifecycle mutation rejected; unique private operation files unchanged |
| Stop/remaining-process/guard/snapshot/fsync/quarantine/rename/DB replacement/start errors and cancellation | Exact fail-closed phase; recovery set available before authority invalidation; no success report for uncertain state |
| Injected exit/restart at each durable boundary, including WAL cleanup and DB rename | Valid complete old or paused new DB; no stale journal replay, fallback seed or old runtime credential load; inhibitors survive reboot |
| Held startup/restart plus every scheduler and manual external route | Zero synthetic URL/recipient requests; local login/status/edit/download available; saves cannot trigger callback |
| Explicit resume success/failure/idempotence and restart | Persistence failure starts none; deliberate resume allows acquisition before Apply; no endpoint activation, implicit publication or ingress release |
| Apply with absent/unsupported native artifact | Visible guarded failure, no skipped native policy or old runtime replay |
| Paused Apply before/during/after scoped domain and CIDR probes | Client-scoped reject with nonselected/direct controls; selected orphan never becomes global matcher |
| Enable without Apply; background refresh/check/restart | No chosen WG handshake or pending-policy publication |
| Explicit synthetic WG enable plus Apply | Only chosen synthetic credentials reach the contained endpoint; positive observer control establishes actual handshake visibility |
| Explicit rollback/retry | Current destination coherent snapshot/authority/native dependencies/service intent/prior hold preserved; activation remains a distinct explicit recovery action |

## Isolated runner boundaries and gates

- Local tests use temporary roots and service adapters. No test uses host systemd sockets,
  real `systemctl`, installed app reloads, imported real URLs or actual provider material.
- Linux integration requires private network AND mount namespaces, private `/var/lib`
  tmpfs and test unit/config roots before executing fixture code. Use independently
  generated real WG keypairs, contained endpoints, HTTP/Telegram-like sinks, synthetic
  DNS and positive controls. Never discover or contact the excluded source host.
- The installed staging v0.10.0 app remains untouched; it is not a fixture. Stage the new
  binary only inside the isolated fixture. Test old-binary capability rejection there.
- Network containment, artifacts, commands, observers and rollback must pass a separate
  preflight and independent threat-model gate before any staging action. Root supplies
  the GO decision and fresh current destination backup where required. No host/SSH calls
  are authorized by this design-only phase.
- Final code/test/docs/evidence review is by a reviewer different from the threat reviewer
  and implementation author. No unresolved P0/P1 may be declared ready.

## Design-gate revisions (proposed; not implemented)

The independent review of frozen planning commit `322a8f1` requested changes for the
managed installer ownership gap. Revised ADR 0027 extends the same nonblocking lifecycle
lock over `packaging/install.sh` executable publication, setup and service start, with
a verified inherited file-descriptor handoff and no unlock/relock gap. Both staged
candidates are inspected through a harmless capability mode before publication whenever
ANY trusted restore record requires the protocol, including resumed/completed/recovery
state. Incompatible downgrade/update, unknown state and invalid handoff fail closed;
installed executable identity/hash is still checked before startup. No implicit upgrade.

Before migrations or WG pause writes, verify the complete supported SQLite schema-object
allowlist for the declared version. Reject triggers, views, virtual tables, unknown
objects and modified definitions without repair. After supported migration, pause and
reopen, verify schema identity and exact permitted row differences, including settings,
checks, sessions and raw credentials. This prevents imported executable schema behavior
from later undoing the pause. Integrity/version/count checks alone do not qualify.

The managed reset-network command must refuse guard removal for incomplete, held or
runtime-inhibited restore state, including resumed-before-Apply. Corrupt state also
refuses reset. Only specified guarded publication or serialized recovery transitions
may release restore safety state. Setup/update/uninstall cannot provide an escape hatch.

| Additional deterministic cases | Required result |
|---|---|
| Restore paused between final daemon attestation and ExecStart; installer attempts binary replacement | Installer fails to acquire the shared lock before either executable changes; no unsupported daemon starts |
| Installer owns lock across daemon/CLI rename, setup and start; competing restore/setup/uninstall | Competitor rejects before shared mutation; one owner throughout |
| Valid inherited lock FD, missing/wrong FD, wrong inode/permissions, independent FD and signals | Valid handoff has no gap/deadlock; invalid ownership rejects; no leaked long-lived child lock; cancellation retains safe inhibitors |
| Incompatible staged update/downgrade with incomplete, held, resumed, completed or recovery record | Harmless staged capability check rejects before installed binary publication; version labels cannot bypass |
| Candidate changes after attestation; first/second binary rename fails; setup/start fails | No unverified daemon or incompatible pair starts; failure visible, restore inhibitors and coherent recovery retained |
| Trigger on settings/check writes that would re-enable WG; view/virtual table/unknown object; altered known table/index | Reject schema before any migration/pause writes or live mutation; no silent dropping/repair |
| Valid older/current schemas through migrate/pause/reopen | Exact supported schema and only supported migration plus intended WG enabled differences; all other rows/bytes preserved |
| reset-network during partial restore, hold, resumed-but-inhibited and corrupt state | Guard and restore markers remain; visible refusal; no indirect publication/runtime activation |
| Resume then scheduled checks, source edits, list/pool refresh before first Apply | Acquisition allowed but zero runtime start/recovery, ingress release or implicit policy publication |

These are implementation obligations, not completed tests or review approval. The
revised frozen ADR must receive independent re-review before implementation starts.

## Guarded runtime publication phase (proposed clarification)

Removing the runtime marker to perform a checked restart is not completed activation.
After guard installation and checked config installation/sync, durably BeginPublishing;
only then remove the runtime inhibitor for controlled restart. The `publishing` phase
blocks daemon startup/reset and is protected restore state for installer capability
checks and recovery. Finish only after runtime/nft/authority/bootstrap publication is
coherent and durable; release client ingress last. Abort durably reinstalls the runtime
inhibitor before restoring a retryable complete-but-runtime-inhibited record. An Abort
failure or interrupted record/marker combination remains fail-closed and requires
explicit recovery. No pending WG enable is published by this phase.

Add injected failures/crashes immediately before/after BeginPublishing, marker removal,
runtime restart, committed authority save, bootstrap invalidation, Finish and ingress
release. Assert that missing markers never imply completion and reset cannot open
clients during an unfinished publication. This is a contract clarification pending
independent review, not completed implementation proof.
