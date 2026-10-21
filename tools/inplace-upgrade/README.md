# Bounded in-place operator upgrade

This Linux/root tool wraps the supported `razdacha setup --start=false` command.
It supports a healthy existing installation with **committed generation v4**, no
pending policy, the **same SQLite schema** and **same pinned sing-box runtime**.
It refuses legacy/bootstrap-only state and schema/runtime transitions. The normal
fresh installer is unchanged. It never calls Apply or removes the daemon guard.

A maintenance interruption is expected. Trusted daemon committed recovery publishes
policy and releases its native ingress guard. Independent Clash/DNS/TLS/process and
preservation checks are **post-publication acceptance**, not an extra traffic
barrier. Their failure stops the daemon, reinstalls/verifies the guard and rolls
back. Successful return does not establish every client's end-to-end connectivity.

## Prepare exact inputs

Build from the reviewed candidate source using the target architecture:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o inplace-proof ./tools/inplace-upgrade/proof
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o inplace-proof.test ./tools/inplace-upgrade/proof
python3 -m unittest discover -s tools/inplace-upgrade -p 'test_*.py'
```

The helper tests use injected nft fixtures and do not modify host networking. They
must also run on Linux before deployment. The Python tests inject all host service,
network and process operations and use only temporary trees and synthetic keys.

Copy the controller, proof helper and published release archive to private,
root-owned staging files. Verify the archive against trusted release checksums.
Obtain both executable hashes from that verified archive, and the expected running
version/commit from the reviewed release build. Pin the helper hash separately.
Create a **root-owned mode `0600` JSON manifest** with exactly these fields:

```json
{
  "archive": "/root/release/razdacha-linux-amd64.tar.gz",
  "archive_sha256": "REPLACE_WITH_TRUSTED_ARCHIVE_SHA256",
  "helper": "/root/release/inplace-proof",
  "helper_sha256": "REPLACE_WITH_VERIFIED_HELPER_SHA256",
  "version": "REPLACE_WITH_RELEASE_VERSION_WITHOUT_V_PREFIX",
  "commit": "REPLACE_WITH_EXACT_API_BUILD_COMMIT",
  "schema": 12,
  "runtime": "1.12.25",
  "razdacha_sha256": "REPLACE_WITH_VERIFIED_CLI_SHA256",
  "razdachad_sha256": "REPLACE_WITH_VERIFIED_DAEMON_SHA256"
}
```

Inputs must be regular owned files without group/world write or symlinked parent
paths. Only the published four-member archive (two binaries, LICENSE, README.md)
is accepted. Candidate CLI/daemon versions and linked runtime are checked before
stopping anything. Archive/helper/executable checksums are mandatory.

## Check, run, status and recover

The backup parent `/var/backups/razdacha` must be root-owned mode `0700`. Each command
uses a new operation name with 8–64 letters/digits/hyphens after `inplace-`.

```sh
python3 upgrade.py check --manifest /root/release/expected.json \
  --operation /var/backups/razdacha/inplace-check-20261021
python3 upgrade.py run --manifest /root/release/expected.json \
  --operation /var/backups/razdacha/inplace-release-20261021
python3 upgrade.py status \
  --operation /var/backups/razdacha/inplace-release-20261021
python3 upgrade.py recover \
  --operation /var/backups/razdacha/inplace-release-20261021
```

`check` stages only private inputs and reads the healthy baseline, including an owned
panel login/logout and real DNS request. It does not stop services, change networking
or run setup. Its `checked` operation is terminal; `run` must create a **different**
operation and take a fresh stopped snapshot. Disk preflight conservatively requires
four times managed content plus archive staging and reserve on each relevant volume.

The password is prompted privately; automation may pass an already-open descriptor
with `--password-fd`. Never put passwords in arguments, manifest files or environment.
No third-party endpoint receives diagnostics or configuration. The bounded DNS probe
asks the installation's existing resolver for `example.com`; its configured upstream
may receive that ordinary DNS query.

An exclusive process lock and durable private current-operation marker prevent a new
upgrade while an earlier mutation needs recovery. Explicit recovery must match that
marker's path, directory inode and random ledger token. Old completed/superseded
ledgers cannot replay a stale snapshot. Do not delete or edit markers/ledgers to
bypass refusal. `status` prints only phase/recovery-required state; raw command output,
policy, hashes and errors remain in private operation files.

## Snapshot, acceptance and rollback bounds

After proving the daemon fully stopped, the helper installs and independently checks
only the exact dedicated native ingress guard. The runtime is then stopped. A fresh
snapshot retains the complete daemon state/authority/native artifacts, runtime
configuration/cache/binary, both application binaries, nginx/TLS configuration,
setup-managed units and sysctl drop-in. Its SQLite copy is a standalone WAL-aware
integrity-checked snapshot. The snapshot is synced and retained permanently.

Only then are the two application binaries replaced atomically and candidate setup
run with `--start=false` and a minimal environment. Before candidate start, verify
stable policy, committed/native authority, runtime config hash, WG identity, protected
TLS/nginx/units/sysctl files and runtime/candidate binary hashes. After committed
recovery, accept only matching actual process/build/runtime/schema, real Clash/DNS,
HTTPS with existing certificate trust, unchanged protected files and absent guard.
All user fields, keys, activation, peer egress and rule scopes compare strictly.
Only `pool`/`pool_updated_at` on **paired `source=pool` rows** may refresh; the
installed version is separately checked. This does not approve pending Apply edits.

Post-stop failure first stops the daemon and verifies protection before stopping
runtime/restoring. Rollback prepares sibling copies from the full fresh snapshot,
records inode identities durably, then moves uncertain live objects to collision-
checked sibling quarantines. It never overlays or deletes uncertain live trees.
Interrupted atomic renames resume by actual identity, including interruption after
a rename but before its phase update. Fully exited failed units are accepted only
with zero main/control/cgroup processes and no queued job.

A missing/incomplete snapshot, unknown tree identity, quarantine collision, corrupted
snapshot or failed rollback leaves `recovery-required`. The controller attempts to
stop the daemon and retain protection; a separate `protection_failure` in the private
ledger means that protection itself could not be proven and needs immediate operator
attention. Never clear a guard merely to pass readiness. Normal signals enter bounded
recovery; SIGKILL/host failure requires explicit `recover`. A partially prepared
unrecorded sibling is conservatively refused for administrator inspection.

The tool is not a general installer or cross-host restore command. Setup changes to
protected files, a different schema/runtime, external concurrent administrator
changes, custom path layouts and nonstandard service/binary locations are outside
its accepted transaction. Resolve these in a separately reviewed procedure.
