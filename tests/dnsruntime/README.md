# Pinned DNS runtime and isolated wire regression

This nested test module intentionally keeps sing-box runtime dependencies out of
the daemon module. It pins sing-box v1.12.25 and imports the actual project generator.

```sh
go test ./... -count=1
go test -race ./... -count=1
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o /private/tmp/razdacha-dns-runtime.test
```

`TestGeneratedDNSRuntime` uses actual local rule sets and `DNSRouter.Exchange`, a
real loopback UDP upstream, and a synthetic FakeIP transport. It covers manual and
plain CIDR/domain/mixed/empty/missing sources, native JSON/SRS CIDR/domain/mixed sources,
block/unavailable/live/direct actions,
real resolution, selected/unassigned peers and unrelated questions. The synthetic
FakeIP transport verifies action selection, not the persistent FakeIP allocator.

`TestDNSProjectionWire` is opt-in Linux/root verification. Only after independent
review, run the compiled binary with `RAZDACHA_NETNS_TEST=1` and an absolute
`RAZDACHA_TEST_SINGBOX` path to the pinned binary. The fixture first clones network
and mount namespaces, verifies both differ from its caller, makes mount propagation
recursively private and hides `/var/lib` with tmpfs. It creates only synthetic veth
clients and sinks with a contained default route. It starts its own checked sing-box
process and never calls systemctl or accesses a real key/database/configuration.

Actual nft/tproxy steering protects selected CIDRs; AF_PACKET at the sink records
forbidden TCP/UDP probes and escaped port-53 traffic independently of socket errors.
After all probes, a fresh UDP nonce to the permitted sink must be acknowledged by
the same raw collector. The gateway waits until its output scanner receives that
barrier before inspecting forbidden reports; an echo response or fixed delay does
not establish observer completion. The raw socket is bound to the disposable sink
interface; nonce decoding checks IPv4 header/total lengths and UDP payload bounds.
Collector errors or unexpected output termination fail evidence collection. This
drains frames queued before the barrier, not packets sent after the observation
interval. Verbose mode propagates to the isolated gateway
so all eight subtests are visible. Assertion helpers take the current subtest's
`*testing.T` explicitly, so an expected baseline failure does not abort the parent
matrix. The global success summary is emitted only after
the raw barrier and absence checks pass and no subtest has failed.
The fixture verifies unrelated DNS over TCP/UDP, domain rejects, permitted traffic,
failed validation, prior checked config/nft restoration and restart. This is a
standalone DNS prerequisite fixture, not #214 publication-order evidence. Missing
sources retain the pre-existing generator diagnostic/skip behavior; #214 owns safe
publication and last-good generation preservation.

Use the staging supervisor required by `docs/11-staging-validation.md` for bounded
process-group cleanup. No source-host access, host service changes or external
traffic is permitted. Public evidence contains only synthetic assertions. Independent final acceptance
remains a required merge gate.
Attempt 1 on `bc013e7` stopped after one baseline failure because helpers captured
the parent test; seven baseline cases and all fixed cases were not run. That
attempt is failed evidence, not a completed baseline or fixed wire validation.

Attempt 2 on `2c18017` completed all eight original-base cases with the expected
permitted-DNS REFUSED failure and all eight fixed cases with PASS. The checked
source/artifact identities, cleanup and scoped observations are recorded in
[the completed task](../../tasks/done/222-dns-rule-scope.md). No product deployment
occurred. The nonce barrier drains queued observations only: without
`PACKET_STATISTICS`, these checks make no zero-drop, lossless or universal no-leak
claim. They are not #214 publication-order or actual FakeIP-allocator validation.

`TestNativeOriginalAndProjectionRuntime` compares actual local native JSON and SRS
versions 1/2/3 against exact DNS plus nft projections, including default domain+IP OR,
pure AND/NOT, regex empty-domain behavior and false address expressions.
`TestNativeUnsafeRuntimePredicatesRejected` proves mixed-AND, source-scope and
negated-domain hazards and a noncanonical compiled SRS marker/Dump discrepancy.
Native empty/missing sources are compile-time refusals, not successful wire cases.

## Authenticated REALITY capability proof

`TestRealityIngressIdentity` is an opt-in capability fixture for
[task #297](../../tasks/297-reality-runtime-proof.md). It uses a supplied sing-box
**1.12.25** executable, generated disposable X25519 keys/UUIDs, a local TLS 1.3
ECDSA/X25519 handshake target, and loopback-only client/server listeners. It never
loads installed configuration or calls systemctl. Private fixture files are mode
0600 under temporary directories; failure diagnostics redact UUID/key-shaped values.

```sh
RAZDACHA_REALITY_TEST=1 RAZDACHA_TEST_SINGBOX=/absolute/path/to/sing-box \
  go test -mod=readonly -run '^TestRealityIngressIdentity$' -count=1 -v -timeout=90s
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go test -mod=readonly -c -o /private/tmp/razdacha-reality-proof.test
```

The executable must include `with_utls`; the Go test binary itself needs no extra
build tags. Verify the executable's trusted source/hash separately: its version
string is a compatibility check, not executable attestation. The fixture is skipped
unless explicitly enabled. Normal local execution creates only loopback sockets.

For a Linux staging run, an independent reviewer must approve the fixture first.
Use a bounded supervisor/process group in fresh network and mount namespaces, as
required by the staging runbook. Before `unshare`, privately capture the caller's
`/proc/self/ns/net` symlink and pass it as `RAZDACHA_REALITY_PARENT_NETNS`. The
fixture refuses matching namespaces and requires that the new namespace contain
only `lo` before bringing that interface up through netlink. The supervisor owns
cleanup on abrupt test timeout; ordinary test cleanup stops and reaps child runtimes.
Keep installed runtime state hidden and supply the executable explicitly. Do not
expose any fixture listener publicly or use real client/tunnel keys.

The fixture checks two named users from the same loopback source, different TCP/UDP
sinks and DNS answers after hijack, unknown UUID/unnamed-user/plaintext/wrong-key
rejection, working fallback observers remaining unused, and closure of a positively
established TCP flow when the server process exits. This proves engine hooks using
handwritten fixture policy, not integration with the product generator, publication
guards, persistent authorization/revocation, WG coexistence, restoration or any
particular client/router application. Negative sink observations cover the bounded
fixture window; this is not a packet-capture proof of universal absence of leaks.
No external network, DPI resistance, IPv6/private-destination protection, throughput
or arbitrary UDP application/WebRTC behavior is established by this fixture.
