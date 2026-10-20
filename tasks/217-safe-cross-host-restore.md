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
Local implementation and bounded independent corrective reviews are in progress. Use synthetic databases and disposable
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
Review approval does not authorize merge or release. Threat-design and bounded corrective code reviews are recorded below; final integrated
acceptance remains pending.

## Approved design contract

[ADR 0027](../docs/decisions/0027-safe-cross-host-restore.md) has an independently approved
design; its implementation is under final review. It applies safe defaults to every restore, rejects unsupported service
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

## Required proof matrix

The local evidence record below identifies completed checks and remaining proof. Capture pinned baseline
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

## Independent runtime start and Abort clarification (proposed)

The publishing-phase proposal additionally requires a permanent managed runtime
ExecCondition calling the metadata-only CLI runtime-start check. With marker absent,
publishing still denies uncontrolled starts: only a private permit bound to exact config
SHA-256, operation/publication nonce, current boot ID, live daemon PID/start identity
and at most 60 seconds of monotonic boot uptime allows the controlled restart. Begin
persists that candidate identity before permit creation/marker removal. No SQLite or
network access occurs in this independent service condition.

After positive candidate readiness and coherent nft/authority/bootstrap publication,
Finish durably revokes the permit and records safe completion before last ingress
release. Failed Finish remains protected. After durable Finish, guard-release failure
stays a guarded error without fabricating a stopped/held runtime. Abort before Finish
durably inhibits starts and revokes permit, positively stops/verifies the live candidate,
keeps the guard and only then records retryable inhibited state. Any uncertainty requires
explicit recovery. Test ordinary daemon-first and runtime-first reboot order, stale boot
ID, dead/reused PID, expired permit, wrong candidate hash/nonce, helper failure, candidate
readiness failure, Abort stop failure and every record/permit/marker fsync boundary.

This is a docs-first clarification for independent threat review; production phase
integration remains uncommitted and unvalidated at this checkpoint.

The permit deadline must be a cross-process kernel CLOCK_BOOTTIME/CLOCK_MONOTONIC value,
not wall time or serialized Go time monotonic metadata. Every state/permit safety field
is mandatory exactly once, with strict types and null/duplicate/unknown rejection;
private ownership, canonical config and no-symlink checks apply. Capable binaries with
older units missing the permanent independent runtime condition fail restore preflight;
compatible managed setup is a separate prerequisite. A live checked runtime can survive
authorizing-daemon failure: permit revocation/expiry denies future starts, while the
ADR 0023 ingress guard contains uncertain publication. Only positive stop verification
supports an inhibited/stopped claim. Test these distinctions and enforcement absence.


## Recovery implementation contract clarification

Passive `restore-recover` records `recovering` before mutation and `recovered` only
after the complete current-destination file set is restored. Both phases stay held,
transaction/runtime inhibited and guarded; repeated passive recovery never starts a
service. Separate `-activate-destination` requires `recovered`, unchanged verified DB,
original private metadata and explicit destination-credential acknowledgment. It
preserves original callback hold and service enable/active intent, leaves runtime
inhibited for explicit Apply, and sets strict required-once `recovery_activated` so only
recovered destination WG intent may survive that Apply. Imported WG stays paused.
Null/missing/duplicate fields, incomplete rollback and byte equality without a complete
passive phase grant no authority. An original destination without a DB remains safely
inhibited with an explicit fresh-install recovery limitation. Local synthetic recovery
checks cover passive retry, premature activation, prior hold/active intent, failed stop,
changed recovered DB and symlink parents; isolated runtime/staging proof remains pending.


## Local integration evidence and remaining gates

- Independent foundation review closed strict JSON omission/duplicate/null and inherited
  original-FD CLOEXEC defects at `428186c8`.
- Independent backend review closed canonical-reset aliasing, canonical CLI helper
  attestation, Resume/Apply lock inversion and orphan publishing permit defects at
  `afcb31d6`; unchanged private regressions retained old-fail/new-pass evidence.
- Independent recovery review closed intermediate native-parent traversal at `1417456`,
  including an actual rollback sibling-sentinel old-fail/new-pass negative.
- Local authored tests exercise exact identity/schema/WG classification, held API/scheduler
  gating, current saved list/pool/backup/notification destinations, duplicate/concurrent
  Resume, aborted-candidate startup, passive recovery and activation failure/retry.
- Real child-exec tests cover shell-style inherited descriptors, unrelated descendant
  CLOEXEC, lease retention and signal release. Real installer functions execute with
  only private canonical-path/owner overlays, synthetic archives/download and equivalent
  real flock; incompatible retained-evidence downgrade, publication/setup contention and
  SIGTERM stop/reap are checked. No host systemd or live download is used.
- Replacement fault adapters delegate real operations except one injected file fsync,
  source/destination directory fsync or rename failure. The fixture asserts actual old
  versus new complete DB and current-destination rollback; injected failure is not a
  physical disk failure. Ten real restore child SIGKILL cuts verify restrictive state
  and explicit recovery without Go defers; no physical power-loss/reboot claim.
- Local headless browser tests use actual synthetic localhost API and Secure/HttpOnly
  cookies, both Resume acknowledgments, unknown-status denial and mobile layout. All
  browser external requests are intercepted: the resumed release-check positive proves
  frontend request intent, not external delivery or whole-daemon zero egress. Exact
  frozen source/artifact/script hashes are retained in the private execution report.

Final full diff/security review, the separate #228 contained true-WireGuard baseline/fix
fixture, its reviewed runner/artifact/isolation gate, staging observations and final CI
are still required. No namespace/staging/SSH/installed-service operation, merge or release
is authorized by these local results.
