# 0043: Client connection evidence

Status: Accepted

Extends ADR 0031, ADR 0038, ADR 0039 and ADR 0042; supersedes none.

## Decision

Expose authenticated no-store POST `/api/clients/observe` for one selected client
and ingress. WG receives/transmits cumulative encrypted peer bytes at the gateway;
REALITY observes authenticated current-open flows and nullable byte totals.
Zero, missing, reset, unsafe, future and stale values remain distinct. Open-flow
closures and decreasing totals do not establish absence of traffic. No application
throughput, client latency, loss, jitter, site success or quality score is derived.

Private before/after authority binds checked configuration and released manifest,
current client/transport eligibility, publication activity lifetime and a random
process nonce. The public ADR 0042 marker alone cannot cover same-byte Apply or
restart. Narrow metadata reads use store route eligibility; existing trusted
REALITY observation retains its internal private live-admission validation.
No networking occurs under the publication mutex. One coalesced WG source job
retains capacity through cancellation; REALITY reuses its existing bounded cache.
No server activity-window state exists.

Independent POST `/api/clients/check-exit` accepts only a client, ingress and domain.
Applied ADR 0042 conditional TCP/443 outcome selects a private outbound internally.
Unknown, reject or unadmitted outcomes never probe; Direct must be an explicit
applied action. Chains use their complete compiled tag. Plain pools consume
existing timestamped group history without fan-out. Other known exits reuse the
ADR 0031 fixed IP-literal HTTPS broker and existing capacity leases. The supplied
domain is never contacted or resolved. No legacy global check-cache,
authentication, policy or profile state is written. Generic failure stays unknown.

## Browser and verification boundary

The browser owns a 90-second guided window, serial polls at least five seconds
apart and expiry after 30 seconds of monotonic elapsed time plus source freshness.
Comparisons require equal epochs and increasing source times. Context, session,
modal and navigation changes invalidate callbacks and baselines. Ingress and
gateway-to-one-fixed-target HTTPS are separate scopes. User-reported site success
is an assertion, never a server proof. Controlled owned RAC acceptance is separate;
synthetic tests cannot establish live client, carrier, all-site or media quality.

Pool history remains historical and unbound to the current publication even when
its source timestamp is recent; the response epoch binds current selection only.
Active probes preserve private monotonic broker admission and shared completion
times. Only jobs admitted after coherent request authority can supply a bound
result; preexisting jobs stay shared but return unknown rather than being relabeled.
