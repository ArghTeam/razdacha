---
type: task
tracker: "#297"
title: Isolated encrypted WireGuard and REALITY coexistence proof
status: review
---

# Isolated encrypted WireGuard and REALITY coexistence proof

## Checkout and ownership

- Authorized repository and designated primary: `/Users/romanzhigalkin/Web/razdacha`.
- Remote `origin`, target `main`, freshly fetched base
  `b46be7f2dff567fb72bd9d788d64bfc91eff7100`.
- Worktree: `.codex/worktrees/test-297-wg-reality-coexistence`;
  branch: `test/297-wg-reality-coexistence`.
- Owned changes: one new Linux coexistence fixture, its minimal role/exercise
  dispatch in `network_fixture_linux_test.go`, and this record. The backend
  implementation remains a separate reviewed dependency.

## Acceptance

Reuse the opt-in encrypted quality topology, its reciprocal disposable kernel WG
keys, private network/mount namespaces and independent sink/drop accounting. Start
from successful WG traffic, enable generated REALITY through the application store
and publication gate, and run authenticated TCP, UDP and DNS requests concurrently
with WG probes. Require a recent real encrypted WG handshake and increasing
bidirectional counters before and after the concurrent period.

The client wrapper derives its VLESS outbound and private credentials from the
application's delivered JSON. Its test-only `bind_interface=under1` selects the
direct documentation-address underlay instead of the existing WG default route;
wrapper interface autodetection is disabled for that explicit binding.
Two local direct inbounds supply controlled echo and gateway-DNS destinations.
This does not change or claim to run the complete delivered TUN profile; that has
a separate frozen proof. The fixture's direct engine reloader does not exercise
systemd packaging or service hooks.

No provider, installed state, operator route or existing tunnel is used. All keys,
TLS/DNS/echo services and namespaces are synthetic. Runtime execution requires
independent source/binary review and a bounded outer cgroup with whole-group
cleanup; a Go timeout alone does not run test cleanups.

## Intended invocation

```sh
RAZDACHA_NETNS_TEST=1 RAZDACHA_WIRE_QUALITY=1 \
RAZDACHA_WIRE_REALITY_COEXIST=1 RAZDACHA_TEST_SINGBOX=/absolute/pinned/sing-box \
  go test ./cmd/razdachad -run '^TestNetworkGenerationWire$' -count=1 -v -timeout=180s
```

The execution records below distinguish failed and successful frozen images.
Separate WG and REALITY proofs do not by themselves establish concurrent
coexistence.

## Frozen pre-execution checkpoint

The reviewed backend dependency is
`c850fb433a32df6a9da732faa6dbdfe3123e5544`. The source checkpoint passed ordinary
daemon tests, Linux-target compile/vet, pinned golangci-lint 2.12.2 with `GOOS=linux`
(zero issues), and `git diff --check`. No namespace fixture was executed locally.

- New fixture SHA256:
  `dc40633f24c99ff228cb9c9dcb50988b3f722332756d21eed9042fa99036189c`.
- Role/exercise dispatch source SHA256:
  `29e75aa07dcbf98c9b7d57fa37448c3cc509d3282e3812767a9cf40a7f48d3d6`.
- Static Linux amd64 test executable SHA256:
  `e7bad5424f084fa6aac9e92e7c9dc3625c05c8be18e6910c68a0046d39194e11`.

The primary operator owns independent pre-execution review, bounded namespace
execution and the subsequent result.

### First isolated attempt

The reviewed `e7bad542...` executable failed safely after 0.59s at public ingress
staging: the numbered `testing.TempDir` child was not mode 0700. The production
authority check correctly rejected that fixture directory. The test now explicitly
sets its owned state directory to 0700 before activating live authentication; no
production permission check was changed. Existing installed files/service PIDs
were unchanged and no test process/cgroup remained.

### Corrected isolated proof (2026-11-04)

The primary operator ran the independently reviewed corrected fixture with the
pinned sing-box 1.12.25. It passed in 5.31s. Repeated authenticated REALITY TCP/UDP
echo and TCP/UDP DNS controls ran concurrently with the actual kernel WG quality
matrix and persistent flows. The encrypted WG peer retained its public key and
advanced both receive and transmit counters; the owned sink/drop checks passed.

- Corrected fixture SHA256:
  `efb831e86a7f65e2feaae32b589b58cf7773bf9bb496174b5b7f11ef09f09cbf`.
- Dispatch source SHA256 remained
  `29e75aa07dcbf98c9b7d57fa37448c3cc509d3282e3812767a9cf40a7f48d3d6`.
- Executed static Linux amd64 test image SHA256:
  `bc8deb0151ad19e68296a6938902f87e94512f91d47035c842da45f0b66bbdef`.

The bounded supervisor verified unchanged installed daemon/runtime/config hashes
and all three existing service main PIDs, plus no remaining owned processes or
cgroup. Raw operational evidence stays private. No configured provider tunnel
was contacted or changed.

The WG address/MTU publication adapter and shared synthetic encrypted peer retain
the existing quality-fixture boundary. The REALITY client is a profile-derived
wrapper explicitly bound to `under1`, rather than the complete delivered TUN JSON.
The direct engine test reloader does not test systemd or boot hooks. No external
provider, actual DPI, native client app, production WG-key allocation/reconciliation
or performance result is claimed.

### Final source integration

After that frozen execution, the branch fast-forwarded to freshly fetched
`origin/main` `5158748b5d1e2a1fec4b842851f200933a168fa2`, including the reviewed
restore-only follow-up `5ec522bdacf3151962516f68ee1371885257ed97`. Those changes do
not alter the fixture source, generated runtime/compiler or coexistence protocol.
The execution above remains pinned to its actual `bc8deb...` image; a subsequent
final-source build is not relabeled as that executed binary.

Final-source verification passed affected ordinary daemon tests (19.17s),
Linux-target compile/vet, pinned golangci-lint 2.12.2 with zero issues and diff
whitespace checks. The primary operator coordinates final review, CI and merge.
