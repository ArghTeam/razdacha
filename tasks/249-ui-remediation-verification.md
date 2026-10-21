# UI remediation verification

## Checkout and scope

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
  no-CORS fetch, avoiding a false pass from unreachable external DNS or CORS denial.
- `CGO_ENABLED=0 go test ./ui`, JavaScript syntax checks and diff whitespace passed.
- Final integrated verification after #246–#248 is still pending. This specification
  remains active; this phase establishes repeatable CI tooling only.
