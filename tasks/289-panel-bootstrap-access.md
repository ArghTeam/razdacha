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
