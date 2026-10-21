# Isolated encrypted client connection-quality checks

The opt-in Linux fixture tests encrypted **incoming** WireGuard and the real
nftables/TProxy/sing-box path using synthetic clients and destinations. It does
not load installed configuration, restart services, contact providers, change
production peer settings, or attach a host interface to a test namespace.

## Run boundary

Run only after independent review, as root on a Linux machine with kernel
WireGuard, network/mount namespace support, nftables/TProxy and `sch_netem`.
Missing capabilities fail the explicitly requested check; they are not a pass.
The regular daemon tests skip the namespace test without its explicit opt-in.

Build a static test executable from the reviewed commit:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./cmd/razdachad -o quality.test
sha256sum quality.test
```

Use a trusted, matching sing-box executable. Both environment switches are
required for this mode:

```sh
RAZDACHA_NETNS_TEST=1 RAZDACHA_WIRE_QUALITY=1 \
  RAZDACHA_TEST_SINGBOX=/usr/local/bin/sing-box \
  ./quality.test -test.run='^TestNetworkGenerationWire$' \
  -test.timeout=190s -test.v
```

The supervising test clones fresh network and mount namespaces. The gateway
refuses pre-existing links/routes before mutation, makes mounts private and
mounts bounded private tmpfs filesystems over `/var/lib` and `/run` (hiding any
installed userspace WireGuard sockets). Its own child client and sink
namespaces have no host uplink. Every subprocess is owned by the test and cleaned
up; the gateway and child process timeouts are bounded. WG publication still uses the existing test adapter for address/MTU changes;
this fixture does not prove production key allocation or peer-reconciliation
lifecycle. The two logical client sources share one synthetic encrypted peer.
Synthetic keys remain in
memory; temporary policy/configuration files use the existing private fixture.
Do not override internal `RAZDACHA_WIRE_ROLE` or namespace-marker variables.
Do not combine this mode with other fixture mode flags.

## What a pass demonstrates

- Kernel `wg0` and client `client0` exchange an actual reciprocal encrypted
  handshake over a dedicated `198.51.100.0/30` veth underlay. The original fixture
  keeps its veth-only ingress unless quality mode is requested.
- A recent handshake plus positive, increasing bidirectional WireGuard byte
  counters establish encrypted ingress, rather than inferring it from an
  interface name.
- Client A's selective rules, full selected-exit routing and shared routing run
  through the production guarded publication path. Client B remains a direct
  source-isolation control. A synthetic SOCKS exit stamps responses, independently
  distinguishing selected-exit delivery from direct delivery.
- TCP and UDP payloads of 64, 1200, 1252, 1253 and 1400 bytes round-trip exactly;
  the oracle checks length, content and SHA-256, including source/frame identity.
  TCP is a byte stream and may segment; no TCP packet-size claim is made.
- With the default WireGuard MTU of 1280, IPv4 UDP's 28-byte IP/UDP overhead puts
  1252 bytes at the inner boundary. Both client and sink UDP sockets use `IP_PMTUDISC_DONT` for the matrix,
  allowing inner IPv4 fragmentation/reassembly above that boundary. With
  `IP_PMTUDISC_DO`, a 1253-byte UDP write must return `EMSGSIZE`; disabling DF must
  restore a successful exact exchange. The fixture is an integrity check, not a
  claim that large UDP datagrams avoid fragmentation.
- Each selected routing mode keeps one TCP and one connected UDP socket open for
  40 exchanges with 50 ms pacing (at least two seconds per protocol), then opens
  separate sockets to prove reconnect. This is bounded continuity coverage, not
  a long-duration soak or a throughput benchmark.
- Disabled and unreachable exits remain rejected with successful other-client
  controls. The independent sink's capture barriers and AF_PACKET drop accounting
  continue to reject forbidden direct-delivery markers. Quality mode uses a
  single-member `PACKET_FANOUT_FLAG_DEFRAG` group so the kernel reassembles inner
  IPv4 fragments before the datagram oracle; unreassembled fragments are errors,
  and neither collector errors nor kernel packet drops are ignored.
- A disposable gateway-underlay `netem` qdisc applies deterministic 100% outbound
  encrypted-packet loss. A bounded UDP receive timeout must coincide with an
  increasing qdisc drop counter. Removing only that test qdisc must restore
  persistent TCP/UDP traffic and direct controls. Collector packet drops remain
  failures; deliberate qdisc drops are separate impairment evidence.

Look for `266-QUALITY-STAGE-PASS`, `266-QUALITY-IMPAIRMENT`,
`266-QUALITY-LOSS-RECOVERY-PASS` and `266-ENCRYPTED-QUALITY-PASS`, alongside the
existing egress completion evidence. Compilation alone is not runtime evidence.

## Limits

Outgoing exits here are synthetic SOCKS transports, not real remote WireGuard
providers. A pass does not establish real-provider UDP reachability, external
path MTU, DNS correctness outside the fixture, Google Meet/WebRTC behavior,
congestion handling, randomized loss/jitter tolerance, multi-hour stability or
performance. Those require a separately scoped client/provider run. The fixture
never accepts real credentials or real provider profiles.
