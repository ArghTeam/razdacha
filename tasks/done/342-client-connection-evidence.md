# Client connection evidence (#342, roadmap #338 milestone 1b)

## Intended behavior

Authenticated no-store selected-client ingress observations and independent exit
HTTPS observations follow [ADR 0043](../../docs/decisions/0043-client-connection-evidence.md).
The browser owns a 90-second activity window with serial polls at least five
seconds apart and 30-second local expiry. No server window registry exists.

## Acceptance

- Strict selected-client metadata; nullable safe WG counters/handshake or
  authenticated current-open REALITY totals, source time and opaque epoch.
- Before/after I/O bind checked released publication, live transport admission,
  publication lifetime and process boot identity.
- Exit selection uses applied conditional domain TCP/443 policy. Unknown/rejected
  outcomes never probe. Explicit Direct, full chains and existing pool history
  remain distinct; reuse one shared fixed IP-literal HTTPS broker, no DNS queries.
- No keys, configs, endpoints, destinations, flow IDs or raw runtime errors;
  no policy/auth/config writes.
- Deterministic API, publisher and browser lifetime tests, scoped race checks,
  complete Go checks and Linux lint precede review. Controlled RAC acceptance is
  recorded separately by the primary agent before parent closure.

## Delivery boundary

Backend #343 owns Go/API and this contract; the UI component owns panel assets
and UI context. The parent remains open until both components and bounded RAC
acceptance complete. No production or DO operations are included.

## Backend component verification

Component #343 passes the complete `CGO_ENABLED=0 go test ./...` suite,
`go vet ./...`, a static Linux amd64 build and the project-pinned official
`golangci/golangci-lint:v2.12.2` Linux image (zero issues). Focused API, publisher,
cache and broker tests also pass with `-race`, including source I/O outside the
gate, same-byte Apply and boot identity, WG admission ABA, applied-versus-pending
exit selection, full chains, unknown DNS, historical pool timestamps and shared
probe lifetime fences. This is synthetic backend evidence; UI and actual RAC
acceptance remain separate parent requirements.


## Final acceptance — 2026-11-07

Status: Complete for roadmap #338 milestone 1b. Backend [PR #346](https://github.com/ArghTeam/razdacha/pull/346)
and UI [PR #345](https://github.com/ArghTeam/razdacha/pull/345) are merged into main.
The functional main commit is `c89a6dd653e8051bcbda03e210488a46c4156fb7`.
The designated primary checkout `/Users/romanzhigalkin/Web/razdacha` was safely
fast-forwarded to the fresh remote target after each confirmed merge; merged
commits are reachable and existing user edits were preserved.

### Source, review and artifacts

The RAC integration source is `a945231fbf78659c100df5049b5dc77f6a636839`;
its backend composition `51e90e611b5e95ab9bf8663f71c4de575819a551` preserves
both existing onboarding and observation authority. Independent exact-head
reviews approved the components and final composition without material findings.
The RAC-only integration PR #306 remains a draft and was not merged into main.

Component and final combined CI passed Go tests/vet, lint, Linux amd64/arm64
builds, pinned sing-box configuration checks and synthetic browser regressions.
The combined backend passed the complete CGO-disabled Go suite and focused
API/controller/daemon race tests. The subsequent integration changes only UI and
its context; all 21 combined browser suites passed, including 54 actual-module
synthetic Connection evidence cases. Initial local cache, loopback and Chromium
sandbox denials were retried with authorized local permissions; they are not
reported as product failures.

Four static Linux daemon/CLI artifacts were built from the clean frozen integration
source. Process-local Git metadata attests the exact source with `vcs.modified=false`;
a local synthetic authenticated API/CLI probe separately verifies linked metadata.
The deployed amd64 artifact SHA-256 values are:

- `razdachad`: `9c212b73892c8482633cddf54a2c2371d4b1034070f3d3ac5320879a825d9fe5`
- `razdacha`: `37da787e164b72e07ef85a3da36f8516ad998b410ee1422ef52d9fbab4bc64c6`

### RAC deployment and actual acceptance

RAC runs `0.11.0-rc.16.rac.a945231fbf78` at the recorded full integration source.
The binary-only replacement used normal guarded startup, restarting sing-box and
briefly interrupting connections. The daemon, sing-box and nginx returned active;
the ingress guard was literally false. No setup/reset, database restore, client
mutation, explicit Apply request, WARP registration or tunnel reconfiguration was performed.

Before/after comparisons preserved the seven protected user tables (settings,
peers, tunnels, rules, REALITY identity/credentials and WARP registrations), exact
sing-box configuration and applied network authority. All four existing clients
and their keys, selected exits and rules remain; schema version is 13. Only after
actual proof, `installed_version` was compare-and-set from RC15 to RC16 while all
other protected rows remained equal. API running/installed metadata then matched.
Production and DO services/configuration were not accessed or changed; test traffic
used the already configured DO exits.

The first bounded live acceptance attempt passed using the current exported
profiles of the two existing owned REALITY-only test clients. Only their local
inbounds were replaced with loopback mixed listeners; exported DNS, routing and
outbounds stayed equal. No TUN or machine route/DNS change occurred. Retained HTTPS
connections and valid UDP STUN exchanges traversed RAC. Three source observations
at least six seconds apart retained one epoch per client, distinct source times,
nonzero current-open counts/bytes and an observed current-total byte increase.
DO Direct returned `165.22.196.130` with `warp=off`; DO WARP returned
`104.28.219.140` with `warp=on`. These are point-in-time exit observations.

Separate selected-client fixed-target HTTPS checks succeeded through each applied
exit. Domain normalization, invalid fields/URLs/IPs, missing clients and disabled
WG observations were also verified. The actual served Clients row action showed
REALITY as the enabled default, source counters, applied exit names and separate
HTTPS results. Two serial polls per client and two explicit exit checks completed;
closing stopped later polls. Desktop/mobile screenshots showed no horizontal
overflow. The browser blocked exports, arbitrary target navigation, other writes
and probes outside the two selected owned clients; no unexpected request or page
error occurred. The optional public release check was deliberately suppressed.

Owned sockets, child clients and browser processes were closed; temporary profile
copies and the owned temporary authenticated session were removed. Passwords and
existing sessions were not replaced. Private proof artifacts remain under
`/private/tmp/razdacha-client-evidence-20261107/`; no credential/profile material is
committed to the repository.

### Limits and next phase

Live acceptance proves these two controlled REALITY paths and the served evidence
workflow. Positive real WG activity was not generated; its lifetime/counter logic
has synthetic coverage and disabled ingress was checked live. Runtime pool history,
full chains, arbitrary user devices, Android behavior, carrier DPI, selected-site
success, client RTT, bandwidth, loss, jitter and media quality were not established
by this acceptance. Current-open totals can decrease or miss short completed flows;
zero or unchanged values do not establish inactivity or an offline device. Fixed
target HTTPS timing is separate from client activity and selected-site success.

Milestone 1b is complete; the umbrella roadmap remains open. Revocable scoped
subscriptions (phase 2) require their own specification and review.
