# Retained UDP recovery after a REALITY exit outage (#350)

Status: Preparation; live acceptance is pending. Tracker: [#350](https://github.com/ArghTeam/razdacha/issues/350).

## Goal and scope

Characterize the finite three-second retained-WARP-UDP observation from
[task #348](done/348-reality-exit-reliability.md). Distinguish delayed responses,
association behavior and ordinary idle effects before proposing a fix. Do not
infer a permanent defect, seamless recovery or general UDP/media behavior.

Use the existing owned REALITY-only RAC test profiles. Keep one local client
process, SOCKS TCP control socket, UDP socket, local UDP endpoint and relay endpoint
unchanged from successful priming through all observations. A new transaction ID
is required for every STUN request; a late reply to an older request is evidence
about that request only. Do not recreate the retained association before the
observation series is classified.

Production and DO configuration are excluded. No Apply, daemon/client restart,
credential/routing changes, new cloud resources or implicit Direct fallback.
The sole fault is the existing task348 helper's exact RAC-to-DO TCP2443 OUTPUT
DROP, with its independently armed 30-second watchdog and immutable holder
deadline at most 60 seconds. This affects all sandbox routes using that endpoint;
it is not peer-isolated and does not test WARP failure inside DO. Current Direct
and WARP endpoints share one host.

## Control-first gate

Before any new outage, run two independent pairs of separately primed associations
with no fault: five seconds and twelve seconds of idle time per pair. Each gets
exactly one post-idle transaction with a three-second maximum observation and
concurrent fresh controls. This avoids adding a later long idle gap to the short
control. The same local client processes remain running across all four cases. Inspect the retained SOCKS TCP control
socket passively for EOF with readiness plus `MSG_PEEK`; do not consume control
bytes or reconnect. Record actual idle/send/response timing and scheduling drift.

If retained non-response or control EOF occurs without a fault while fresh
requests work, record that idle confound before deciding whether an outage can
answer a remaining question. Do not inject a fault merely to repeat an already
confounded observation. The current private runner has no executable fault/capture path; the conditional
fault design below remains future work if the controls leave a useful question.
Preparation is not live fault acceptance.

## Intended fault comparison, if still warranted

1. Pin reviewed source/helper/client hashes and inspect fresh RAC runtime,
   protected user-table/configuration/authority hashes, released guard and PIDs.
   Require the existing two owned peers and exact applied exits; retain a working
   Direct control. Run the helper's isolated kernel selftest before live mutation.
2. Establish healthy fresh Direct/WARP STUN and verified HTTPS controls. Prime the
   retained WARP association with a matching STUN response. Record its socket,
   control, client-process and endpoint identities.
3. Start the exact owned finite WAN capture before negative requests. Arm the
   independent watchdog before installing the fault. After installed readiness,
   send a fresh retained transaction and separate fresh WARP/Direct requests.
   Verify negative selected requests, increasing exact DROP counter, healthy
   other-exit controls and the finite no-direct-escape packet oracle. Stop and
   validate the negative capture before recovery traffic.
4. Require verified revocation/table absence and stopped fault PID. Record the
   independent watchdog's trusted actor/monotonic journal evidence. Begin the
   recovery clock only after confirmed absence.
5. On the same association, issue distinct transactions at offsets 0, 7 and 27
   seconds, each with a three-second maximum observation. These are request
   windows ending by approximately 3, 10 and 30 seconds, not continuous observation
   or a recovery-time guarantee. Record actual send/receive times and scheduling
   drift. Run separate fresh WARP and Direct STUN controls concurrently in each
   window. Keep all late/unmatched responses in the transaction ledger.
6. Create and prime a separate no-fault WARP association, leave it idle for the
   measured fault-case prime-to-removal duration, then apply the same schedule
   and controls. This is a comparable idle-duration control, not an identical
   traffic history: the fault case also sent a transaction during its outage.
7. Only after all retained observations may an explicitly labeled fresh/recreated
   association serve as a recovery control. No client restart is needed.
8. Unconditionally revoke the exact owned fault even when capture/probe errors
   occur; do not cancel its watchdog until verified absence. Close only owned
   sockets/processes, remove temporary profiles and delete only the owned session.
   Verify protected state, authority/configuration, released guard and PIDs against
   the fresh before snapshot. Preserve failed artifacts and explicit cleanup errors.

## Evidence and acceptance

The tracked [probe](../tools/udp-recovery/README.md) is inert offline code. Its
socket/transaction ledger and offline tests do not themselves establish any live
path. Private orchestration and raw captures contain operational information and
remain outside Git; record reviewed exact hashes and aggregate results here after
execution. No preparation or CI success is live acceptance.

A valid characterization requires a healthy primed association, unchanged
identity, fresh controls per window, exact fault/counter/negative capture evidence,
independent removal evidence, the matched no-fault idle control and verified
cleanup/protected-state equality. Missing controls or observation errors remain
unknown; a timeout is bounded non-response, not a permanent stuck-flow conclusion.
Any implementation fix requires a separately reviewed responsible-layer finding.
Changes to dependency repositories require separate authorization; never patch a
module cache as a hidden fix.
