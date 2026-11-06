# 0039. REALITY open-flow byte observations

Date: 2026-11-06 · Status: **accepted** · Tracker: [#322](https://github.com/ArghTeam/razdacha/issues/322)

## Context

WireGuard cumulative counters do not cover REALITY. The pinned official sing-box
1.12.25 exposes per-flow upload/download bytes for currently open connections,
but no per-user cumulative history. Global controller totals include other inbound
traffic and cannot be attributed to REALITY or combined with WireGuard counters.
This partially supersedes ADR 0038's counts-only/no-byte-payload boundary; its
identity, admission, privacy, freshness and bounded-read requirements remain.

## Decision

Extend the same authenticated `/connections` snapshot with nullable
`open_upload_bytes` and `open_download_bytes` per user. Upload is client to gateway;
download is gateway to client. Sum only flows admitted by the existing dedicated
inbound and singleton matched-rule identity checks. Missing byte fields leave the
corresponding user's byte observation unknown while preserving valid flow counts.
Present malformed, negative or overflowing byte counters invalidate the snapshot.
An admitted user absent from a successful fresh snapshot has zero open flows and
zero open-flow bytes.

Keep one bounded controller read and its five-second cache, with authority checked
before and after observation. Do not persist or accumulate samples. Return no
flow IDs, destinations, public addresses or credentials. WG observations retain
their existing scope.

The panel labels WG cumulative counters separately from REALITY **open-flow**
received/sent bytes in gateway directions. REALITY byte values can decrease when
flows close. Completed short flows, DNS-hijacked/rejected traffic and historical
transfers are outside this observation. A missing/stale eligible client makes the
aggregate unknown instead of silently reporting a partial sum. Reject unsafe
JavaScript integer observations and sums. Header metrics and rows expire together
after thirty seconds of monotonic receipt age, including while a dialog pauses
inventory polling. Counts identify clients with observed open flows, not idle
online presence.

## Consequences

The official runtime, routing policy, logging and credentials are unchanged.
The feature makes current REALITY traffic visible without claiming lifetime or
yesterday's usage. Reliable cumulative accounting requires a separate runtime
instrumentation decision.
