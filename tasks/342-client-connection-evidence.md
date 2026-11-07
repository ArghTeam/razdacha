# Client connection evidence (#342, roadmap #338 milestone 1b)

## Intended behavior

Authenticated no-store selected-client ingress observations and independent exit
HTTPS observations follow [ADR 0043](../docs/decisions/0043-client-connection-evidence.md).
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
