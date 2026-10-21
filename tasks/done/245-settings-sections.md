# Settings section persistence

Primary checkout: `/Users/romanzhigalkin/Web/razdacha`, `main`.
Freshly fetched remote: `origin` (`git@github.com:ArghTeam/razdacha.git`), `main`,
base `f2d7480173ba4b8e9c2545f4fdbc0d23fc63c836`.
Feature checkout: `.codex/worktrees/feat/245-settings-sections`.

## Intended behavior and acceptance

Keep Settings in one dialog with Network, Schedules, Telegram and Backup navigation.
Each section validates and saves only its fields through the existing API. Disjoint
PATCH keys preserve other sections and hidden settings. Network saves require Apply;
Schedules update automatically but still require Apply to update the active record.
Keep global pending-state tracking for settings writes.
Delivery settings take effect immediately, without routing Apply. Keep drafts across
navigation and failures; display durable local saved/unsaved/error status. Test and
Send use saved settings only and never issue PUT. Omit unchanged secrets, label every
control and show saved-secret presence separately. Clear drafts on modal cleanup.
Late responses cannot render into replacement dialogs or update another session.

Synthetic Chromium regression coverage must check isolated payloads, validation before
writes, failures/drafts, secret omission, delivery actions, lifetime and responsive
layouts at 1440/1024/375/320. No external service calls or staging mutations.


## Final implementation constraints

The shared settings endpoint performs full-record read/modify/write. Serialize its
requests across modal lifetimes, release the queue after either outcome and gate queued
intent by its original session before dispatch. Send only changed fields; a replacement
modal opened during a prior save must not revert untouched values from its older view.
Use confirmed values only for the response's own section. Backup sends refresh delivery
metadata after either outcome and never reload draft controls. Keyboard focus must be
geometrically clear of sticky header/footer. Validation messages are deterministic English.

## Verification

- `CGO_ENABLED=0 go test ./ui ./internal/api`: passed.
- JavaScript syntax checks and `git diff --check`: passed.
- `NODE_PATH=<installed Playwright modules> node ui/tests/settings-persistence.cjs`:
  passed with installed Chrome, synthetic local API and production UI modules.
- Browser regressions cover each payload/reopen boundary, validation-before-write,
  save/delivery failures, saved-secret omission and clearing, retries, custom intervals,
  stale GET/save callbacks, shared write queue across replacement, failed queue head,
  queued session cancellation and refreshed backup delivery history. The fixture reads
  the full settings snapshot before an optional hold, reproducing backend RMW behavior.
- Responsive checks at 1440/1024/375/320 include persistent labels, horizontal overflow,
  bottom actions and actual Tab-to-Save geometry above/below sticky chrome.
- Independent visual evaluation used fresh actual Chrome sessions and the available
  OpenAI evaluator; no different-provider review is claimed. Round1 requested focus
  clearance and deterministic English validation; both were fixed and rechecked.
- No staging mutation, deployment, real credentials or external delivery calls occurred.
  Screenshots and evaluator reports remain private temporary artifacts, outside Git.
- No Go source changed; local `golangci-lint` is unavailable. Daemon builds still need
  no Node or frontend build step. Runtime networking/cookie delivery is outside this
  synthetic frontend verification boundary.
