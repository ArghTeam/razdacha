# Tunnel action lifetime

Issue: #256; epic: #242. Authorized repository: /Users/romanzhigalkin/Web/razdacha.
Primary checkout: /Users/romanzhigalkin/Web/razdacha, main; remote: origin; target: main.
Fresh base recorded by primary: b4ecfbcea24c5f2d1f6b6f8f950c17b7a0a14d12.
Feature checkout: /Users/romanzhigalkin/Web/razdacha/.codex/worktrees/fix/256-tunnel-action-lifetime.
Branch: fix/256-tunnel-action-lifetime.

## Intended behavior

Tunnel Save must not close or overwrite a replacement interaction. Capture session and modal/interaction lifetime for all tunnel actions and feedback. Preserve confirmed same-session durable effects after ordinary close/navigation, while previous-session callbacks cannot mutate inventory/pending state, notify a new session or dispatch follow-up requests. Single-flight actions prevent duplicate accepted submissions; current-session inventory loads are coalesced and ordered, with mutations invalidating older snapshots. Update current inventory identities rather than detached captured objects.

Separate confirmed Save/WARP creation/pool refresh success from a failed follow-up inventory/details read; failed refresh must not encourage repeating an already accepted mutation. Preserve raw/parse cleanup and ordering, WARP/reference rules, pool health and backend/network semantics. No layout or shared shell/auth/API changes.

## Acceptance

First reproduce the delayed PATCH replacement-close defect against the base in actual Chromium with intercepted synthetic exact-origin requests. Then cover held POST/PATCH success/failure, ordinary close/replacement/new session, single-flight, current inventory identity, stale inventory ordering/coalescing, post-mutation reload failures including WARP, row toggle/delete/refresh/check feedback and session guards, metadata ownership and raw/parse cleanup/order. Run relevant existing browser suites, focused Go UI and syntax/diff checks. Commit, push and open a PR; no merge, staging, real WARP/network requests, deployment or other repository changes.

## Baseline reproduction

Before production edits, the actual Chromium regression held `PATCH /api/tunnels/t1`, opened a replacement draft, then resolved the PATCH successfully. On base b4ecfbc it failed with `A delayed Save closed the replacement editor`. All requests were intercepted at the disposable exact-origin fixture; no daemon or external service was contacted.

## Narrow scope extension: cross-screen reader

The primary additionally authorized `ui/dist/js/screens/peers.js` **load() only**. A full-app Chromium reproduction held the Clients screen's `GET /api/tunnels`, navigated to Tunnels, accepted a toggle PATCH, then released the older Clients snapshot. Before the guard it failed with `Delayed Clients GET replaced accepted tunnel state` (enabled became true again). The reader must still belong to its initiating screen as well as its session/request revision before publishing either result; a valid current Clients load remains accepted. A reviewer additionally reproduced a startup batch race: tabs remain available while an unrelated bootstrap GET is pending, allowing an accepted tunnel mutation before the old batch publishes its tunnel snapshot. The bounded `tunnels.beginInventoryRead()` token is shared by tunnel and Clients readers, and the #248 owner will apply the same token to app.loadAll() before its batch requests. No app.js change is included in this task's ownership; the coordinator retains that integration requirement.


## Shared-reader integration boundary

The shared `beginInventoryRead()` token is implemented here and used by tunnel/Clients screen readers. It exposes `current()` and `accept(tunnels)`; a newer read or accepted mutation invalidates an older token, including already-downloaded snapshots waiting on another batch member. The dependent #248 owns startup app.loadAll() adoption: capture the token before dispatching its batch and publish via accept rather than assigning state.tunnels. The demonstrated boot-batch race is therefore **not closed by #256 alone**; the coordinator retains #248 integration and final #249 whole-app verification. No app.js edit is included here.

## Final implementation and verification

- Tunnel Save, WARP, toggle/delete, catalog refresh, checks and metadata reads now capture session and appropriate modal/row interaction ownership. Same-session accepted effects update current inventory identities and pending state after close/navigation; prior-session callbacks cannot mutate or start follow-up reads.
- Single-flight gates survive rerender/replacement and release by operation ownership. Connected initiating buttons are restored independently of obsolete modal feedback. Modal Save ownership follows the original modal even when browser/hash navigation changes its background screen, preventing an accepted create from remaining resubmittable.
- Shared read tokens and mutation invalidation protect accepted state; tunnel reads coalesce. Clients load uses the token and screen/session/request guards, including error paths. Confirmed Save/WARP/catalog success remains distinct from inventory/details failure. Raw configuration stays out of accepted inventory copies; raw/parse lifetime, newest-input ordering and debounce cleanup remain intact.
- `node ui/tests/tunnel-actions.cjs`: **47/47** synthetic Chromium cases passed, including two full-app/router regressions and the shared startup-token contract. `node ui/tests/client-actions.cjs`: **29/29** passed. `node ui/tests/client-details.cjs`: **25/25** passed. Earlier related `rule-integrity.cjs` also passed all 14 cases.
- `CGO_ENABLED=0 go test ./ui`, ES-module syntax for both owned modules and `git diff --check` passed. Backend code and protocol contracts are unchanged, so no additional networking/API integration claim is made.
- Frozen production SHA-256: tunnels.js `c5ad1f022ae16d0e1ad7c62da2461bd921d743b64f408936222205e4e22b133c`; peers.js `884e6540d37a5bff6c83145eed0dba133603999427a78eb101f9123da380aa52`.

Independent source review prompted the connected-button and original-modal navigation regressions; both are fixed and covered. Exact committed-head review and hosted CI remain coordinator gates. The maintained API invariant records the implemented lifetime/token rules and the explicit #248 boot-reader dependency. No composition/CSS, shared shell/auth, backend, staging, installed networking, deployment or merge changes were made. All test requests are intercepted at one synthetic origin; no external WARP registration occurs.
