---
type: spec
tracker: "#288"
---
# Optional fresh-install free-start onboarding

The user authorized actual staging installation and an install-and-use flow.
Extend the API prototype from #286 with an explicit fresh-install choice: free
third-party tunnels with Auto or one country, or Skip. Never alter an existing
installation's tunnels/rules or prompt during an upgrade. Personal exits and
personal-to-free fallback remain excluded.

Implementation stays on `feat/286-free-start-prototype`, PR #287, based on
freshly fetched `origin/main` at `61afab058acbbf02febf8adf2eb225786d4bc433`.
Absolute checkout and staging connection details remain private.

## Acceptance

- Fresh interactive setup reads `/dev/tty`, including when piped from curl.
  EOF, no terminal and the default choice skip; explicit flag/environment can
  enable unattended installation. A short English prompt explains third-party
  exits and absence of usability guarantees. Default country is Auto.
- Opt-in creates/enables the one built-in pool using the official bounded JSON
  API with supported protocols and RU/BY exclusions. Skip preserves the daemon's
  ordinary disabled igareck default and makes no catalog/list requests.
- One enabled selective starter rule uses `lists.Preset()` without `google_ai`,
  scoped only to the newly created first peer. No whole-Internet or ASN policy.
- Acquire every rule source through the existing bounded Fetcher into canonical
  immutable cache authority before writing the first runtime configuration.
  The same frozen source contents feed configuration generation and the v4
  bootstrap seed. The normal guarded daemon recovery publishes that authority.
- Failed catalog admission retains an enabled empty pool and selected-resource
  rejection; failed source acquisition/configuration validation aborts before
  service startup or bootstrap publication. No direct/personal/source fallback.
- Synthetic tests cover choices, EOF/noTTY, flags/env/country, upgrade isolation,
  first-peer scope, coherent frozen sources and failure behavior. Full tests,
  lint, static builds and independent review precede staging deployment.
- Report admission independently from health. Client, video/call and transport
  quality require actual staging evidence; never infer success from installation.

## Staging validation evidence

At `2026-10-27T20:03:46Z`, the primary operator confirmed the fresh deployment
on the newly user-authorized staging target. Running source commit is
`dc073b2f85a3e8d00b25a97838575b888aae4361`; version is
`v0.10.0-staging.288.dc073b2`, with sing-box `1.12.25`. Uploaded binary hashes
and authenticated HTTPS API version/commit assertions matched.

Setup with `--free-start=true --free-country=auto` exited zero. Daemon, sing-box
and nginx were active; committed authority existed and bootstrap authority was
discarded. Exactly one client and one enabled selected-scope rule targeted only
that client, with `russia_inside`, `meta`, `twitter`, `discord`. Runtime inventory
had 96 pool members, with 38 reported alive and a current selection.

The earlier reviewed shadow probe observed 100 items and 94 eligible. Installation
performed a fresh fetch, so the count difference does not imply lost authority.
The original #286 target was unreachable; this successful deployment uses the
explicitly authorized replacement. No connection values or credentials are public.

External-client traffic, media/video/call, large HTTP transfers and public UDP
validation remain pending. Runtime health is not application usability evidence.
Documentation-only evidence updates do not rebuild/redeploy the code commit above.
Keep #286/#288 open and PR #287 draft until the remaining evidence is reviewed.
