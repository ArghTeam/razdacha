---
type: spec
tracker: "#290"
---
# First-login web setup

Implement ADR 0035 in the existing authorized PR #287 worktree. Fresh CLI setup
persists pending initial-client metadata and defers default choices to the web.
Authenticated wizard must offer Free/Skip and Auto/country, review, explicit
guarded Apply, existing profile QR/download, completion and explicit reopening.
Absent legacy metadata means completed. No automatic route changes on opening,
selection, polling or closing; no duplicate clients/rules or personal fallback.

Reject unrelated pending edits and concurrent policy replacement using the same
publication gate and transactional policy fingerprint. Preserve source authority;
loading failures must be retryable and honest. Keep secrets out of listings,
diagnostics and public fixtures/artifacts. Add backend and synthetic browser
regressions, maintained context, full checks and independent design/code review.
Root owns staging deployment; never edit another checkout or contact SSH hosts.
