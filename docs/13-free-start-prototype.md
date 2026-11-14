# Experimental free-start catalog probe

This increment validates catalog admission and optional configuration acceptance.
The operator probe does not enable free start or change installed state.
Optional fresh-install CLI onboarding is now available for staging under
[task 288](../tasks/288-free-start-onboarding.md) and
[ADR 0033](decisions/0033-opt-in-free-start-onboarding.md). The ordinary built-in
igareck default remains unchanged; actual service usability requires evidence.

The source is the [TelegaVPN free-vpn-keys API](https://github.com/TelegaVPN/free-vpn-keys/blob/main/API.md),
served by `https://vpnkeys.me/api/v1/keys` and
`https://freevpnkeys.com/api/v1/keys`. Requests identify razdacha in User-Agent.
Respect the provider's cache and refresh guidance; this tool is a single explicit
operator invocation, not a polling daemon. Never save a raw response in a public
issue, artifact, fixture or shell transcript: member URIs contain credentials.

## Build and use

```sh
CGO_ENABLED=0 go build -trimpath -o /tmp/razdacha-pool-probe ./cmd/razdacha-pool-probe
/tmp/razdacha-pool-probe --country auto --limit 100
/tmp/razdacha-pool-probe --country DE --limit 100 --sing-box /absolute/path/to/sing-box
```

No catalog URL flag is provided. Country accepts Auto or one two-letter code;
explicit selection also rejects unknown or mismatching structured country locally.
The source request excludes RU/BY, requests supported protocols and forces JSON.
The local country/title and encryption filters still apply. There is no default
`for_russia`, reputation or uptime restriction: those provider observations do
not prove usable supported nodes. One batch defaults to 100 and caps at 240;
there is no pagination. At most two HTTP requests use the original complete query
across the official mirrors; redirects are disabled. Valid empty or malformed
responses never weaken filters. Each request has a 15-second ceiling, each body
an 8 MiB ceiling, and the overall timeout defaults to 45 seconds, at most 1 minute.

The JSON report separates source items, admission rejection, parser rejection,
duplicates, local filtering and eligible members. `has_more` means this is a
bounded sample, not a complete source inventory. A valid empty or wholly excluded
batch reports `no_eligible_members` and exits nonzero; retrieval/schema failure
reports `failed`. No keys, remarks, provider hosts, raw checker output or installed
credentials appear in the report. Provider latency stored in `PingMS` remains a
provider observation, never a measurement from this gateway.

Without `--sing-box`, configuration remains `not_requested`. With it, the tool
copies the explicitly selected executable into private temporary storage and
reports that copy's SHA-256. It checks an ordinary generated configuration over
synthetic default settings and the pool only. The normal runtime window of at
most 64 members still applies: `config_members` counts what the checker accepted,
which can be less than catalog eligibility. Checker rejection fails the candidate
as a whole; private diagnostics are discarded instead of pruning members silently.
Only `Applier.Prepare` is used. The tool never commits, reloads, starts a proxy,
reads the installed database/configuration or modifies networking. Temporary
directories are mode 0700 and configurations 0600, removed on return. As with any
process, forced termination can prevent cleanup; use a private temporary root.

`configuration: accepted` is not connectivity evidence. Connectivity and media
always report `not_measured`; there is no TCP, UDP, download, call or video proof.

## Staging evidence gate

Guarded fresh-install onboarding passed on the user-authorized replacement
staging target. At `2026-10-27T20:03:46Z`, private operator evidence recorded:

- Running code commit `dc073b2f85a3e8d00b25a97838575b888aae4361`, staging version
  `v0.10.0-staging.288.dc073b2`, runtime sing-box `1.12.25`; uploaded binary hashes
  were verified and authenticated HTTPS API version/commit assertions matched.
- `setup --free-start=true --free-country=auto` exited successfully; daemon,
  sing-box and nginx were active. Committed `applied-network.json` existed and
  temporary bootstrap authority `bootstrap-network.json` was absent.
- Exactly one client and one enabled rule, with selected scope restricted to
  that first client. Sources were `russia_inside`, `meta`, `twitter`, `discord`.
- Runtime pool inventory contained 96 members; runtime health reported 38 alive
  and a current selection. These are runtime observations, not client/media proof.

The earlier shadow-only probe on this target observed 100 source items, 94
eligible and one request. Fresh installation fetched again; different counts
are expected for a changing free catalog. The original #286 target was
unreachable; that historical attempt remains NOT RUN, not a passing deployment.
Connection values, credentials, profiles and raw operator output remain private.

The first-install evidence above belongs to code `dc073b2`; the later preserving
upgrade below identifies the current validated candidate. Documentation-only
commits do not rebuild or redeploy either candidate. Keep the PR draft until
the remaining actual client/media checks are reviewed.

At `2026-10-27T21:01:45Z`, the preserving upgrade to code
`0fa13ea14799134161a1cdb64df221a77bc014a9`, version
`v0.10.0-staging.289.0fa13ea`, passed. Uploaded binary hashes were verified;
setup without panel flags preserved public mode, all three services and the
original client profile. The version API matched code/installed version with
`installed_match=true`. One client and its selected-scope starter rule remained.

Direct external checks without VPN passed: HTTPS 200, protected API 401 before
authentication, password login, and authenticated profile download matching the
original private bytes. This establishes panel bootstrap access and preserving
upgrade behavior. A fresh no-override reinstall was NOT RUN; the new fresh
public default is covered by synthetic regressions. VPN/media remain NOT TESTED.

Further transport-quality checks may use a separately reviewed isolated sidecar
(HTTPS transfer larger than 64 KiB, public UDP DNS/STUN), alongside a dedicated
staging client/control test and unavailable-pool rejection. Never feed
provider credentials into the synthetic no-uplink connection-quality fixture.
Video/call suitability remains unverified until actual client evidence is reviewed.
External-client traffic, large HTTP transfers and public UDP tests have not run.

## Optional fresh-install onboarding

Every genuinely fresh installation defaults to the existing password-protected
public HTTPS panel, whether Free start or Skip is selected. The external URL and
generated password are printed before the client QR/configuration instructions,
so panel access does not depend on an already imported VPN profile. TLS remains
self-signed. Explicit `--public=false` or `RAZDACHA_PUBLIC=0` selects VPN-only
access; existing installations retain their saved mode without explicit override.
This bootstrap-access correction is specified in [ADR 0034](decisions/0034-fresh-panel-bootstrap.md).
It does not change the catalog query, free-start choice or selected-client policy.

Bootstrap-access evidence is separate: the existing `dc073b2` staging installation
was corrected using supported `setup --public=true`. External HTTPS, rejection of
unauthenticated protected API requests, password login and authenticated client
listing were verified without a VPN. The new fresh default is regression-tested;
upgrading that existing installation does not establish a fresh no-override install.

Fresh `razdacha setup` now defers default free-tunnel choices to the authenticated
first-login web wizard (ADR 0035), instead of asking terminal questions. The
installer prints public access/password and seeds pending metadata for the existing
first client; no catalog is fetched until explicit opt-in. Explicit CLI/env
automation below remains supported and its created starter policy is recognized.

For an unattended fresh installation:

```sh
razdacha setup --free-start=true --free-country=auto
# The release installer inherits these environment values:
RAZDACHA_FREE_START=1 RAZDACHA_FREE_COUNTRY=DE sh install.sh
```

`RAZDACHA_FREE_START` accepts true/false or 1/0; explicit flags override environment.
`RAZDACHA_FREE_COUNTRY` only chooses a country and never enables the feature by
itself. `--free-start=false` skips. Existing installations never prompt or fetch
onboarding sources; an explicit enable or country request is rejected before
opening/migrating/backing up the database. Remove onboarding flags/environment
when updating. Existing pool/rules are never implicitly changed.

Opt-in uses the one built-in pool at the bounded JSON API and one selective
starter rule scoped only to the new first client. The rule uses the ordinary
preset without `google_ai`: `russia_inside`, `meta`, `twitter`, `discord`.
There is no catch-all, broad ASN list, personal tunnel or fallback. Other clients
receive no implicit assignment. Skip retains today's disabled igareck behavior
and performs no onboarding catalog/list requests.

All starter sources are acquired synchronously with the existing Fetcher under
the canonical database-parent `lists` cache, with immutable native originals/DNS
projections. The overall acquisition deadline is two minutes and each source
request is bounded at 15 seconds. Stale fallback is refused. Acquisition happens
before database creation, so a transient source failure can be retried safely.
The exact verified source map feeds both the first checked configuration and v4
bootstrap seed, then the daemon's existing guarded recovery publishes it. Missing
source contents never create a permissive empty-list bootstrap. Later setup
failures preserve existing files/state for explicit recovery; retries do not
reinterpret an existing database as a fresh opt-in.

Catalog admission failure retains an enabled empty pool and starter policy;
selected destinations reject until candidates become available. It never changes
to direct routing or another source. Source/configuration failure stops setup
before service startup or seed publication. Admission counts appear separately
from runtime health; installation alone does not prove a working exit or media.

## Authenticated web setup

After login, genuinely fresh installations show three steps: choose Free/Skip
and Auto/country, review and explicitly Apply, then show/download the existing
first client's saved WireGuard profile and Finish. Selecting an option, opening,
polling or closing the wizard does not change routing. Profile secrets load only
on an explicit delivery action and are cleared with the modal. Setup can be
reopened through the header's Setup button; completion is persisted server-side.
Legacy installations without metadata are completed by default, regardless of
their client count. Explicit authenticated reopening changes metadata only.

Reopened legacy installations with existing rules/personal setup are read-only:
the wizard keeps current policy and delivers the selected existing profile. It
does not adopt a rule by name, duplicate clients/rules, change a shared pool's
country or silently disable existing exits. Editable fresh setup tracks the exact
owned starter rule, verifies its full scope/destinations and rejects extra shared
references. Skip preserves existing configured policy.

Configure checks that unrelated edits are not pending and transactionally pins
the expected routing fingerprint. Apply checks that same intent under the daemon
gate before ordinary guarded publication. Fingerprints ignore only refreshable
pool inventory, never routing, manual-tunnel credentials or source country. Changed unrelated
policy produces conflict rather than being silently applied. A failed Apply keeps
choices for retry; while prepared intent is pending, the choice stays locked.
Closing an in-flight explicit operation does not undo already saved intent;
reopening resumes it rather than starting a duplicate operation.

The daemon's existing source Manager acquires missing starter artifacts on explicit
Apply, with a 20-second acquisition budget inside a 45-second request context,
below the HTTP server write timeout. Cancellation is checked before publication.
Missing/invalid native authority never bypasses guarded checks. Source failures
remain retryable and Finish requires accepted, still-current applied policy.
Config acceptance still does not prove application usability.
An accepted enabled pool with no candidates is explicitly identified in the final
step: assigned resources reject until the built-in pool is refreshed, without
direct fallback. Redacted current inventory is distinguished from runtime health.

Authenticated endpoints: `GET /api/onboarding` and
`POST /api/onboarding/{reopen,configure,apply,finish}`. Only mode/country and an
optional existing client selection are accepted as setup intent; catalog URLs are
owned by the server. Responses exclude internal fingerprints, keys and diagnostics.
No credential or host value belongs in public evidence.

## Aggregate client traffic

Clients again shows gateway received/sent totals for current clients since the
gateway interface started. Each direction is Unknown if any current client has
missing, invalid or negative observations; measured zero remains zero. Per-client
rows remain present and apply the same unknown-data rule. This is interface-reset
scope, not lifetime history. The separate `56e8bcb` staging restoration was verified
at `2026-10-27T21:19:12Z`: preserving setup upgrade, active services, exact version,
unchanged profile, served aggregate cards and numeric API counter observations.
