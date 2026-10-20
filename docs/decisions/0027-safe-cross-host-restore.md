# 0027. Restore with paused credentials and explicit destination activation

Date: 2026-10-20 · Status: proposed; implementation and independent review pending

Partially supersedes [ADR 0016](0016-backup-full-and-encrypted.md): restoration
preserves incoming identity, but does not automatically restore outgoing activation
or promise unchanged client connectivity after moving hosts. Complete secret-bearing
backups and encryption before external delivery remain required. ADRs 0013 and
0023–0026 remain authoritative for guarded publication, rejection and selected scope.

## Context

A database backup can clone credentials still active on another host, source URLs,
notification destinations and scheduled backup recipients. Pausing database tunnels
and guarding wg0 does not stop independent sing-box endpoints or daemon callbacks.
DB-only backups omit native artifacts, so prohibiting acquisition until successful
Apply would prevent recovery. Current destination state, rather than the imported
archive, must be the rollback authority.

## Proposed decision

### Scope and preserved data

Every restore uses this contract, including apparent same-host restores. No hostname,
address or key comparison grants an exception. Restore never discovers, probes,
contacts, disables or changes the source host. Validation is offline; no imported
URL, provider key or notification recipient is used during validation.

Before running any migration or pause write, inspect the private staged SQLite file
read-only with extension loading disabled and untrusted schema handling. Compare its
complete schema-object set against an allowlist derived from the supported migrations
for its declared schema version, including expected table definitions, constraints,
columns, foreign keys and indexes and only expected SQLite internal objects. Reject
unsupported triggers, views, virtual tables, unknown objects and modified definitions;
never silently drop or repair them. A plausible `user_version`, `integrity_check` and
foreign-key checks alone are insufficient: a trigger on later settings/check writes
could undo the WG pause. After this admission, migrate using existing migrations,
validate references/metadata, pause effective WG and close/reopen the staged DB. Verify
schema identity again and exact permitted row differences: supported migration output
plus only the intended WG enabled-flag changes, including no changes to settings,
checks, sessions or credential bytes beyond supported migrations. Preserve incoming
server identity, every peer key, IDs, rule order, enabled intent, destinations,
conditions, source scopes and both tunnel references. Preserve tunnel raw/parsed
credentials, pool ordering and WARP registration material. The only intentional
activation rewrite is `Enabled=false` for every effective outgoing WireGuard tunnel,
including unreferenced tunnels, WARP, ordinary WireGuard and raw JSON whose effective
`Parsed.type` is `wireguard`. Already-disabled tunnels stay disabled. Classification
follows generator precedence, including pool handling, rather than inspecting only
the declared type. Malformed or ambiguous metadata, invalid JSON (including duplicate
protocol keys), inconsistent WARP/type/source combinations and unsupported pool
endpoints reject the archive before live mutation, even when disabled. Non-WireGuard
activation intent stays unchanged but cannot run before explicit publication.
Existing supported migrations remain authoritative; do not silently repair rules,
detach references or broaden selected scopes as part of restore. Preservation tests
compare current-schema fixtures exactly and older fixtures against migration output.

### Supported installation and ownership

Initially support only the canonical managed daemon and sing-box units and their
single known local config path. Preflight compares effective systemd execution,
fragment and drop-in properties with supported managed templates, resolves canonical
paths and verifies ownership/permissions. Reject packaged/custom units, unknown
drop-ins, additional config directories/files, wrappers, aliases, symlinks, alternate
DB/config paths and unknown service-state query results before mutation. Never guess
that a failed state query means inactive. This explicit compatibility restriction can
be widened only with equally strong quarantine proof.

Before installing inhibitors or changing services, attest the installed daemon's exact
executable using a new metadata-only `-restore-capabilities` mode. It reports the
supported restore protocol and hold/inhibitor/API contracts without opening the DB,
initializing networking or starting callbacks. Older binaries reject the unknown flag;
that rejection is a preflight failure, never an invitation to start them normally.
A release label or `dev` string is not capability proof. Verify a trusted regular
executable, bind the attestation to its file identity/content hash, and recheck before
allowing daemon startup; changed/missing binaries retain the boot inhibitor. A new CLI
against an older installed daemon must fail before mutation and instruct the operator
to install compatible binaries first. No implicit daemon upgrade is part of restore.

