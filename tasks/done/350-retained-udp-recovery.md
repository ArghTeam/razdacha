# Retained UDP recovery after a REALITY exit outage (#350)

Status: Complete for the bounded idle-confound characterization. Active-flow outage
recovery remains a separate investigation. Tracker: [#350](https://github.com/ArghTeam/razdacha/issues/350).

## Goal and scope

Characterize the finite three-second retained-WARP-UDP observation from
[task #348](348-reality-exit-reliability.md). Distinguish delayed responses,
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

## Original conditional fault design, not executed

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

The tracked [probe](../../tools/udp-recovery/README.md) is inert offline code. Its
socket/transaction ledger and offline tests do not themselves establish any live
path. Private orchestration and raw operational evidence remain outside Git. The final
record below distinguishes actual no-fault observations from this conditional
fault design. No new outage or packet capture was executed under #350.

A valid no-fault characterization requires a healthy primed association, unchanged
local identity, fresh concurrent controls, passive control status and verified
cleanup/protected-state equality. Any future conditional outage test additionally
requires exact fault/counter/negative capture and independent removal evidence
with matched no-fault traffic history. Missing controls or observation errors
remain unknown; a timeout is bounded non-response, not a permanent stuck-flow
conclusion.
Any implementation fix requires a separately reviewed responsible-layer finding.
Changes to dependency repositories require separate authorization; never patch a
module cache as a hidden fix.


## Final bounded acceptance — 2026-11-13

### Result and decision

Long idle alone reproduced retained UDP non-response. After approximately 30 and
45 seconds of silence, the original SOCKS TCP control was already at EOF before
any new UDP request. A new STUN transaction on the unchanged local UDP socket got
no response within its three-second observation, while new Direct/WARP associations
worked. No outage was injected. This is an idle confound for the old #348
three-second retained-association observation, not proof of an outage-recovery
network defect.

The two independent five-/twelve-second pairs all received matching responses;
a nominal ten-second timeout seen during source inspection did not reproduce as
a failure at twelve seconds. These samples do not locate the closing hop, prove
a precise timeout threshold or establish causation for the historical fault case.
Local socket identity remained unchanged even when its remote SOCKS control had
closed; local object/file-descriptor survival alone is not a live association.
No product fix or configuration workaround is justified by these data.

Active-flow carrier recovery needs a distinct keepalive/continuously active test
with matched no-fault traffic history under [#352](https://github.com/ArghTeam/razdacha/issues/352). Keep the idle and
outage questions separate before designing approved backup exits. The original
3/10/30-second fault windows were not executed in this task.

### Runtime, observer and operations provenance

The designated primary checkout is `/Users/romanzhigalkin/Web/razdacha`, remote
`origin`, target `main`. Fresh fetch verified base
`54e2f7bc121972fd3aca918d451c40c4c7b86f43` before creating
`test/350-retained-udp` in `.codex/worktrees/test-350-retained-udp`.
The tested tracked observer/tests are source preparation commit
`d11e85ec9003af23da06fa629afc5d0a7a934f5c`. Probe SHA-256 is
`443ac89d4da6400423e9e38d3e46f2073c631f4f066c807a55ec612ee2fb1340`.
The pinned native sing-box client is 1.12.25, binary SHA-256
`ffc693b9edf2f83f3284b94b5e73421555b3f0e2ec166f0193dd6e142e3f4c0d`.
Fourteen offline fake-socket tests cover identity/transaction/deadline boundaries.
The short and extended private runner versions passed eight and nine offline
orchestration checks respectively, including no executable fault/capture path,
passive EOF inspection, cleanup on failure and bounded case dispatch.

The private runner creates only current owned REALITY exports with loopback mixed
inbounds substituted. Saved DNS/routes/outbounds are preserved. No TUN, machine
route/DNS changes, client-process restart, Apply, application restart, injected
outage, capture or DO/production configuration changes occurred. The same two
local client processes stayed running throughout each run; each idle case used a
separately primed association that remained unchanged until its own observation.

| Evidence | SHA-256 |
| --- | --- |
| Short-run reviewed source manifest | `945913419156bcf16f256d9077fd6ddf684cf2974a532b558240670422a4da94` |
| Long-run reviewed source manifest | `bb10d7b27629fdd32f9bd035e9c15f686a4aed36279797f484ea7affd9d3192d` |
| Short-run driver | `47db3c65a2652caaf677bc15ba914b1c093065cdaba3a26d04ae80ae1c8855dd` |
| Long-run driver | `d43f20a60836fa0bd842968f9af2ac057ce967f515375a511fdd7d3a873634f9` |
| Short complete proof `38635a7ddc1f21dbfc895b15e8fabdc5` | `46a5a23973457bb737b00f3ce327cc9ed98cf9d9c3551c8699424709efa553d1` |
| Long complete proof `9dd907357072b9e0d349aba992f62b0e` | `f27e75ddaacb848068b2d1a62ee1e4093c2205dcd1ab2ea0e8096d8600abfc93` |
| Final independent local resource audit | `f633dfd5541a3216391d8405516fe8b9d2284e369c59604fe757547ecc759910` |

The private artifacts are retained under `/private/tmp/razdacha-udp350/`;
per-run proofs and transaction ledgers are under `ops/runs/<run-id>/`.
Raw exported profiles were removed, not committed. These hashes identify finite
artifacts; their existence does not extend the measured topology or guarantees.

### Six actual no-fault cases

Each case successfully primed a new WARP SOCKS UDP association, then idled it with
no traffic on that association. Exactly one post-idle transaction used the same
UDP/control objects, file descriptors, endpoints and relay; the test did not
recreate or replace the local association.
Fresh Direct and WARP STUN controls ran concurrently and were healthy in every
case. TCP control inspection used readiness plus `MSG_PEEK`, without consuming
control bytes or changing timeout state.

| Run / case | Actual idle (seconds) | TCP control before / after post-idle probe | Retained STUN observation |
| --- | --- | --- | --- |
| Short, five-second repeat 1 | 5.001536 | Open, no buffered bytes / same | Matching response in 0.096250 seconds |
| Short, twelve-second repeat 1 | 12.003310 | Open, no buffered bytes / same | Matching response in 0.247410 seconds |
| Short, five-second repeat 2 | 5.003148 | Open, no buffered bytes / same | Matching response in 0.088671 seconds |
| Short, twelve-second repeat 2 | 12.003789 | Open, no buffered bytes / same | Matching response in 0.107894 seconds |
| Long, thirty-second case | 30.003534 | EOF / EOF | No response within the three-second observation |
| Long, forty-five-second case | 45.004628 | EOF / EOF | No response within the three-second observation |

The two long cases are one sample per duration, not a timeout distribution or
threshold search. Matching STUN IDs establish these endpoint transactions, not
Android/router compatibility, all-UDP behavior, media quality or seamless failover.

### Protected state and cleanup

Both complete runs have empty error lists and exact before/after snapshot equality.
RAC remains `0.11.0-rc.16.rac.a945231fbf78`, source
`a945231fbf78659c100df5049b5dc77f6a636839`, schema 13. The ingress guard remains
false, and no fixture units were present before or after either run. Daemon,
sing-box and nginx PIDs remain `269709`, `269726` and `8241`.

| Protected object | Unchanged SHA-256 |
| --- | --- |
| Seven protected user tables | `64f12535ecd4ff925d12ef0b47baf5054006bbaec8b796c0ffc21da7cbd2a950` |
| Checked sing-box configuration | `302faf5b71ae4ab6777d7a5c98575fa9ea516713f17171074b0715f91f9d0e14` |
| Applied network authority | `d253665bb5ea39cef8b9c5ef37e47221ca8dd3e6eddb76fe4efc02d182c0d8d9` |

The final local audit independently verifies both runs' owned client PIDs absent,
temporary profile files absent and the temporary session file absent. Each runner
also verifies deletion of only its owned session hash and closed owned sockets and
processes. Normal health/session bookkeeping is separate from protected user rows.
No daemon build/deployment is required: only test observer/docs/context changed;
production source and dependencies are unchanged. Production remains excluded.
