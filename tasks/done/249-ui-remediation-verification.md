# UI remediation verification

## Current final verification checkout

- Primary: `/Users/romanzhigalkin/Web/razdacha`, `main`.
- Remote/target: `origin` (`git@github.com:ArghTeam/razdacha.git`), `main`.
- Fresh initial final-phase base: `b463ae31e86169971ba0e90f6bd73f7436d6e9a1`.
- Dependent #259 merged base: `54817315fa2ac60af405cfcb6cc4c41281d66865`.
- Current feature: `.codex/worktrees/test/249-ui-remediation-verification`, branch
  `test/249-ui-remediation-verification`.
- Final candidate readiness is separate from merge/primary synchronization. The
  original tooling phase below is retained as its historical implementation record.

## Historical tooling checkout and scope (PR253)

- Primary: `/Users/romanzhigalkin/Web/razdacha`, `main`.
- Remote/base: `origin` (`git@github.com:ArghTeam/razdacha.git`), `main`.
- Fresh base recorded by primary preflight: `3a7aaf976ff7a32544a6d5a53a2d4fad8b65f6ff`.
- Feature: `.codex/worktrees/test/249-ui-browser-regressions`, branch
  `test/249-ui-browser-regressions`.

## Intended tooling behavior

Add an independent browser-regression CI job. Pin the already locally exercised
Playwright version and its transitive dependency lock under `ui/tests`. Install
Chromium in this job and run every maintained browser suite sequentially with a
bounded timeout and a failing exit status for any failed suite. Use ordinary module
resolution, without personal dependency paths. Keep Node, browser downloads and npm
outside daemon builds and release jobs. Fixtures must use synthetic local/intercepted
API data and block runtime external requests; no real host, credentials or deployment.

