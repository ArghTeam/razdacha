# #322 — REALITY open-flow traffic observations

## Goal

Show trustworthy REALITY observations alongside WireGuard statistics without
inventing cumulative usage from current connections. Follow ADR 0039.

## Checkout identity

- Designated primary: `/Users/romanzhigalkin/Web/razdacha`, branch `main`.
- Remote/base: freshly fetched `origin/main`, `776fd98d8ef3f4aca127f61dc2581bdae35c908c`.
- Feature checkout: `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/fix-322-reality-statistics`.
- Branch: `fix/322-reality-statistics`.
- Existing primary edits, deployed candidate and hosts are outside this change.

## Acceptance criteria

- The existing bounded authenticated snapshot supplies nullable open-flow upload
  and download byte sums per admitted user. Global and WG counters are excluded.
- Missing fields preserve counts but leave their byte direction unknown;
  malformed, negative and overflowing counters never become measured zero.
- Repeated snapshots are not accumulated; closing flows can reduce observations.
- Separate header cards identify WG cumulative versus REALITY current-open-flow
  scope and gateway directions. REALITY client counts require fresh open flows.
- Rows and header expire together while polling is paused without replacing modal
  contents, focus or drafts. Auth recovery suppresses stale observations.
- Backend and synthetic browser checks cover direction, isolation, compatibility,
  closure, malformed input, overflow, epoch/admission and paused-poll expiry.
- Maintained API/UI context documents the final contract. No runtime rebuild,
  public listener, network policy or credential change is introduced.

## Verification

- Implementation verification is complete within the checks below. PR merge and
  RAC candidate integration/deployment remain primary-agent work; this record
  does not claim an updated running sandbox or authorize a release.
- Full `go test ./...` and focused race-enabled API/controller tests passed.
- Linux-target project lint passed with zero issues.
- Actual embedded-module Chromium activity regressions cover directions, separate
  transport scopes, old missing byte fields, incomplete/unsafe aggregates,
  nonaccumulation/closure and header/row expiry while a draft retains focus.
- All 17 sequential synthetic browser suites passed, including 18 activity cases
  through ordinary `.cjs` discovery. The four browser-runner unit tests also passed.
  These checks do not contact a daemon or provider.
- The official pinned sing-box 1.12.25 synthetic loopback fixture verified real
  VLESS TCP/UDP payload byte directions, user isolation and per-user decreases
  after one flow closes. Only fixture TLS wrapping and dial targets are replaced;
  no installed network, configuration, logging or user credentials changed.
- These observations are not REALITY cumulative history, network wire overhead,
  end-to-end Android testing or general leakage/DPI guarantees.
