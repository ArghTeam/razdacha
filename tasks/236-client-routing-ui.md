# 236 — Visible client routing controls

## Goal
Expose each client's configured route and a direct Routing action on the client inventory.
Preserve existing API and modal/session safety. Saving edits configuration; only Apply
activates it. Global dirty state cannot identify an individual client's active policy.

## Acceptance
- Structured inventory with identity/activity, labeled configured route and visible
  Routing and Configuration controls; administrative actions remain in the overflow menu.
- Named route editor with Shared rules and Whole Internet radio choices, conditional
  exit selector, unavailable warnings and honest empty/loading/error states.
- Explain IPv4 TCP/UDP scope, blocked IPv6/ping/private forwarding, gateway access and
  no direct fallback. Enabled state and health probes do not prove connectivity.
- Preserve QR/download cleanup, modal focus and late-response session protection.
- Existing system fonts, tokens and static modules; no remote assets or build step.

## Provenance
Primary `/Users/romanzhigalkin/Web/razdacha`; remote `origin`, target `main`.
Fresh base `182974b0b23360ae2da854693cb43a9a4af5b037`.
Worktree `.codex/worktrees/feat-236-client-routing-ui`, branch `feat/236-client-routing-ui`.
Ownership: `ui/dist`, `context/ui`, this specification.

## Verification
- `CGO_ENABLED=0 go test ./ui ./internal/api`: passed.
- Module syntax and `git diff --check`: passed.
- Actual static assets with loopback synthetic API: 13/13 browser cases passed,
  covering direct controls, named radio modes, failed Save retention, Save versus
  Apply, dirty reload, disabled/missing/empty targets, retry, keyboard containment,
  modal replacement and previous-session responses, long names at 320/375/768/1440,
  dark theme and empty-client creation action. Nonlocal browser requests blocked.
- Screenshots and results are local private review evidence in
  `/private/tmp/razdacha-client-ui-U2RbplvM`; provenance records asset hashes.
- The synthetic Apply fixture checks frontend sequencing and presentation, not real
  runtime publication, encrypted WireGuard, external connectivity or installed deployment.
  No real hosts or client profiles were used. Existing secret-download implementation
  remains unchanged; this task does not claim new heap-erasure or cookie-timing coverage.


## Independent evaluation refinement
The first visual evaluation passed composition but required two presentation corrections:
shared rules may tunnel, go direct or block; source-specific forwarding restrictions apply
only to Whole Internet mode. Both descriptions are now mode-accurate. Repeated connectivity
caveats were removed while configured labels, target warnings and Save → Apply remain.
Focused browser verification checks mode-specific help, neutral shared copy, unavailable
warnings and final desktop/mobile layouts; it does not rerun unrelated lifetime cases.


Refinement verification: focused browser checks passed 4/4, embedded UI Go tests,
module syntax and diff checks passed. Evidence is under the same private directory's
`revision2/` subdirectory, including desktop and 320/375 screenshots. This supplements
rather than replaces the previous 13-case behavior results.