Take one nonblocking installation-wide lifecycle lock before shared mutation. Managed
`packaging/install.sh` install/update, setup, uninstall, restore and restore recovery
all honor the same stable private lock inode. The shell installer must acquire it
**before publishing either installed executable** and retain ownership through both
binary replacements, setup and service start/verification; taking it only in setup
leaves a capability-check-to-ExecStart race. Downloads/extraction may be private staging
outside the lock, but no installed binary or shared install state changes there.

Under that lock, inspect ANY trusted restore state, including incomplete, held,
resumed/runtime-inhibited, completed and recovery records. Before executable replacement,
run the staged daemon and CLI's harmless metadata-only capability mode and bind results
to their verified trusted regular files/content hashes. Reject an incompatible update
or downgrade before publication whenever that state requires the restore protocol;
unknown/corrupt state is an error. Retain the installed-daemon attestation and final
identity/hash check before startup as well. Matching version labels are insufficient.
Binary-publication interruption preserves inhibitors and cannot report success; setup
must never start an unverified daemon or continue with an incompatible binary pair. All managed candidate publication uses
the lock, closing the legitimate installer/restore replacement race.

Use an inherited exclusive-lock file descriptor for the shell-to-CLI handoff, not an
environment boolean or unlocking/relocking gap. Setup validates the inherited descriptor
against the canonical lock's inode, owner, mode and regular-file/no-symlink requirements,
and successfully requests the same nonblocking exclusive lock before accepting it.
The descriptor retains the same kernel open-file description; CLI callers without a
valid inherited descriptor acquire the lock normally or fail. Keep an owner alive
until setup/start finishes; propagate failures/signals and wait for the child before
normal release. Mark the descriptor close-on-exec for unrelated child commands so no
long-lived service inherits lifecycle ownership. Never unlink/replace a locked file.
An invalid handoff fails before publication/setup, and a second independent owner is
rejected rather than waiting or deadlocking itself. This is a bounded managed installer
protocol, not authorization for arbitrary historical installer scripts or direct root
binary replacement to bypass restore safety.

A competing restore fails without touching the first operation. Each operation uses a unique 0700 staging/recovery directory and
0600 files on the destination filesystem. No shared `.restore` filename or deletion
of another operation's files is allowed. Root/host compromise and unmanaged processes
that bypass managed unit/lifecycle controls are outside this contract.

### Durable recovery and failure ordering

The transaction is a small, versioned restore record, not a general network-generation
framework. Write/sync each private file before publishing its name; sync every affected
parent after rename, create or removal, including service drop-in directories.
Unsupported filesystems or failed synchronization are errors. Test the following order:

1. Lock, decrypt, stage, admit the exact supported schema before writes, migrate,
   classify/pause, reopen and verify exact permitted differences offline. Inspect effective
   units/config paths and destination adaptation answers. Record initial active/enabled
   unit state, original inhibitor/drop-in contents and any existing trusted restore hold
   before mutation. Durably save this initial metadata in private recovery storage
   marked incomplete, then prepare/validate the unit inhibitor representation. This
   partial record cannot authorize DB/authority mutation; it supports explicit recovery
   if inhibitor installation is interrupted. Recheck attested paths under the lock.
2. Durably install start inhibitors for **both** services using managed systemd
   `ConditionPathExists` drop-ins and durable inhibitor marker files; reload systemd
   and verify the effective inhibitors. The daemon inhibitor covers an incomplete
   transaction; the runtime inhibitor persists until safe explicit Apply. Never
   overwrite unit fragments or use a volatile `/run`-only stop/mask. Install the
   daemon inhibitor first so it cannot race a runtime stop by restarting runtime.
   A crash here has not published imported state; recovery remains explicit.
