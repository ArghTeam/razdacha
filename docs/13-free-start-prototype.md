# Experimental free-start catalog probe

This increment validates catalog admission and optional configuration acceptance.
It does not enable free start, modify the installer or change the built-in
igareck pool. The initial installation choice, starter rules and live validation
are subsequent work ([task 286](../tasks/286-free-start-prototype.md),
[ADR 0032](decisions/0032-api-pool-shadow-probe.md)).

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

Status: **NOT RUN — designated staging is unreachable.** No other host is an
authorized substitute, and no production activation is part of this increment.

After independent review and restored staging access, record the exact commit,
probe binary hash, selected checker hash/version, command flags and redacted JSON
report. Verify installed configuration hash, service PID and database state are
unchanged before and after. Keep connection details and before/after output in
private operator evidence. Do not classify an unreachable host as a passing test.

Only a separately reviewed isolated sidecar can establish candidate transport
quality (HTTPS transfer larger than 64 KiB, public UDP DNS/STUN), followed by a
dedicated staging client/control test and unavailable-pool rejection. Never feed
provider credentials into the synthetic no-uplink connection-quality fixture.
Installer/onboarding behavior remains deferred until that evidence is reviewed.
