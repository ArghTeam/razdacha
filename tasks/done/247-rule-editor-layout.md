# Rule editor hierarchy

Issue: #247. Authorized repository: /Users/romanzhigalkin/Web/razdacha.
Primary checkout: /Users/romanzhigalkin/Web/razdacha; remote: origin; target: main.
Fresh base verified by primary: c650a59f6cb6d64a772a3e04f6b092cae2a21faf.
Feature checkout: /Users/romanzhigalkin/Web/razdacha/.codex/worktrees/feat/247-rule-editor-layout.
Branch: feat/247-rule-editor-layout.

## Intended behavior

The rule dialog follows Name, Clients, Resources, Action. A compact position line and optional ordering help replace permanent priority prose. Client selection has search, a count and wrapping identities with separate disabled/whole-Internet status. Community selections use compact review/catalog disclosures instead of an always-expanded chip wall. Missing selections remain explicitly identified and require deliberate removal. Existing API-managed URLs remain escaped read-only text outside the catalog. Manual resources keep bounded fields and live line validation. Action controls have an attached tunnel label and an optional Advanced WARP chain disclosure, opened for existing chains.

Keep the integrity contract from #243: no implicit tunnel replacement; unavailable references and API-managed sources survive unrelated edits; scope-aware overlap remains advisory; disabled selected empty orphans stay disabled; validation/API failures retain drafts and durable errors; modal/session/request ownership prevents stale responses from replacing newer drafts while legitimate same-session success marks pending changes. No routing, chain, DNS, API or daemon build semantics change.

## Acceptance and boundaries

Run the actual embedded modules in Chromium with loopback-only synthetic API state at 1440, 1024, 375 and 320 pixels, both themes, 200% text and keyboard focus geometry/scroll ends. Cover empty/error catalogs, URL-only sources, empty/disabled/missing targets, selected orphan, chains, long names, 36 selected sources, 39 manual lines, over 20 clients, validation/server failure and delayed Save/session behavior. Preserve the existing 14 integrity cases and add layout coverage. Run focused Go UI, syntax and diff checks. Supply stable local synthetic preview/screenshots for independent visual evaluation (up to three rounds), then exact-head source/context review, commit, push and PR. No staging, installed networking, deployment or other repository changes.

## Implemented result

- One section sequence and one primary body scroller replace the split Name/Action layout. Save/Cancel and the Save-versus-Apply reminder remain available.
- Client search/counts preserve selection while names wrap fully and disabled/whole-Internet status stays separate. Community review and catalog browsing start compact; retained unavailable-source counts and warnings stay visible outside closed disclosures. Existing URL sources remain escaped inert text outside the catalog.
- Manual fields retain live line errors and bounded scrolling. A separate attached Tunnel label and optional Advanced WARP disclosure preserve existing targets/chains; existing chains open automatically. Keyboard focus scrolls fully inside clipping ancestors and explicit chip removal restores a visible summary focus target.
- Maintained Rule documentation and UI invariants were updated in the same change; existing Client Details invariants were preserved. API payload, orphan, DNS, chain and asynchronous ownership boundaries from #243 remain unchanged.

## Verification

- `node ui/tests/rule-layout.cjs`: 15 Chromium layout/state matrices passed, including eight light/dark × 1440/1024/375/320 matrices, each with 200% text, keyboard focus geometry, hit areas and scroll ends. Large fixtures include 24 clients, 36 selected sources and 39 manual lines.
- `node ui/tests/rule-integrity.cjs`: all 14 existing policy/lifetime cases passed.
- `CGO_ENABLED=0 go test ./ui`, ES-module syntax and `git diff --check` passed. Playwright uses the pinned developer-only package and bundled Chromium; no daemon build dependency was added.
- Independent visual evaluation attempt 1: PASS, no material visual/UX findings. Fresh evaluator captures covered 1440/1024/768/375/320 in both themes, doubled text, empty/unavailable/URL-only/orphan/chain states, validation/save errors and 275 actual Tab transitions checked against clipping ancestors. Source hashes remained unchanged through evaluation: rules.js SHA-256 `ac84fb4056187b8a632654c07a9e85bdc46b917fcd87f959999805a1cce41996`; app.css SHA-256 `37e53750edfb76af54edff23642ec7aa03888498cb5d9de4f366917960308a4e`.
- Independent WIP code/context review reported no material findings and reran both browser suites successfully. Final committed-head confirmation is requested separately before merge.

The initial synthetic preview stopped during evaluation; the evaluator continued with the same worktree's exported loopback fixture server and actual modules, with unchanged product hashes. Browser evidence verifies frontend presentation and request/callback behavior with disposable synthetic state. It does not establish backend cookie timing, deployed routing, tunnel connectivity or installed policy. No host/staging/old-host requests, deployment or merge were performed.

## Integration closure

This implementation was merged and the designated primary checkout synchronized before
final #249 integrated verification. This record was moved to `tasks/done` during that
verification; original implementation evidence and its limitations above are retained.
