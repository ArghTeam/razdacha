# REALITY domain denial before upstream resolution

Issue: #329; umbrella: #324. Base: origin/main
`65d611db6f5e075888cb446a9905fc1736a72909`. Design: ADR 0041.

Add bounded reject-only preflight before shared REALITY resolution, after admission,
DNS and whole-client rules. Reuse the same compiled domain projections and source/
availability outcomes. Preserve positive singleton route identities, ordinary DNS
and FakeIP options, numeric sniffing, original full matchers and private guards.
Earlier applicable positive domain matches defer rejection; any possible earlier
positive address branch conservatively defers it for that authenticated scope.
Unknown native summaries count as address-capable. Fail generation before guard
on construction budget exhaustion. This does not change captured-DNS C1 priority.

Acceptance requires static ordering/scope/frozen-read/budget checks and pinned
1.12.25 controlled upstream-question/sink counters, including retained FakeIP,
positive ordering, private answers and ordinary DNS equivalence.

## Verification

- `go test ./...`: passed on Darwin; Linux generator/daemon lint passed.
- Pinned `sing-box 1.12.25` controlled VLESS proof under Go race detection:
  explicit manual/plain/native/OR block and unavailable/empty policies each send
  zero cold upstream questions for both TCP and UDP and reach no terminal sink.
- Earlier domain positives preserve scope and exit; CIDR/context barriers permit
  one cold query per protocol and preserve the original full matcher decision.
  Private/loopback/metadata/local answers reject after lookup for both protocols.
- Retained gateway FakeIP survives a checked runtime restart across deny policy
  publication; new TCP/UDP payloads send zero upstream questions and reach no sink.
  An unselected user then resolves that exact retained FakeIP and reaches the
  direct sink, excluding the missing-record rejection false positive.
- Ordinary captured TCP/UDP A, AAAA and HTTPS controls preserve real/FakeIP/reject
  behavior; structural comparison preserves the complete ordinary DNS options.
- Existing numeric TCP/sniff and singleton activity pinned fixtures pass with race
  detection. A mixed 100-client/100-rule candidate has 5,311 route entries and
  2,655,976 indented JSON bytes; pinned runtime check took 0.56 seconds locally.
  These finite synthetic results establish no host/client/DPI or universal DNS claim.
