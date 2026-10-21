# 0031. Scoped tunnel observations

Status: Accepted. Clarifies [0011](0011-tunnel-check-schedule.md).

An HTTPS delay failure cannot identify a WireGuard handshake, DNS, TCP, TLS or
remote-service failure. Diagnostics must not infer transport failure from it.
Individual checks use an IP-literal HTTPS target with normal certificate
verification, so hostname rejection does not determine their result. Runtime DNS
is observed separately: this describes the runtime resolver, not every tunnel.
Pool selection and its test URL are unchanged.

The probe budget is fifteen seconds plus three seconds of controller slack.
A shared broker coalesces by tag, permits two active jobs and 32 pending identities.
Caller cancellation stops waiting, not the runtime job. Failed requests retain
capacity through the budget because pinned runtime jobs ignore caller cancellation.

Observations expose scope, time and freshness. Transport remains unknown without
independent evidence. HTTPS success proves a round trip, not all protocols or
clients. Indeterminate failures cannot trigger transport-down alerts. Group results
retain history timestamps. Partial/stale observations cannot report all healthy.

The user authorized issues 218 and 166 together and production validation after
independent review, superseding previous after-217 ordering. Policy is unchanged.

Runtime DNS observations share one coalesced job for the fixed diagnostic name.
Canceling a waiter does not cancel that job. Uncertain controller failure retains
its identity through the pinned runtime's ten-second DNS budget, preventing
cancellation or short HTTP deadlines from accumulating detached resolver work.
Pool confirmation compares history timestamps at persisted whole-second precision;
replaying one fractional timestamp cannot establish repeated recovery evidence.

The uncertain DNS lease includes the two-second HTTP dispatch budget in addition
to the ten-second runtime timer (twelve seconds total from local admission).
The runtime timer may start near the HTTP deadline; releasing at ten seconds from
local start could otherwise overlap a still-running resolver job. A regression
checks that the original job identity remains retained beyond ten seconds.
