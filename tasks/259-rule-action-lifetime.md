# Rule inventory and probe lifetime

Issue: #259; epic: #242. Authorized repository: /Users/romanzhigalkin/Web/razdacha.
Designated primary: /Users/romanzhigalkin/Web/razdacha, main; remote: origin; target: main.
Freshly fetched base: b463ae31e86169971ba0e90f6bd73f7436d6e9a1.
Feature checkout: /Users/romanzhigalkin/Web/razdacha/.codex/worktrees/fix/259-rule-action-lifetime.
Branch: fix/259-rule-action-lifetime.

## Intended behavior

Rule inventory toggle, duplicate, delete and full-order writes capture their initiating session and row interaction. Accepted same-session mutations retain durable inventory/pending effects after ordinary navigation or modal replacement, while previous-session completions cannot update state, feedback or dispatch follow-up reads. Same-target actions and full-order writes are single-flight. Reordering publishes its accepted ID order against current identities, without rolling back captured inventory snapshots over concurrent accepted actions. Confirmed duplication remains successful when its follow-up read fails, preserving URL sources and resolve_real_ip in its POST payload.

Shared rule read tokens arbitrate screen/editor/duplicate/deletion reads against newer readers and accepted mutations. The primary authorized the narrow Clients deletion-follow-up reader integration. Startup app.loadAll() adoption is owned by dependent #249; this task alone does not close the separately reproduced bootstrap batch overwrite until that integration lands.

Both domain probes remain explicitly shared-rule scoped. Their input/result/error/busy state belongs to the initiating session and request/interaction; late old-session responses cannot render or clear a new request's busy state. Probe errors identify which operation failed. Editor layout, source preservation, orphan/chain/DNS policy, validation and presets remain unchanged.

## Acceptance

Reproduce overlapping reorder rollback and obsolete-session mutation/probe effects in actual Chromium before production edits. Cover concurrent reorder/delete/toggle/duplicate, session invalidation, navigation/replacement, single-flight, shared read ordering/errors, duplication read failure and URL payloads, both probes' result/error/busy ownership and retry. Use strict synthetic exact-origin fixtures with unknown/external request journals. Run affected integrity/layout suites, Go UI and JS syntax/diff checks; commit, push, open PR and independent exact-head review/CI. No shared CSS/app/fixtures/docs/UI-context edits, daemon/network/backend changes, real probes, staging, old host, deployment or merge.

## Baseline evidence

Before production edits, actual Chromium captured overlapping full-order writes: second success followed by first failure left UI IDs `[r1,r2,r3]`, undoing accepted `[r2,r3,r1]`. A held old-session duplicate also replaced new-session inventory with `[r1,r2,r3,copy-1]` and marked dirty. The independent reviewer separately reproduced boot-batch and Clients-deletion rule-reader overwrites through the actual app. The primary approved the shared token and narrow Clients deletion-follow-up integration; startup adoption remains #249-owned.

## Final implementation and checks

- Full-order PUT is single-flight and publishes only after acceptance, applying accepted IDs to current objects and retaining newly created objects. Failure does not restore any captured snapshot, so concurrent accepted deletion/toggle/duplication remains intact.
- Inventory actions capture their session and row/modal/navigation feedback scope. Confirmed current-session mutations remain durable after ordinary navigation/replacement; old sessions cannot repopulate inventory, mark pending, notify or start duplicate follow-up reads. Duplicate POST preserves remote_lists and resolve_real_ip; confirmed creation is distinguished from its failed inventory refresh.
- Shared rule tokens cover screen/editor/duplicate and the narrowly authorized Clients deletion-follow-up reader, including stale error paths. The #249 startup adoption is an explicit remaining integration requirement, not claimed fixed here.
- Both diagnostic variants share an action/Enter single-flight gate and session/request/navigation ownership. New-session rendering resets completed input/results/errors/busy state. Older completions cannot clear newer busy state; pending input drafts survive response rendering, and error labels identify route versus direct reachability while preserving shared-rule scope.
- `node ui/tests/rule-actions-lifetime.cjs`: **38/38** Chromium cases passed. Includes actual app keyboard and Clients-deletion navigation, concurrent reorder/action cases, all mutation session outcomes, shared readers and both probe variants. Unknown/external request journals remained empty; the unrelated release lookup was satisfied from synthetic browser cache, with all outside-origin requests still aborted.
- Existing `rule-integrity.cjs` **14/14**, `rule-layout.cjs` **15/15** matrices, `client-details.cjs` **25/25**, `CGO_ENABLED=0 go test ./ui`, both JS syntax checks and `git diff --check` passed.
- Frozen production SHA-256: rules.js `164ae562dda04a17c139b370a926b869bf501aeb962d6a5fd9d131f3c4e9e3c9`; peers.js `076b84ead7abeb04d9c4be82768cbf805db673e1fe7647d9b52bd4380faea3a6`.

The API lifetime section was updated in the same change. Editor layout, source/policy payloads, orphan/chain/DNS semantics, presets and backend contracts remain unchanged. Tests exercise intercepted synthetic responses and do not cancel accepted server requests or establish backend cookie timing, real routing/probe behavior or installed deployment. No staging, external probes, old host, CSS/shared-shell/app edits, deployment or merge were performed. Exact committed-head review and hosted CI remain coordinator gates.
