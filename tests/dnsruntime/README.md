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
plain CIDR/domain/mixed/empty/missing sources, block/unavailable/live/direct actions,
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
so all eight subtests are visible. The global success summary is emitted only after
the raw barrier and absence checks pass and no subtest has failed.
The fixture verifies unrelated DNS over TCP/UDP, domain rejects, permitted traffic,
failed validation, prior checked config/nft restoration and restart. This is a
standalone DNS prerequisite fixture, not #214 publication-order evidence. Missing
sources retain the pre-existing generator diagnostic/skip behavior; #214 owns safe
publication and last-good generation preservation.

Use the staging supervisor required by `docs/11-staging-validation.md` for bounded
process-group cleanup. No source-host access, host service changes or external
traffic is permitted. Public evidence contains only synthetic assertions. Staging
execution and independent final acceptance remain pending until explicitly recorded.