3. Stop and positively verify both daemon and sing-box quiescent, including absence of
   remaining unit processes. Install/verify the client ingress guard. Drain/close old
   DB writers. Create a consistent CURRENT destination DB snapshot including committed
   WAL data; validate it offline and sync it. Preserve runtime config, committed and
   bootstrap authority, reachable native artifacts, original service state, original inhibitor/drop-in contents and prior
   hold in the same recovery set. Artifact history may remain in place only if verified
   durable and retained against deletion. Sync the recovery manifest and directory;
   only this complete set authorizes subsequent destructive steps. On a fresh target,
   record verified absence explicitly. Earlier failures retain original DB/authority.
4. Durably create the trusted callback hold; quarantine **all** effective runtime config
   outside every loaded config directory, preserving it in the private recovery set.
   Verify no endpoint-bearing config remains loadable. Invalidate bootstrap authority
   before committed authority using the existing durable protocol. All these actions
   occur only after the coherent recovery set is durable.
5. Checkpoint/close the staged DB and sync it. With all destination writers stopped,
   checkpoint/close the old DB before removing its WAL/SHM; sync journal removal before
   atomically renaming the staged DB, then sync the DB and parent. A crash exposes
   either a complete old DB or a complete paused imported DB, never old WAL over a new
   main file. Record the publication boundary durably. Failure or cancellation after
   inhibitors are installed leaves them and the ingress guard in place.
6. Mark the transaction complete durably, keeping the callback hold and runtime
   inhibitor. Remove only the daemon transaction inhibitor durably; reload/verify units.
   Start the daemon only if its recorded previous active state required it. Start/ready
   failure is a reported incomplete recovery condition, not successful restoration.
   Never automatically start sing-box, replay previous config or remove the guard.

No automatic rollback reactivates credentials. An explicit local `restore-recover`
operation, serialized under the same lifecycle lock, restores the **current destination**
recovery set with both services stopped/inhibited, journal-safe DB replacement and
coherent authority/artifact recovery. Preserve original DB flags and original hold
in that set without adding a second activation schema. Recovery remains held/guarded;
a distinct explicit recovery activation acknowledges reusing destination credentials
before restoring runtime/service enable intent. It never enables imported WireGuard.
If interrupted, retry/recovery inspects the durable phase and verifies contents rather
than treating absent markers as success. The completed restore record remains durable and identifies whether callback hold or
explicit resume is expected; a missing hold is not inferred as resume. Uncertain/corrupt
phase, hold, resume record or recovery material fails closed. Recovery data contains secrets and is never printed or exported
by diagnostics. No automatic garbage collection is added.

### Trusted hold, administration and explicit intent

The callback hold is root-owned private installation state outside the imported DB,
written atomically and directory-synced. It is not a `Settings` field or archive value.
The daemon reads and validates it before constructing/starting any network-producing
manager, recovery, scheduler or API callback. An incomplete transaction refuses normal
startup; a completed held restore serves authenticated loopback administration with
ingress guarded and runtime inhibited. Normal installations without restore state
retain existing behavior; no global `!initialized` callback gate is introduced.

Expose authenticated `GET /api/restore` for sanitized status and
`POST /api/restore/resume` for deliberate destination activation. The panel shows a
persistent hold banner, the destination checklist and a separate explicit Resume
external activity action; no page load, background request, ordinary settings save,
archive field, Apply attempt or restart clears the hold. Resume requires an explicit
acknowledgment of saved external destinations/credentials and the completed destination
checklist. Durably record resume before starting callbacks; failed persistence starts
none. Repeated resume is idempotent. It allows list/native/catalog acquisition **before**
Apply, scheduled tunnel checks, notifications and encrypted backup delivery, so the
operator must audit or replace imported external settings before resuming. It does
not enable WG, publish policy, clear the runtime inhibitor or release client ingress.

