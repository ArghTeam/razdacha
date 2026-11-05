# Deliver pending REALITY profiles before Apply

Issue: #308. Primary checkout: `/Users/romanzhigalkin/Web/razdacha`.
Implementation checkout: `.codex/worktrees/fix-308-pending-reality-profiles`.
Remote/base: `origin/main`, freshly fetched at `2b1dc6205dd5ba10e33e2a5ec8ccc67ffcce880c`.

## Contract

Authenticated administrators can obtain actual REALITY URI, QR and complete sing-box
JSON for a saved, enabled client before explicit Apply. These are marked **Works after
Apply**. Delivery never admits credentials, publishes policy, creates keys or restarts
services. Runtime admission remains the committed/live intersection in ADR 0032.

`reality_profile_status` retains activation semantics. A separate
`reality_profile_downloadable` boolean reports delivery readiness. Successful secret
responses carry coherent `X-Razdacha-Reality-State` and
`X-Razdacha-Reality-Revision` headers, preserving importable raw bodies.
Active profiles use committed connection settings; pending profiles use validated
saved settings and live public server identity/current credential from one read-only
transaction under the publication gate. Global/client disable, deleted credentials,
guarded or uncertain publication and stale frontend revisions refuse delivery.
Missing global configuration gives actionable Network settings guidance.

The pending warning persists through QR, copy and both downloads. The modal retains
existing session/draft/mutation/revision cleanup protections. Connection checkboxes
are independent credentials sharing routing rules, with no automatic fallback.

## Acceptance and verification

- Pending URI and JSON contain the real current UUID and stay identical after Apply.
- Secret GETs leave committed authority, runtime users and private state unchanged.
- Active delivery retains committed endpoints despite saved draft changes.
- Disabled/global-off/guarded/deleted/revoked/uncertain and stale revision cases remain protected.
- Browser checks cover immediate creation, pending warning through copy/download,
  late metadata/revision responses and existing lifetime regressions.
- Run Go tests, Linux-target pinned lint, all browser suites and pinned profile check.
- Update ADR 0036, API documentation and maintained layer context in the same PR.

## Verification record

- `CGO_ENABLED=0 go test ./...`: passed across all packages.
- Pinned golangci-lint 2.12.2 with `GOOS=linux`: passed, zero issues.
- `npm test --prefix ui/tests`: runner checks and all 16 browser suites passed.
  After the final global-settings readiness invalidation fix, the affected REALITY
  suite passed all 44 cases and the Settings suite was rerun separately.
- `RAZDACHA_TEST_SINGBOX=/private/tmp/razdacha-297-client/sing-box CGO_ENABLED=0
  go test ./cmd/razdachad -run TestRealityPendingDeliveryDoesNotAdmitCredentials
  -count=1 -v`: passed. The exact generated pending JSON passes the pinned
  sing-box 1.12.25 checker; no runtime connection was started.
- JavaScript syntax checks and `git diff --check`: passed.

Only local synthetic fixtures were used. No server, sandbox, production tunnel,
credential, deployment or release was modified by this implementation task.

## Completed implementation

Implementation completed in commit `3640d58d82a515bb4f46e59c1dbeab891f9b0dcd`
and delivered in [PR #309](https://github.com/ArghTeam/razdacha/pull/309).
Independent review approved that implementation commit. The Go, Linux lint,
16-suite browser run, final affected browser reruns and pinned profile checker
results above establish the completed local verification boundary.

The implementation includes independent downloadability metadata, coherent private
export responses, persistent pending activation guidance and unchanged explicit
Apply/runtime admission. ADR 0036, API documentation and maintained layer context
are included in the same PR. Merge and any later deployment are separate primary
agent operations and are not claimed by this completed implementation record.
