# Active retained UDP recovery across a REALITY exit outage (#352)

Status: Preparation; live acceptance is pending. Tracker: [#352](https://github.com/ArghTeam/razdacha/issues/352).

## Goal and scope

Separate active-flow outage recovery from the idle confound measured in
[task #350](done/350-retained-udp-recovery.md). The historical #348 and #350
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
Root must bind its exact source/provider hashes and independently review it
before live execution. No daemon behavior, dependencies or runtime configuration
changed in this preparation; live failure/recovery acceptance remains pending.
