---
type: spec
tracker: "#289"
---
# Fresh-install panel bootstrap

The user authorized correcting initial access: the first client is not connected
to the VPN yet, so a VPN-only panel address cannot be the initial access route.
Continue PR #287 on its existing feature branch without merging.

- Every genuinely fresh installation defaults to existing public authenticated
  HTTPS unless explicit private mode is selected. Free start and Skip both need
  panel access before importing a VPN client profile.
- Existing-installation saved panel mode is preserved without explicit override.
- Use existing panel persistence, TLS/SAN and nginx PublicSiteConfig machinery.
- Print the real external URL/password before client QR/configuration instructions.
- Do not change API query filters, pool policy, authentication or guarded authority.
- Cover fresh opt-in/private override/skip/upgrade and output ordering with tests;
  run relevant checks and independent review before deployment.
- Keep real host values, credentials and raw staging output private.

## Validation boundary

The existing staging installation on code `dc073b2` was corrected operationally
using supported `setup --public=true`, preserving its committed policy. External
HTTPS returned 200, protected API returned 401 without authentication, and password
login plus authenticated client listing succeeded without a VPN connection.

The new no-override fresh default is covered by synthetic regressions, including
Free start, Skip, explicit private selection and saved upgrade modes. A subsequent
candidate upgrade must preserve existing clients; it is not evidence of a new
fresh no-flag installation. Do not wipe staging state merely to broaden that claim.

## Preserving candidate upgrade evidence

At `2026-10-27T21:01:45Z`, the primary operator verified the preserving upgrade to
running code `0fa13ea14799134161a1cdb64df221a77bc014a9`, version
`v0.10.0-staging.289.0fa13ea`. Uploaded binary hashes matched. Setup without panel
flags preserved public mode, all three services remained active, and the original
client profile was unchanged. The authenticated version API matched code/installed
version with `installed_match=true`.

From outside the VPN, HTTPS returned 200, protected client listing returned 401
without authentication, password login succeeded, and authenticated profile
download matched the original private profile. One client and one selected-first-
client rule were preserved. These are direct panel-bootstrap checks without a
VPN, not VPN tunnel or media tests.

A fresh no-override reinstall was NOT RUN; synthetic fresh/default/private/Skip
regressions passed. VPN/media remain NOT TESTED. Later documentation-only commits
do not rebuild or redeploy running code `0fa13ea`. Keep the PR draft and issues
open pending remaining validation.
