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
- [UI](../context/ui/index.md): peer modal, shared shell, tunnel editor cache and logout cleanup.
- [API](../context/api/index.md): opt-in synthetic browser fixture and authenticated session-check semantics.

## Decisions
Implement after [#215](done/215-safe-peer-deletion.md). Keep secrets only for the active
configuration interaction. Invalidate or ignore in-flight configuration responses when
the modal/session lifecycle changes, so stale asynchronous work cannot restore secrets.
Use one modal revision and cleanup boundary for text, filename, canvas pixels and
modal-owned download URLs. Capture it before asynchronous peer creation or config
fetches; only the current interaction may render a response. Logout enters the gate
synchronously, before its request completes. Serialize a new login behind the actual
logout response to preserve cookie ordering; old-session responses cannot invalidate
a newer login. Tunnel editor raw/parse caches share the same modal lifetime.
This localized cleanup needs no ADR unless it introduces a
broader secret-storage decision. Update maintained UI context with the final lifetime.

## Acceptance criteria
- [ ] Close button, backdrop and Escape remove configuration/filename attributes and caches.
- [ ] Replacing the configuration modal clears its previous data.
- [ ] Logout clears an open modal even when the request fails due to an expired session.
- [ ] Delayed configuration responses cannot reopen or repopulate secret data after
      close, modal replacement, logout or session expiry.
- [ ] A synthetic-key browser regression verifies successful download while open.
- [ ] Without reloading, the same test verifies that configuration/key data is absent
      from persistent DOM/UI state after close, replacement and logout; inspection
      requires no authenticated API request.

## Validation
Implementation verification is in progress; controlled staging acceptance remains pending.
Use fixture credentials in local browser
checks, including expired-session logout and delayed fetch completion after each
modal/session lifecycle change. On staging use only a disposable synthetic peer
and apply the [staging runbook](../docs/11-staging-validation.md). Never inspect or export
real peer configurations. Track redacted evidence in
[issue #216](https://github.com/ArghTeam/razdacha/issues/216).

## Residual limitation
The stale-401 browser case injects no `Set-Cookie` and checks frontend notification
and rendering lifetime only. The existing backend expires cookies on invalid-session
401 responses; delayed cookie delivery can require another login. Backend cookie
behavior is unchanged. This is an availability limitation, not a confidentiality
failure or authentication bypass.

## Security review gates
The implementation author cannot approve their own change. Before an isolated staging
canary, an independent reviewer must assess the local regression, threat/failure model
and proposed scope/rollback. Require a separate reviewer and primary owner review of the
final diff, tests, documentation and staging evidence.
Final acceptance requires baseline-fails/fix-passes evidence, adversarial negative
cases and controlled staging observations under the [runbook](../docs/11-staging-validation.md).
No unresolved P0/P1 findings may be marked ready to merge. Unknown results stay explicit.
Review approval does not authorize merge or release. These reviews are planned, not completed.
