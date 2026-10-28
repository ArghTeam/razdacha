# 0034. Fresh-install panel access before VPN connection

Date: 2026-10-27. Status: experimental; supersedes the private fresh-install
default in ADR 0009 and clarifies ADR 0033. Explicit private mode and existing
saved modes remain authoritative.

## Context

A freshly installed client has no VPN connection until it imports the generated
configuration. A VPN-only panel URL therefore cannot bootstrap the initial user
experience. The user explicitly requested usable panel access immediately.

## Decision

Every genuinely fresh installation defaults to the existing public
password-authenticated HTTPS panel unless the user explicitly chooses private mode with `--public=false`
or `RAZDACHA_PUBLIC=0`. Persist that choice through the existing panel-mode path,
PublicSiteConfig and certificate/SAN handling. Print the real external HTTPS URL
and generated password before the client QR/configuration instructions.

This applies to Free start and Skip alike: a user who skips needs the panel to
add personal tunnels. Do not change the saved mode of an existing installation.
No temporary unauthenticated endpoint,
new authentication scheme or invented external address is introduced. Existing
self-signed certificate behavior remains explicit in setup output.

## Consequences

The fresh default supersedes the private fresh default of ADR 0009 and clarifies
the initial experience of ADR 0033. API source queries, pool membership, selected
first-client policy and guarded bootstrap remain unchanged. Public access still
requires the generated password; actual external HTTPS/authentication evidence is
separate from VPN/client/media usability.
