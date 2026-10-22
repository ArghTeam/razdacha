# 273: Checked configuration proofs for in-place upgrades

## Scope and provenance

Authorized repository: `ArghTeam/razdacha` only. Primary checkout:
`/Users/romanzhigalkin/Web/razdacha`; remote `origin`; target `main`; fresh base:
`a4dcc3295cf69017129a5c1812102dbe40cb2797`. Feature checkout:
`.codex/worktrees/fix/273-checked-pool-upgrade`.

This changes only the bounded operator proof/controller, focused regressions and
maintained packaging documentation. No daemon, cross-host restore, legacy migration,
release changes or host operations are included. Historical failed-operation
ledgers and snapshots are preserved.

## Final proof behavior

A private diagnostic rehearsal showed that raw generation can differ from a healthy
configuration because ADR 0019 removed a runtime-rejected pool member. The existing
checked preparation reproduced that private baseline exactly without publication.

The helper now uses `Applier.PrepareRecovery` with frozen committed policy/lists,
a fresh owner-only temporary ConfigPath and a capturing checker. It never calls
Commit, Reload or starts another runtime. Existing ADR 0019 logic permits only
bounded pool-member retries; all other validation failures refuse proof. The
installed checker version must equal the pinned compiled library. Its executable
hash/ownership are verified before/after the entire proof and every check, and the
controller compares that hash/version to its installation baseline.

`generated_config_sha256` retains its existing field name but now identifies the
last successful checked candidate. `raw_generated_config_sha256` separately retains
the initial generated hash. Exact original raw authority and canonical digest remain
private immutable evidence, and every current native artifact is independently
verified. Temporary owned candidates/directories are cleaned on success or error.

## Coherent snapshot boundary

An automatic pool refresh can occur between preflight and stopping the daemon.
After both writers stop under the verified guard, compare a fresh authority proof
against the original using only the existing paired-pool exception; require actual
stopped config to equal its exact checked proof. Record separate stopped authority
and config hash with the durable full snapshot before any binary replacement.
Preserve original preflight authority/config evidence. Setup and rollback pre-start
checks pin the stopped snapshot; post-start checks use exact current accepted checked
output. An arbitrary concurrent config change cannot become a new trusted baseline.

## Verification

- Python controller matrix: 29 passing tests, including forward and rollback with
  a pool/catalog/config refresh between preflight and snapshot, preserved original
  evidence, exact stopped snapshot identity, and arbitrary config refusal.
- Linux helper regressions cover one permitted pool retry with exact final output,
  immediate non-pool refusal, private ConfigPath/no publication, owned temporary
  cleanup and changed checker rejection before execution.
- Existing raw-authority evidence, JSON strictness and native-corruption cases
  remain covered. Linux compilation/static checks and independent Linux execution
  and review are required before use; no host upgrade is claimed by this record.