Document clean install, browser setup, runner discovery/helper naming and verification
limits. This tooling PR references #249; final integrated QA remains active and waits
for the remaining remediation work (#246–#248). It does not close #249 or claim the
full audit has been completed.

## Acceptance and verification

- `npm ci` uses the committed exact Playwright pin and lockfile.
- `npm test` runs all committed suites in stable sequential order against Chromium.
- Runner failures/timeouts return nonzero and stop subsequent suites.
- Settings fixture defaults to bundled Chromium; optional local Chrome selection is
  explicit. Its request guard fails closed outside the exact disposable fixture origin.
- Existing browser assertions remain intact; Go UI embed checks continue to pass.
- Independent review, commit, push and an open PR; no merge authority.

## Tooling phase verification

- Pinned Playwright 1.59.1 was read from the previously exercised local installation;
  the committed lock resolves the same package and matching playwright-core version.
- `npm ci`, `npx playwright install chromium` and `npm test` passed with normal local
  module resolution, without NODE_PATH or an application server prerequisite.
- All three current browser suites passed: 30 client-action cases, 14 rule-integrity
  cases and Settings persistence/delivery/lifetime/responsive coverage.
- Four Node runner regressions passed: stable sequential discovery/helper exclusion,
  propagated child failure, real child timeout and refusal of an empty suite directory.
- Settings exact-origin refusal uses a reachable alternate loopback hostname with
  no-CORS fetch. A separate control page proves the same target is browser-reachable
  without interception before the guarded page must reject it, avoiding a false pass
  from hostname/binding/browser-policy failure or CORS denial.
- `CGO_ENABLED=0 go test ./ui`, JavaScript syntax checks and diff whitespace passed.
- Final integrated verification after #246–#248 is still pending. This specification
  remains active; this phase establishes repeatable CI tooling only.


The initial hosted browser job failed during browser installation because
`ubuntu-latest` resolved to Ubuntu 26.04, unsupported by Playwright 1.59.1. The browser
job now pins Ubuntu 24.04, verified in the pinned package's browser/dependency mappings.
Other CI jobs retain their existing runner selection. Hosted rerun remains a separate
gate from successful local tooling/suite checks.

## Final integrated verification phase

- Designated primary remains `/Users/romanzhigalkin/Web/razdacha`, `main`.
- Fresh `origin/main` fetched before this phase: `b463ae31e86169971ba0e90f6bd73f7436d6e9a1`.
- Feature checkout: `.codex/worktrees/test/249-ui-remediation-verification`, branch
  `test/249-ui-remediation-verification`.
- Scope: preserve the nine maintained suites and add only rule inventory/probe,
  broad tunnel-control and whole-application integration gaps using `app.fixture.cjs`.
  Keep runtime requests exact-origin and journal unexpected API/assets. Exercise UI
  operations using synthetic state, never installed/runtime services.
- Capture readable ordinary desktop/mobile scenes separately from deliberate stress,
  loading and failure captures. Record source hashes and actual case coverage. Existing
  suite coverage and source-only conclusions must remain distinguishable from new tests.
- Close historical specification locations for merged 243/246/247/256/259. Final
  verification is recorded in `tasks/done` for the final PR; issue/epic closure remains
  conditional on confirmed merge and primary synchronization.
- The final #248 design attempt ended MAJOR REVISION for textarea focus occlusion.
  An ordinary subsequent scoped fix and independent geometry checks verified the
  correction. This phase is integrated correctness QA, not a fourth design evaluation
  or a claim that the last design evaluation passed.

## Integrated coverage map

The final runner result is recorded below after the dependent #259 merge and boot
reader adoption. A suite's synthetic response or model mutation alone is not runtime
proof; assertions check the rendered/current frontend state as well as request payloads.

| Surface | Maintained evidence and added gaps | Verification boundary |
|---|---|---|
| Authentication and shell | Existing shell functional/layout suites; real-index login validation, wrong-password/Retry-After, logout, expiry, navigation, offline/recovery, version, menu keys and global Apply checks | Synthetic sessions and local response ordering; no backend cookie-delivery claim |
| Clients and Details | Existing action, Details and layout suites retain creation single flight, identity/enabled/route payloads, blocked targets, source refresh, config/retry, Copy/Download, QR/application cleanup, delete and delayed-session/replacement coverage | Disposable configuration only; clipboard content is intentionally external, no QR camera scan or real client import |
| Rule editor | Existing integrity/layout suites plus combined domain/CIDR/community/API-managed resources, selected enabled/disabled clients, tunnel/direct/block and valid WARP-chain payloads; invalid CIDR before write | UI validation/payload preservation, not runtime matching/publication |
| Rule inventory and probes | Added actual-router menus, toggle, duplicate/delete success/failure/cancel, complete order payload/rollback, preset draft-only opening, URL-only duplicate/real-IP preservation, both probe busy/results/refusal/error states; #259 owns action/probe race regressions | No real domain probe or external list request |
| Tunnels | Existing 47-case lifetime suite plus actual DOM controls, Add validation, mocked input-shape preview responses/warnings/errors, raw missing/error replacement guidance, WARP name-only editing and menu restrictions | Preview responses are synthetic; no parser/protocol interoperability or WARP registration proof |
| Pool details | Empty, excluded-only, unknown health, disabled, failed/missing details and refresh retry; all four populated groups overflow and reach visible ends at 1440/1024/375/320 in both themes | Candidate health/filter records are fixture data, not observed network measurements |
| Settings | Existing persistence suite retains all four boundaries, drafts/reopening, shared save queue, RMW secret rotation, old-session intent cancellation, validation, secret omission and saved-only actions; actual app adds Save-to-Apply and direct backup download | Download is a deliberately non-restorable synthetic file; no real backup delivery/decryption |
| Diagnostics | Existing supported checks/rerun/report-download and allowlist privacy regressions; unavailable log/config controls remain undispatched in real-index integration | No raw viewers added; report export is tested as an allowlist, not universal text redaction |
| Geometry and presentation | Existing client/rule/shell matrices cover light/dark, narrow widths and doubled text; new pool scroll assertions reject empty/hidden containers and require overflowing stress data; normal captures assert the actual heading matches the selected screen module | Actual Chromium geometry, not full screen-reader, hardware-keyboard or cross-engine certification |

The final ordinary gallery uses Example laptop, Office tablet, Demo exit and documented
synthetic addresses. It is separate from deliberately long/error/stress captures. Each
capture records viewport, selected screen, matching heading hash and hashes of all
production UI assets involved. No deployed-version or installed-network claim follows
from these images. External release destinations are inspected through local version
metadata; no outward navigation is needed or asserted as verified.

## Dependent final base and demonstrated startup correction

Merged fresh `origin/main` at `54817315fa2ac60af405cfcb6cc4c41281d66865` after #259
(PR260), preserving all final QA work. The bounded additional production scope is
`app.loadAll()` only: capture the exported rules reader before the batch, gate the
rules pull's metadata/error feedback and publish through `accept`.

Before that correction, the actual-index test held startup Settings, accepted a rule
toggle, released Settings and paused the last startup diagnostic read. It failed with
`Bootstrap rule snapshot overwrote the accepted toggle` (true instead of false). Holding
the last read prevents a subsequent navigation reload from concealing the bad snapshot.
Obsolete 404/503/network rules responses use the same observation boundary.


## Completed final verification

The saved final run completed with `All 13 browser regression suites passed.` No
blind rerun was needed after the interrupted conversation: the complete saved runner
log and unchanged production source hashes were recovered and checked.

| Suite | Recorded passing result |
|---|---|
| Client actions | 29 lifetime cases |
| Client Details | 25 cases |
| Client layout | 8 theme/viewport matrices plus creation/configuration checks |
| Actual app integration | 17 cases, including all four bootstrap authority outcomes |
| Rule actions | 38 inventory/probe lifetime cases |
| Rule integrity | 14 cases |
| Rule layout | 15 layout/state matrices |
| Rule inventory/probes | 16 cases |
| Settings persistence | Payload, delivery, lifetime and responsive checks passed |
| Shell/Diagnostics | 28 functional/session cases |
| Shell layout | 16 matrices |
| Tunnel actions | 47 lifetime cases |
| Tunnel UI | 27 form/control/pool cases |

- Runner unit regressions: 4/4 passed.
- `CGO_ENABLED=0 go test ./ui ./internal/api`: passed on the final frozen assets.
- JavaScript syntax checks and diff whitespace: passed.
- Private final evidence: 114 ordinary screenshots and 11 separate stress captures,
  with verified actual screen-heading match, viewport and all production asset hashes.
  All recorded hashes match the frozen source used by the passing run.
- Frozen app.js SHA-256: `56acc20058e10e6d091e64451154d5155e7c4387063007da3ab448ceefec8676`.
- Unchanged app.css SHA-256: `244990d3cdee2e6b12abe2c05abc3ae9de22d89cfe5090c755f7ae6e55a098ad`.
- Data and displayed version (`0.10.0-synthetic`) are explicitly synthetic. No staging,
  old-host, deployment or real network operation was used. Runtime backend persistence,
  cookie delivery, VPN/DO connectivity, real Telegram/backup delivery, QR camera scan,
  parser/protocol compatibility and cross-engine accessibility remain unverified.
- Independent final committed-source review, bounded whole-app correctness QA and
  hosted CI remain separate PR gates. This is not another design-evaluation attempt.
  The primary alone handles any authorized merge, synchronization and epic closure.

## Planned local candidate build boundary

After confirmed merge and safe primary synchronization, the primary will create a
local annotated `v0.11.0-rc.3` tag and build Linux amd64/arm64 daemon and CLI archives
with that linker version, final commit provenance and checksums. This is a planned
local build, not a completed installation or network verification. No tag push,
upload, release publication or deployment is authorized. The daemon build remains
Node-free; the panel obtains its version from the backend rather than a hardcoded UI
version. The stable release statement in project instructions remains unchanged.
