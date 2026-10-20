---
type: spec
tracker: "#216"
---
# Clear configuration secrets from the panel

## Goal and problem
The peer modal retains private configuration in persistent dataset attributes after
its HTML is cleared. Closing, replacing or logging out must clear configuration data
and filenames from persistent DOM and UI caches while preserving download when open.

## Domains touched
- [UI](../../context/ui/index.md): peer modal, shared shell, tunnel editor cache and logout cleanup.
- [API](../../context/api/index.md): opt-in synthetic browser fixture and authenticated session-check semantics.

## Decisions
Implement after [#215](215-safe-peer-deletion.md). Keep secrets only for the active
configuration interaction. Invalidate or ignore in-flight configuration responses when
the modal/session lifecycle changes, so stale asynchronous work cannot restore secrets.
Use one modal revision and cleanup boundary for text, filename, canvas pixels and
modal-owned download URLs. Capture it before asynchronous peer creation or config
fetches; only the current interaction may render a response. Logout enters the gate
synchronously, before its request completes. Serialize a new login behind the actual
logout response to preserve cookie ordering; old-session frontend callbacks cannot dismiss
a newer login. Tunnel editor raw/parse caches share the same modal lifetime.
This localized cleanup needs no ADR unless it introduces a
broader secret-storage decision. Update maintained UI context with the final lifetime.

## Acceptance criteria
- [x] Close button, backdrop and Escape remove configuration/filename attributes and caches.
- [x] Replacing the configuration modal clears its previous data.
- [x] Logout clears an open modal even when the request fails due to an expired session.
- [x] Delayed configuration responses cannot reopen or repopulate secret data after
      close, modal replacement, logout or session expiry.
- [x] A synthetic-key browser regression verifies successful download while open.
- [x] Without reloading, the same test verifies that configuration/key data is absent
      from persistent DOM/UI state after close, replacement and logout; inspection
      requires no authenticated API request.

## Validation
Tested executable source: `1f2d1dcc3770fa381a7447f0f4ea663a67b3b6ba`;
prerequisite baseline: `0da7e20744dc042c13ebc992137f6524b3e66c26`.
The final documentation commit does not alter Go, embedded UI or the three fixture
adapter files. Baseline was a pristine 438-file archive plus only those same adapters.

- Relevant API/UI tests, JavaScript syntax checks, Darwin/Linux lint and static Linux
  fixture builds passed. Independent local browser verification passed all 31 fixed
  cases and positively reproduced six baseline defects.
- Controlled staging B3 used the actual embedded UI and API with disposable synthetic
  state in fresh network/mount namespaces. All 31 fixed cases passed. Five baseline
  defects had positive retained-secret/stale-render observations. The baseline
  peer-creation case is **inconclusive**: consuming the POST body did not await its
  nested configuration GET, and a retained B dataset can survive while loading.
- The controller correctly stopped with exit 2 on that missing predicate. Local wrapper
  SSH exit 255 is distinct from remote supervisor exit 0 and infrastructure PASS;
  independently recovered final remote audit/logs confirm both exact child/parent
  Go PASS envelopes and both actual HTTP 204 stop receipts. This is bounded per-case
  acceptance, not a fully green paired controller run or six staging reproductions.
- Strict before/after comparisons checked every typed database cell, schema, primary
  key and row count. Only monotonic `tunnel_checks.checked_at` and `ok_at` differed
  (seven and three cells); full semantic equality is therefore false. All policy,
  pool and authentication data remained checked and unchanged. Fourteen inventoried
  installed files, services, host structure and policy identity stayed stable.
- All owned local/remote process groups were absent; four pinned uploaded payloads
  were removed. READY tokens and raw host evidence remain private. Superseded
  prelaunch/B2 attempts remain uncertified, with their evidence preserved privately.

Independent security review and primary-owner evidence review accepted this bounded
result. Final documentation review and CI on the final PR head remain merge gates.
No installed application deployment or production/source-host access was performed.
See the [redacted runbook evidence](../../docs/11-staging-validation.md#216-completed-observations)
and [issue #216](https://github.com/ArghTeam/razdacha/issues/216).

## Residual limitation
The stale-401 browser case injects no `Set-Cookie` and checks frontend notification
and rendering lifetime only. The existing backend expires cookies on invalid-session
401 responses; delayed cookie delivery can require another login. Backend cookie
behavior is unchanged. This is a P2 availability limitation, not a confidentiality
failure or authentication bypass. Cleanup removes application references, not immutable
JavaScript heap strings or downloaded files. A B dataset alone does not establish that
B is visibly rendered while another configuration request is loading. The dedicated
peer-create × logout/session-change browser case was not run; source review covers
the session guard, and no executable coverage claim is made for that combination.

## Security review gates
The implementation author cannot approve their own change. Before an isolated staging
canary, an independent reviewer must assess the local regression, threat/failure model
and proposed scope/rollback. Require a separate reviewer and primary owner review of the
final diff, tests, documentation and staging evidence.
Final acceptance requires baseline-fails/fix-passes evidence, adversarial negative
cases and controlled staging observations under the [runbook](../../docs/11-staging-validation.md).
No unresolved P0/P1 findings may be marked ready to merge. Unknown results stay explicit.
Review approval does not authorize merge or release. Source, local browser, runner
and staging evidence reviews completed with the qualifications above; final documentation review and current CI remain merge gates.
