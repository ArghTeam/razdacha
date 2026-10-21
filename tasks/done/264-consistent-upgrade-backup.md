# 264: Consistent pre-migration upgrade backups

## Goal

An upgrade must retain committed data in SQLite WAL in a standalone rollback
snapshot before opening the migrating store. Copying only `state.db` can silently
lose recent keys and settings even when the main file passes integrity checks.

## Scope and checkout provenance

- Authorized repository: `ArghTeam/razdacha` only; no deployed customer data.
- Primary checkout: `/Users/romanzhigalkin/Web/razdacha`.
- Remote/base: `origin/main`, freshly fetched
  `1a3b84e81f5b50b6596f7e259fae137d4ae31def`.
- Feature checkout: `.codex/worktrees/fix/264-consistent-upgrade-backup`.
- Ownership: setup backup, pre-migration store helper and relevant store/packaging
  context. The restore call site only forwards the new context argument.
- No new update framework, service sequencing or cross-host restore behavior.

## Behavior and acceptance

- `store.BackupAt` opens an existing database read-only without migrations or
  journal-mode changes; a SQLite `VACUUM INTO` snapshot includes committed WAL.
- The original schema version, application ID, keys and installation metadata
  survive. The source main file and WAL are not checkpointed or rewritten.
- Reserve the output exclusively with mode `0600` before SQLite writes private
  data. New backup directories use `0700`. Existing files and symlinks are refused.
- Verify the standalone snapshot with `integrity_check` and sync before success;
  failure or cancellation removes the incomplete reserved output.
- Setup keeps version-labeled names with nanosecond timestamps and propagates its
  context. Missing or corrupt sources stop the upgrade before migrations.
- `InstalledVersionAt` uses the same escaped read-only connection rather than the
  normal migrating/WAL-enabling DSN.

## Verification

- Active, uncheckpointed WAL regression through the setup backup function checks
  old schema, key, saved version, integrity, source bytes and repeated backups.
- Store regressions cover independent snapshots, journal mode, escaped paths,
  absent sources, permissions, existing files/symlinks, cancellation and failures.
- The setup regression is also run with the previous main-file-copy algorithm to
  establish that it detects the original loss of committed WAL data.
- Passed: `go test ./...`, `go test -race ./internal/store ./cmd/razdacha`,
  configured `golangci-lint` with a fresh cache (zero issues), and `git diff --check`.
- Negative control: the original main-file-copy algorithm fails the setup
  regression because the committed synthetic private key is absent from the copy;
  the consistent snapshot passes.
- Cancellation before reservation and invalid-source failure leave no snapshot.
  No claim is made about deterministically interrupting an in-flight SQLite copy.
- This change makes no deployment or outage-free guarantee.
