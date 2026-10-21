# Embedded panel browser regressions

These development-only tests exercise the committed `ui/dist` modules in real
Chromium with disposable synthetic API responses. They do not start a daemon, use
installed VPN state, send Telegram messages or contact staging. Runtime requests
are restricted to synthetic loopback fixtures. Client and Settings suites enforce
exact origins; the rule suite blocks non-loopback hosts. Client actions use a fully intercepted
loopback origin; rule and Settings fixtures bind temporary loopback ports.

## Run

Use Node.js 22 or newer. CI uses Node.js 24.

```sh
cd ui/tests
npm ci
npx playwright install chromium
npm test
```

On Linux, install browser system dependencies as well:

```sh
npx playwright install --with-deps chromium
```

Playwright is pinned to `1.59.1`, the version already exercised by these suites before
adding CI; `package-lock.json` pins dependency artifacts. Update the version and lock
together, install its matching browser revision and rerun all suites when upgrading.
CI pins Ubuntu 24.04, which this Playwright version supports, and installs the browser
for that exact package rather than using an arbitrary Chrome. Do not switch the job to
a newer runner image without checking its support in the pinned Playwright package.
The Settings suite alone also accepts `PLAYWRIGHT_CHANNEL=chrome` for an explicit local
installed-Chrome check; leave it unset for the full pinned Chromium run.

`npm test` first verifies the runner's failure/timeout behavior, then discovers all
top-level `*.cjs` suites in sorted order and starts them one at a time. The first
failed suite stops the run and returns a nonzero status. Each suite has a three-minute
timeout. Name reusable modules `*.helper.cjs` or `*.fixture.cjs` to exclude them from
automatic execution; keep maintained test suites as ordinary top-level `*.cjs` files.
No `NODE_PATH`, personal dependency path or separate application server is required.

## Scope and limits

The rule layout suite adds both themes at 1440/1024/375/320 pixels, doubled text,
keyboard focus/clipping geometry, scroll ends, long identities and large selections,
as well as empty, unavailable-source, URL-only, orphan, chain and save-error states.
`RULE_SCREENSHOT_DIR=<temporary directory> node rule-layout.cjs` optionally captures
screenshots; `rule.fixture.cjs` exports a loopback-only synthetic preview server.

Current suites cover rule editor integrity, client action lifetime, unified Client
Details drafts/delivery, responsive client layout/focus, Settings persistence/lifetime,
and shared shell/Diagnostics functionality, report privacy and layout.
The dedicated `ui-browser` CI job runs separately from Go tests,
static builds and releases: Node, npm and browser downloads are never daemon build
requirements. Installed packages are ignored by Git and are outside embedded assets.

These checks establish frontend rendering, request sequencing, drafts and callback
lifetime using synthetic data. They do not verify backend cookie delivery timing,
real tunnel connectivity, live Telegram delivery or installed deployment behavior.
Tooling integration references #249; the final combined remediation audit remains
active until the remaining UI changes and their independent verification are complete.

The shared app fixture (`app.fixture.cjs`) supports explicit state overrides, exact-origin
request journals/unexpected-request failures, per-method/path hold/fail/release and
login/empty readiness. `shell-preview.fixture.cjs` reuses its synthetic model for a
loopback-only preview; unmodeled operations fail explicitly. `SHELL_SCREENSHOT_DIR`
optionally captures the shared layout suite. These fixtures never contact a daemon.