While held, allow login, local UI/assets, local configuration reads/edits, local tunnel
parsing, downloaded backups and read-only local status. Prohibit Apply and every external
manual action with a visible restore-held error: list/catalog fetch/refresh, tunnel
checks, WARP registration/deletion, remote community discovery, domain/route reachability,
notification test, backup send and diagnostics that probe external services. Split local
diag collection from external checks if necessary; a GET must not accidentally probe.
Saving external settings remains local and cannot launch a refresh. Manual actions do
not individually bypass or clear the hold. After resume their existing explicit intent
semantics apply; callbacks still cannot publish pending DB policy implicitly.
Resumed list/pool/check workers cannot recover or start runtime, clear inhibition or
publish anything while no committed generation exists. Once explicit Apply establishes
a generation, background updates retain the existing committed-policy-only contract.
The compiled-in community catalog and purely local diagnostics remain available; any
future external variant is gated before the action. WARP deletion must check the hold
before deleting its local row, not after the provider callback is reached.

After acquisition, explicit Apply prepares/checks a full configuration using paused WG
and preserves unavailable-tunnel domain/CIDR rejection and selected-orphan handling.
Only a fully prepared guarded publication that has installed and synced its checked
paused-safe config at the sole supported runtime path may durably remove the runtime
inhibitor and start sing-box. This release is a small durable `publishing` phase, not
completed activation: install/verify the client guard and install/sync the checked
candidate first, then persist BeginPublishing before removing the runtime marker for
controlled restart. Startup, reset, installer/setup and recovery treat `publishing`
and uncertain marker/record combinations as protected/incomplete. Imported callbacks
cannot interpret them as explicit resume or successful policy publication. Finish
publishing only after the checked runtime, nft policy, committed authority and bootstrap
invalidation are coherent and durable; release client ingress last. If publication
fails before that commit, Abort must durably recreate the runtime inhibitor before
returning the record to complete-but-runtime-inhibited state. Failed Abort remains an
uncertain protected phase requiring explicit recovery; never infer completion from an
absent runtime marker. A crash at any intermediate marker/record boundary fails closed.
This phase changes no WG enabled flags. Startup still cannot recover quarantined config. Failed publication
stays guarded under ADR 0023 and never restores old runtime. A separate explicit tunnel
enable plus subsequent Apply is required to activate chosen outgoing WG credentials;
background work/reboot cannot publish that pending edit. Resume alone never releases
client traffic. After successful paused Apply, ordinary direct and non-WG policy may
operate as saved, while rules depending on paused first/second hops reject.

The managed `razdachad -reset-network` entry point must inspect trusted restore state
before removing nft/routing state. It refuses guard removal while a transaction is
incomplete, callbacks are held or runtime is inhibited, even after callback resume.
Corrupt/uncertain state also refuses reset. Only the explicitly specified guarded
publication or serialized recovery transition may change restore safety state; ordinary
reset, setup, update or uninstall cannot silently bypass it. Any destructive uninstall
with retained restore state must either reject or use an explicitly defined serialized
recovery/teardown path, preserving recovery material and never starting credentials.

### Destination adaptation

Before cutover, require explicit local review of incoming endpoint/public hostname,
WAN interface/hardware, route and firewall compatibility, UDP port and WAN/NAT mapping,
panel exposure/bindings and TLS certificate SANs. Reuse existing incoming identity and
peer keys; do not regenerate them to adapt a destination. Changes are explicit local
edits, not external reachability probes or source-host discovery. Display the checklist
and retained recovery location before mutation and in held status. Endpoint/DNS/NAT or
certificate changes can require client configuration updates or trust changes; keeping
keys does not guarantee uninterrupted client connectivity. The source is retired only
by an operator outside this restore operation.

## Alternatives and consequences

DB-only pause plus wg0 guard leaves independent handshakes and callbacks possible.
Disabling every imported setting loses intent and still misses runtime files. Holding
acquisition until Apply deadlocks missing native artifacts. Keeping both services off
until out-of-band recovery is simpler but removes local panel administration. The chosen
narrow hold retains administration, with explicit runtime inhibition and fail-closed
unsupported topology. Availability interruptions and extra destination review are accepted.

## Required proof before acceptance

See [task 217](../../tasks/217-safe-cross-host-restore.md) for the baseline, failure,
crash/restart, rollback and synthetic handshake matrix. This ADR is a proposal only:
no implementation, test pass, independent approval, staging, merge or release is claimed.
