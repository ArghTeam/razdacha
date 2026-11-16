# Authenticated REALITY flow activity

Tracker: #312. Decision: [ADR 0038](../../docs/decisions/0038-authenticated-reality-flow-observations.md).

## Scope and checkout

Authorized repository: razdacha and its worktrees only. Primary checkout:
`/Users/romanzhigalkin/Web/razdacha`; remote/base `origin/main`, freshly fetched
`93bf487cd9242993bfa5f216edaba6b30fdbfa66`. Feature worktree:
`.codex/worktrees/feat-312-reality-activity`, branch `feat/312-reality-activity`.
No production, cloud resource mutation or legacy tunnel activation.

## Acceptance

1. Attribute open flows only to the exact authenticated admitted user, including
   independent clients behind one public address.
2. Preserve direct/tunnel TCP/UDP policy, guard behavior and WG.
3. Bound and coalesce observations; stale/error/malformed/unsupported remains
   unavailable. Discard reads spanning publication or revocation.
4. Show open-flow counts separately from WG counters without claiming idle online
   presence or REALITY byte accounting.
5. Explain gateway DNS and full-profile import limitations.
6. Verify parser isolation, lifecycle races, browser states and rule expansion.

## Validation

- Complete `CGO_ENABLED=0 go test ./...`: passed.
- Linux golangci-lint 2.12.2: zero issues. Focused API/Clash race checks: passed.
- All 17 embedded Chromium suites: passed, including eight new activity cases
  covering separate WG/REALITY states, clock skew, modal-safe expiry and pending/
  uncertain peer and global authentication changes.
- Official sing-box 1.12.25 loopback fixture: two authenticated VLESS users sharing
  one source address each retain separate TCP and UDP counts; selected/direct
  egress is checked independently. Only the TLS wrapper and fixture egress dial
  targets are substituted; no REALITY handshake or Android TUN startup is claimed.
- Existing pinned-runtime numeric TLS/HTTP routing checks still pass.
- Representative 100 clients × 100 rules: 10,211 route rules; compact runtime
  fixture 1,848,764 bytes. Native check took approximately 0.43–0.48 seconds.
  Generation plus pretty serialization benchmark: approximately 376 ms and
  321,712,272 cumulative allocated bytes, not retained memory or RSS.
- Standalone compiled scale test under macOS `/usr/bin/time -l` (excluding the Go
  compiler): maximum resident set 111,468,544 bytes, peak memory footprint
  75,276,288 bytes, zero swaps. This is the OS-reported test-process measurement,
  not total concurrent host memory under real traffic.

Synthetic browser and loopback tests do not establish Android DNS capture; device
confirmation and sandbox deployment remain separate. Publication invalidation,
changed config/guard rejection and restricted legacy fallback rejection are
covered by daemon tests. Maintained API, UI and singbox invariants/logs accompany
the implementation. The branch merged fresh `origin/main` at
`2be4de4111d27d94dda4177d9fbb0000b43e0a2f` after the dependent TCP-routing fix.
