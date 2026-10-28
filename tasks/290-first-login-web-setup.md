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

## Live preserving validation

At `2026-10-27T21:41:25Z`, the primary operator verified uploaded hashes and a
successful preserving upgrade to running code
`deb5924d2362beb301443f1afa042427c3312e8e`, version
`v0.10.0-staging.290.deb5924`, with exact authenticated version/commit and
`installed_match=true`. All three services were active and the original profile
was unchanged.

The actual external API flow without VPN passed: password login, explicit legacy
read-only reopen, Skip/keep-current configure, accepted guarded Apply, existing
profile download matching private original bytes, Finish with pending false,
then explicit reopen with pending true/read-only true/prepared false for the user.
Stable client/rule/tunnel fields matched before/after, with no duplicate objects.
The enabled built-in inventory reported 116 eligible candidates at observation;
catalog inventory may change and does not prove connectivity or application access.

This live installation already had configured free routing. Fresh Free/DE behavior
passed synthetic backend/browser tests; the live proof is preserving/read-only,
not a fresh reinstallation. Actual VPN/media remain NOT TESTED.

A fresh real headless browser context without VPN passed password login and
automatically opened the three-step pending wizard, showing the existing-policy
read-only message and Auto country. Later closed it and exposed both aggregate
cards plus the Setup entry. Observed network activity contained zero onboarding
writes, zero Apply calls and zero profile loads; no QR/private profile was fetched.
The marker remains pending for the user. Documentation-only evidence updates do
not rebuild or redeploy running code `deb5924`.
