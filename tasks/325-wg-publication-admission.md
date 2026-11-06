# 325: WireGuard admission joins guarded publication

Issue: https://github.com/ArghTeam/razdacha/issues/325
Umbrella: https://github.com/ArghTeam/razdacha/issues/324
Finding: F02 (P1)

## Goal and acceptance

Implement ADR 0040: synchronize current live WireGuard credentials before guarded
publication releases source policy or address reservations. Serialize the complete
periodic database-read/kernel-write operation with peer mutation and publication.
Never replay credentials from committed policy. Keep ingress closed on guarded
reconciliation failure and preserve unchanged-peer minimal diffs.

## Checkout and scope

Primary checkout: `/Users/romanzhigalkin/Web/razdacha` (`main`).
Remote/base: `origin/main`, freshly fetched `8dc7cdbe50d6b6639d06cabfc7890fa75d7172cf`.
Feature checkout: `.codex/worktrees/fix-325-wg-publication-admission`.
Branch: `fix/325-wg-publication-admission`.
Only daemon WG/publication wiring/tests and maintained netstack context are owned.
No host network changes, production access, API/store changes or interface/key migration.

## Verification

Require deterministic deleted-key/release ordering, live-read and sync failures,
paused periodic-read serialization and committed-policy non-replay regressions.
Run daemon/netstack tests with race detection, relevant static checks and a Linux
build. Privileged Linux packet validation is reported separately from unit checks.

## Isolated encrypted fixture

Only after independent review, run as root on Linux with kernel WireGuard,
network/mount namespaces, nft/TProxy and a trusted matching sing-box executable.
The normal suite skips the opt-in fixture. Never override namespace markers or
combine it with another fixture exercise. No installed keys/interfaces are used.

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./cmd/razdachad -o wg-admission.test
RAZDACHA_NETNS_TEST=1 RAZDACHA_WIRE_QUALITY=1 RAZDACHA_WIRE_WG_ADMISSION=1 \
  RAZDACHA_TEST_SINGBOX=/usr/local/bin/sing-box \
  ./wg-admission.test -test.run='^TestNetworkGenerationWire$' -test.timeout=190s -test.v
```

The scenario uses fresh synthetic DB credentials, a private documentation-only
underlay and sink without a host uplink, and private tmpfs runtime directories.
It uses the production interface/live-peer callback, verifies encrypted private
TCP/UDP delivery before deletion and while pending, unchanged periodic handshake,
removed kernel key before guard release, then old-key TCP/UDP denial to formerly
selected and ordinary destinations. Sink controls and forbidden nonce capture
check the negative probes independently. This is a bounded deletion/publication
check, not an external-provider or production-installation lifecycle proof.

## Local verification results

- `go test ./...`: passed on Darwin.
- `go test -race ./cmd/razdachad ./internal/netstack`: passed.
- Linux amd64 static daemon test executable: compiled successfully.
- Linux-targeted daemon/netstack golangci-lint: passed with zero issues; an initial
  shared-cache warning prompted a rerun with a task-private cache.
- `git diff --check`: passed.
- Privileged encrypted fixture: compiled, not run on Darwin. A Linux run requires
  independent review and the explicit isolation conditions above.
