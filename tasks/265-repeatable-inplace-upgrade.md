# Repeatable in-place upgrade

Issue: #265

## Scope and provenance

Primary checkout: `/Users/romanzhigalkin/Web/razdacha`; remote: `origin`; target: `main`; freshly fetched base: `b3b3368426a50af3454c2cc17b3bf5e21106ab76`. This task follows #264. Only this repository and its authorized worktrees may be modified.

Provide a bounded repository-owned root operator procedure around the existing candidate `razdacha setup --start=false` command. Do not duplicate setup, change client policy, create a cross-host restore mechanism, or automatically Apply pending policy. A guarded maintenance interval is expected; uninterrupted traffic is not promised.

The first supported transaction is deliberately restricted to an existing healthy installation with version-four committed authority, the same SQLite schema and the same pinned sing-box runtime. Refuse legacy/bootstrap-only, unsupported schema/runtime transitions, unsafe paths, unknown guard shape, partial prior operations and unhealthy baseline before mutation. Legacy first upgrades still require an explicit administrator Apply and a separate reviewed migration procedure.

## Transaction contract

1. Stage and verify the release checksum, archive membership, candidate versions and executable hashes before replacing installed binaries. Require immutable expected inputs; never silently skip checksum verification.
2. Exclusively lock the operation. Use a fresh root-owned private operation directory, an explicit phase ledger and bounded command deadlines. Keep raw command/configuration output and credentials private. Preserve the rollback snapshot permanently until the operator removes it.
3. Record a healthy baseline: actual installed process executable hashes; services; committed authority; database integrity/schema/install version; stable policy; exact guard shape/absence; TLS, nginx, units and runtime hashes; WireGuard identity. Systemd active alone is insufficient: check actual Clash runtime and a real bounded DNS response.
4. Stop the daemon and prove no main/control/cgroup process or queued service job remains. Before stopping the runtime or changing state, install and independently verify the exact dedicated ingress guard. Then stop the runtime and take a coherent fresh snapshot of daemon state, committed/native authority, immutable artifacts, runtime config/cache, installed binaries and setup-managed files. Use the WAL-safe database helper or a verified stopped database snapshot. Require sufficient disk space before stopping services.
5. Atomically replace the two binaries and invoke the verified candidate setup command with `--start=false`, preserving saved panel mode and removing installer environment overrides. Verify that the pinned runtime and protected TLS/nginx settings remain unchanged. Start the existing runtime and candidate daemon; committed-generation recovery, rather than Apply, owns guard release.
6. Accept only after actual candidate process/version, runtime/DNS, stable policy, WireGuard identity and protected file checks pass and ingress guard is absent. Compare all user fields strictly. Only `pool` and `pool_updated_at` may differ when both corresponding tunnel rows remain `source=pool`; installed version is verified separately. Never ignore peer egress, source, keys, activation or rule fields.
7. On a post-stop failure, protect ingress before restoring anything. Stop and prove both writers stopped. Restore the complete fresh snapshot with collision-checked sibling quarantines, never overlay/delete uncertain live trees. Restore the matching binaries and setup-managed files; restart the old runtime/daemon and require the same real readiness and policy proofs. The trusted committed recovery may release the guard; the controller must never remove it merely to make a health check pass.
8. A failed rollback, unknown tree identity or interruption leaves a private explicit recovery-required phase and ingress closed. Never guess, reuse old snapshot paths, restore a different schema or claim success. Handle signals and partial atomic renames from actual filesystem identity, not only the last ledger entry.

## Implementation and acceptance

Prefer a small standard-library operator controller plus bounded Go proof/guard helper over a second installer. Reuse existing reviewed primitives where practical. The controller is an explicit operator workflow; the normal fresh installer remains unchanged. Document build/run/status/recovery commands and the supported bounds. Do not publish deployment-specific addresses, hostnames, credentials, raw state or user/tunnel names.

Use injected filesystem/process/clock boundaries for meaningful phase failure tests, including partial binary/tree replacement, failed-unit-but-fully-stopped handling, foreign guard, source-pool policy comparison and a retained guard on failed recovery. Run the isolated failure rehearsal and independent review before any production deployment. Update packaging maintained context and the installation guide in this PR. Record any limits explicitly; do not expand into unrelated daemon policy or restore work.

## Implemented boundary

The repository-owned controller is `tools/inplace-upgrade/upgrade.py`, with Linux
proof helper `tools/inplace-upgrade/proof` and a complete operator runbook beside it.
A separate `check` action stages exact private inputs and verifies baseline/disk
without service/config/network mutation; it cannot be reused as a live snapshot.
A global private current-operation marker binds directory identity and ledger token,
blocks unfinished upgrades and refuses replay of completed or superseded recovery.

The native guard belongs to the daemon. Trusted committed recovery publishes and
releases it before independent external acceptance checks; this wrapper does not
claim traffic stays guarded until those later checks pass. Strict policy, protected
files, runtime config and binary checks also run before starting the candidate.
No separate operator guard or general updater architecture is introduced.

Injected tests cover successful catalog refresh with strict user fields, checksum
and unsafe archive refusal, non-mutating check, partial binary replacement and
setup writes, changed runtime config before start, post-publication readiness
failure, guarded failed rollback, unknown tree identities, per-phase failure,
partial recovery renames, fully stopped failed units, stale current markers,
missing-marker unfinished operations and installer-environment removal.
Linux helper fixtures independently reject foreign guard shapes. Cross-build and
local injected acceptance are required before independent review and any live use;
no production execution is claimed by this implementation record.

## Local verification

- Fresh `origin/main` at `505b398cd99d0cd5231c0665a35afb494d43dff1` was merged
  without rebasing the implementation branch.
- `go test ./...` passed on the integrated branch.
- `python3 -m unittest discover -s tools/inplace-upgrade -p 'test_*.py'` passed
  all 19 injected tests. The same command is included in the existing CI test job.
- Configured repository lint and Linux-target proof-helper lint passed with zero
  issues; `git diff --check` passed.
- Static Linux amd64 proof and pure-test ELF cross-builds passed. Pure Linux test
  execution and independent final review remain explicit pre-deployment gates.
- No host upgrade or live service/network changes were performed by this task.
