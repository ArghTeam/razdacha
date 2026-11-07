# RAC selected-exit fault fixture

This opt-in test tool is separate from the daemon. Its sole mutation is one owned
inet OUTPUT DROP table on the explicitly authorized RAC sandbox. It never edits
application policy/configuration, restarts application services or contacts DO.
See [task #348](../../tasks/348-reality-exit-reliability.md) for intended evidence,
controls and limits. No live acceptance is implied by building or testing it.

The endpoint is fixed to `165.22.196.130`, TCP443 or TCP2443. Every command requires
`--enable-rac-exit-fault` and Linux root. Activation (`prepare`, `hold`, `selftest`)
also requires the local RAC IPv4 `193.233.112.227`. Revocation/status rely on the
private same-boot, same-network-namespace journal and exact owned kernel state, so cleanup remains possible
if the sandbox address disappears.
Runs use fresh 32-character lowercase hexadecimal IDs and private journal/lock
files under `/run/razdacha-egress-fault-348/<run-id>/`. Do not reuse run IDs.

CLI and exact systemd orchestration are documented alongside the implementation.
The controller must independently arm the watchdog before `hold`; foreground
cleanup alone is insufficient. The test affects every RAC route using the chosen
outer endpoint, not just the selected test client. TCP2443 failure does not test
WARP failure inside DO; both endpoints share one DO host.

## Build and isolated kernel check

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o racfault ./tools/rac-egress-fault
```

The controller pins the clean source commit and binary SHA-256 before staging a
root-owned, non-writable executable. On RAC, first run:

```sh
/path/to/racfault selftest --run-id 0123456789abcdef0123456789abcdef --enable-rac-exit-fault
```

`selftest` starts `/usr/bin/unshare --net --fork --kill-child=KILL` with a bounded
child lifetime. Every Go thread/socket is created inside the new namespace.
Before netlink use, the child requires a namespace distinct from PID1 and its
recorded parent, with only the loopback interface. The production backend must
install/read/remove both exact endpoint rules and reject an exclusive collision.
Namespace teardown on exit/crash contains failed roundtrips. This does not test
live traffic, a provider, the application or the host firewall. Failure aborts
live testing; never weaken ownership checks to force cleanup in the host namespace.

## Controller contract

1. Generate a fresh 32-character lowercase hexadecimal run ID. Choose one port and
   an absolute UTC RFC3339 deadline 5–90 seconds away. `prepare` creates no rule:

   ```sh
   /path/to/racfault prepare --run-id RUN_ID --port 2443 --deadline UTC_DEADLINE --enable-rac-exit-fault
   ```

2. Use the returned names exactly. Arm the independent watchdog timer first with
   one `OnActiveSec` trigger, no calendar/recurring trigger, no randomized delay
   and accuracy at most one second. The timer may fire earlier than the holder
   deadline; it must still be future and no later than the immutable monotonic
   deadline. Its triggered service must execute exactly:

   ```sh
   /path/to/racfault watchdog --run-id RUN_ID --enable-rac-exit-fault
   ```

3. Start the exact returned fault service with `Type=exec`, `KillMode=control-group`,
   `SendSIGKILL=yes`, a positive `TimeoutStopSec` at most five seconds and a finite
   `RuntimeMaxSec` at most the remaining deadline. Give startup
   enough margin for property verification. Its commands must be exactly:

   ```sh
   # ExecStart
   /path/to/racfault hold --run-id RUN_ID --enable-rac-exit-fault
   # ExecStopPost
   /path/to/racfault cleanup --run-id RUN_ID --enable-rac-exit-fault
   ```

   The binary verifies actual systemd executable paths and arguments, its own
   MainPID, timer/service identity and numeric monotonic timer properties via
   `/usr/bin/busctl --system --json=short`. No mutable marker alone arms a fault. The watchdog must retain root credentials,
   CAP_NET_ADMIN and the host network namespace.

4. Wait for `status` to prove `phase=installed`, `installed=true`, the exact tuple
   and a future deadline. Start bounded probes only after capture readiness. DROP
   counters count outer TCP packets/retransmissions, not client payload bytes:

   ```sh
   /path/to/racfault status --run-id RUN_ID --enable-rac-exit-fault
   ```

5. Stop only the owned fault unit, then invoke `cleanup` and require both stopped
   fault MainPID and `phase=revoked`, `table_absent=true`. Cancel the watchdog only
   after those assertions. Keep the revoked journal/lock: reusing or deleting them
   would destroy the late-installer barrier. Never restore a global firewall dump.
   A watchdog run validates and stops only its exact owned unit, then retries
   exact cleanup. Unit identity/stop errors still attempt safe owned cleanup and
   remain errors. A journal persistence failure also attempts exact removal rather
   than retaining the injected outage; it cannot report completed revocation. A foreign schema/unit or unverifiable absence is a failure,
   never permission to delete arbitrary resources.

`prepare`, `status` and successful cleanup/watchdog commands return one JSON line
with `run_id`, `table`, `target_ip`, `target_port`, `deadline`,
`deadline_monotonic_usec`, `network_namespace`, `journal`, `phase`, `installed`, `table_absent`,
`packets`, `bytes`, `fault_unit`, `watchdog_service` and `watchdog_timer`.
`hold` emits an installed observation, waits until a monotonic deadline or signal,
then emits cleanup observation. A caller must check exit status as well as JSON.
Table absence is verified, not inferred from a process exiting. Ordinary CLI lock
waits are bounded; systemd process budgets and the independent watchdog are still
required to bound failed or stalled holders.
