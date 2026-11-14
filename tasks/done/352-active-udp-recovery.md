# Active retained UDP recovery across a REALITY exit outage (#352)

Status: Complete for the bounded active-history RAC comparison. Broader recovery
and reserve policy remain future work. Tracker: [#352](https://github.com/ArghTeam/razdacha/issues/352).

## Goal and scope

Separate active-flow outage recovery from the idle confound measured in
[task #350](350-retained-udp-recovery.md). The historical #348 and #350
observations remain unchanged. No product fix, precise timeout, responsible hop,
seamless failover or Android/media guarantee is established by preparation.

Use only the existing owned RAC REALITY WARP/Direct exports with loopback mixed
inbounds. Preserve saved DNS, routes and outbounds, existing clients/credentials,
configuration/authority and application PIDs. No TUN, machine routing/DNS change,
Apply, daemon/runtime/client restart, new cloud resources, DO configuration or
production operations. Both exits share one DO host; this is no independent-host
redundancy. Commercial and subscription functionality remain excluded.

## Intended active-history experiment

- Successfully prime one WARP SOCKS UDP association and retain its exact UDP and
  TCP-control objects, descriptors and endpoints. Start an active series before
  preparing the fault so setup cannot silently introduce an idle interval.
- A single worker owns every subsequent send/receive. Nominal request cadence is
  three seconds, each transaction observes at most 1.5 seconds. Every STUN ID is
  unique; late old IDs never satisfy a new request. Record passive control EOF,
  actual send times/gaps, response deadline membership and all errors. Local send
  success is not proof that the remote carrier remains alive.
- Inject only the reviewed exact RAC-to-DO outer TCP2443 fault. Arm the independent
  thirty-second watchdog before the sixty-second holder. Preserve exact counter,
  finite zero-drop negative WAN oracle and healthy other-exit controls. This
  affects every RAC route using that endpoint and does not fail WARP inside DO.
- First verified owned rule absence defines the local recovery observation origin.
  Request an immediate worker transaction before journal/cancellation bookkeeping;
  an in-progress bounded receive may defer it by at most its remaining timeout.
  Record actual drift and SSH/status uncertainty. Keep the cadence through thirty
  seconds of recovery; do not create a twenty-second silence between samples.
- Exact local socket identities must remain unchanged. EOF or send/read failure
  is measured liveness loss, not permission to recreate the association. Complete
  fresh post-recovery controls before classifying retained outcomes whenever safe.
- Replay the complete measured send-offset/history from the fault series on a
  separate successfully primed no-fault association, with the same per-request
  timeouts and counts but new IDs. Record timing drift, maximum gaps and fresh
  Direct/WARP controls; do not substitute an equal-duration idle-only control.
- Always revoke the exact owned fault even if captures/probes fail. Cancel only
  after verified table absence and stopped fault PID. Close only owned local
  sockets/processes, remove profiles and delete the owned session. Compare fresh
  protected snapshots before/after. Preserve failed attempts honestly.

## Acceptance and limits

A bounded result needs healthy priming, actual active request histories, independent
controls, exact installation/counter/negative oracle/removal evidence, no-fault
history replay and verified cleanup/state preservation. Attempt and successful local-send gaps include priming and phase tails. Gaps above
five seconds make a continuously active-history claim inconclusive. A successful
local-send/open-control prefix up to a timestamped EOF or send loss can instead
support a bounded association-loss result; no active suffix is manufactured.
The matched no-fault replay must have successful sends and matching responses
throughout the complete attempted history. Successful retained
responses prove only their exact transactions; continued local descriptors alone
are not a live association. A closed association is recorded separately from an
open association with bounded non-response.

Source preparation, offline tests and CI are not live acceptance. Any fix requires
reproducible responsible-layer evidence; changes to external dependency repositories
need separate user authorization. Never patch a module cache as a hidden fix.

## Checkout provenance

Primary checkout: `/Users/romanzhigalkin/Web/razdacha`, branch `main`.
Remote/base: `origin/main`, freshly fetched base
`a62a657b80fbcc25274f2521e1c7838bb8ccfe32`.
Feature checkout: `.codex/worktrees/test-352-active-udp`, branch
`test/352-active-udp`. Root owns live execution and post-merge synchronization.

## Source preparation

The tracked single-owner helper records attempt times, successful local-send
completion times, passive control states and original transaction attribution.
Negative retained checks join the complete received ledger to original sends,
including old IDs and responses delivered after a nominal request deadline.
A pre-recovery request cannot prove a fresh post-recovery transaction.

Thirty-five offline observer/history tests and nine private controller checks pass.
The controller checks cover first-absence clock origin, exact fault revocation
when holder stop fails, capture/probe/ledger failure cleanup, matched worker join
failure and typed generated remote-script syntax. These are offline checks only.
The private executable scenario and raw credentials/captures remain outside Git.
At preparation, root had to bind exact source/provider hashes and independently
review operations before execution. No daemon behavior, dependencies or runtime
configuration changed. The following record distinguishes actual bounded
acceptance from preparation and offline checks.


## Final bounded acceptance — 2026-11-14

### Result and retained-history comparison

One owned RAC WARP outer-endpoint blackout completed with the same primed local
UDP/control sockets and the same client processes retained across setup, fault
and recovery. The active worker made 22 full local datagram sends, with maximum
successful-send gap 3.010489 seconds including priming and phase tails. Passive
SOCKS controls remained open, with no EOF, worker error or observation error.
Local send completion is activity evidence, not remote delivery during the fault:
13 series transactions returned matching responses inside their own request
windows and nine timed out. All nine timed-out IDs were later received as prior
replies at origin -0.872571 to -0.872308 seconds, during the 1.312007-second
absence-status RPC. They were preserved in the ledger and correctly excluded from
the ten genuinely new post-origin requests; no reply was reassigned to a newer ID.
These are observed delayed/buffered replies, not proof of the responsible layer
or exact kernel-removal time.

After the first verified owned rule-absence observation, the worker dispatched
its next transaction at +0.000490 seconds and observed its matching response at
+0.172318 seconds. All ten requests whose sends followed that origin received
matching replies inside the fixed origin-plus-thirty-second window. This finite
case did not reproduce an active retained-association closure or recovery defect.
It neither explains the historical #348 case nor proves the cause/closing hop of
the idle-associated EOF measured in #350.

The no-fault replay used all 22 measured request offsets, identical per-request
timeouts, new transaction IDs and the same phase-tail duration. All 22 requests
had full local sends and matching responses, with maximum successful-send
completion drift 0.009575 seconds (attempt drift 0.009568 seconds). Eleven
concurrent fresh-control rounds returned healthy Direct/WARP STUN responses on
their first attempts. Original and replay histories lasted approximately
64.047275 and 64.052447 seconds respectively; this was a matched active history,
not a long-idle substitute.

### Outage, negative oracle and independent removal

The exact helper fixture was `669f492863a9918ff82402b6529c08f2`, targeting only
RAC OUTPUT traffic to `165.22.196.130:2443`. Other sandbox rules sharing that
endpoint were affected; this was not a per-client fault. A separately armed
thirty-second watchdog preceded the immutable sixty-second holder.

| Evidence | Measured result |
| --- | --- |
| Exact DROP packet counter, installed ready to after negative capture parsing | 0 → 32 |
| Negative capture packets / kernel drops | 138 / 0 |
| Fixed-target direct TCP packets / STUN UDP packets | 0 / 0 |
| Owned visible HTTP/STUN markers or DNS qnames | 0 |
| Other-exit encrypted TCP443 packets in negative capture | 44 |
| Matching retained replies to post-barrier requests while verified installed | 0 |
| Fresh WARP negative requests | No HTTP/TLS target contact; STUN and gateway UDP/TCP DNS timed out |
| Other Direct control | Healthy HTTP, verified HTTPS, STUN and gateway UDP/TCP DNS |
| Independent watchdog cleanup before holder deadline | 28.400665 seconds |

A separate fifteen-packet positive calibration observed seven direct target TCP
packets, including one SYN and the HTTP nonce, plus one owned STUN packet before
the negative interval. The negative capture ended,
was downloaded and parsed, and was followed by a still-installed exact fault
status with an increased counter. Original request sends and the complete received
transaction ledger determined retained negative attribution, including late/prior
IDs. The finite packet oracle is not an all-sites, all-DNS or general leak proof.
Gateway DNS remains an applied/shared-policy observation.

Trusted journal evidence identified the exact root-owned helper watchdog
executable/unit and revoked/table-absent record. Its actor timestamp was
`1876449317887` monotonic microseconds, before immutable holder deadline
`1876477718552`. Cancellation followed verified table absence and stopped fault
PID; no application service was stopped or restarted.

### Recovery timing boundary

The local recovery origin is completion of the first SSH helper-status observation
that verified owned rule absence, not the exact instant of kernel removal. That
status roundtrip was 1.312007 seconds. Therefore the +0.172318-second matching
receipt is measured after confirmed absence; it is not exact removal-to-recovery
latency, an SLA or a general recovery-time guarantee.

Journal verification/cancellation ran after the immediate retained-worker wakeup.
The separate full fresh Direct/WARP matrix started at origin +3.613542 seconds and
finished at +4.283027 seconds, succeeding in one attempt with its own measured
0.669485-second duration. It is distinct from the retained transaction evidence.
The main thread requested worker stop 0.005473 seconds after the fixed thirty-second
classification window; this overrun did not extend the counted reply window.
No Apply, retained-association replacement, client-process restart or application
restart was used for recovery.

### Provenance and finite artifacts

The executed observer/tests source is
`df6a71293ac4deae90435bdbb85afcce40702b63`. Runtime remains RAC
`0.11.0-rc.16.rac.a945231fbf78`, application source
`a945231fbf78659c100df5049b5dc77f6a636839`, sing-box 1.12.25 and schema 13.
The existing standalone fault helper source is
`720fc654a7d44fe58e41a1f96a06e71af0926ebf`; helper/dependency sources remained
unchanged and the fresh isolated namespace selftest passed without host network
mutation. Source safety, private operations and actual finite evidence are reviewed
separately; required final-head CI remains a merge gate.

| Artifact | SHA-256 |
| --- | --- |
| Reviewed nineteen-file source/input manifest | `db3f22d014c63318e72c08d9e216364cd2509fbc08081e05ca537f80a445c0d3` |
| Active history observer | `125e0225c165fc4327c41cefeea6cf0ee9822d7ac412eee23fafe3a3cc16d12b` |
| Retained STUN probe | `a051421147a630c58396862e0e00f94db4d2dac1cafb55cd6aa7bf9aab9586ef` |
| Private controller | `686887f916a944c4b000c201495fe3e1b7be802ee3137cc3875125faf7e72b3e` |
| Standalone Linux fault helper | `aefcd7c9e1c79711b20d7d9023a9f1aad15fb435c198ac4dcef9df58c7e0c665` |
| Native sing-box 1.12.25 client | `ffc693b9edf2f83f3284b94b5e73421555b3f0e2ec166f0193dd6e142e3f4c0d` |
| Complete proof, run `2863ac540fee7e4415018685d6b7a85a` | `ef68065f5cc4eb363dcdd2c55dcca333cd19f7ff6eb89d66aa8eb0c0df9d52d3` |
| Negative pcap, 58,945 bytes | `6b00915f7db2e02b30013532969e8027e24010216917a38e6d9bb76f2e2f14f9` |
| Independent final resource audit | `b2fe051bc5b13faa96697ef231a719080b50239c4622e2b94c465f0c2ccefa76` |

Raw proofs and ledgers remain private under
`/private/tmp/razdacha-udp352/ops/runs/2863ac540fee7e4415018685d6b7a85a/`.
Only aggregates and artifact hashes are committed. Raw exported profiles, client
keys and pcaps are not repository content.

### Protected state, cleanup and next boundary

The complete run has an empty error list and exact before/after protected snapshot
equality. The independent final audit rechecked fresh protected state and exact
helper revocation/table absence. Both owned local client processes, temporary
profiles and session were absent. All five exact fault/watchdog/capture units were
inactive/absent, with no owned active process left.

| Protected object | Unchanged value |
| --- | --- |
| Seven protected user-table SHA-256 | `64f12535ecd4ff925d12ef0b47baf5054006bbaec8b796c0ffc21da7cbd2a950` |
| Checked configuration SHA-256 | `302faf5b71ae4ab6777d7a5c98575fa9ea516713f17171074b0715f91f9d0e14` |
| Applied authority SHA-256 | `d253665bb5ea39cef8b9c5ef37e47221ca8dd3e6eddb76fe4efc02d182c0d8d9` |
| Daemon / sing-box / nginx PID | `269709` / `269726` / `8241` |
| Public ingress guard | `false` |

Ordinary health and owned temporary-session bookkeeping are outside the seven
protected user tables. Production and DO configuration were untouched. No daemon
build/deployment or product fix is justified by this accepted case: only test
observer/specification/context changed, and runtime remains RC16.

Next is a separate reviewed proposal for explicitly approved reserve exits. The
current Direct/WARP ports share one DO host and do not provide independent-host
redundancy. Health alone never grants implicit Direct fallback. This experiment
does not establish Android/router behavior, general UDP/media survival, inner-WARP
failure recovery, DPI resistance, seamless failover or a recovery SLA.
