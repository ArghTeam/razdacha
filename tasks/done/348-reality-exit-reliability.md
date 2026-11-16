# Selected REALITY exit failure and recovery (#348)

Status: Complete for the bounded selected-exit failure/recovery test. Retained UDP
recovery characterization and independent-host reserve exits remain future work.

## Goal and scope

Refocus [roadmap #338](../338-client-connectivity-roadmap.md) on stable Internet access
for family and friends. Subscriptions, billing, tariffs, quotas and commercial
access distribution are excluded. Preserve completed milestones and historical
ADRs/tasks. Approved reserve exits and recovery are future work, not implicit
Direct fallback (ADR 0013/0028).

The bounded first task tests the existing RAC REALITY clients and applied exits
without changing profiles, clients, rules, credentials, application configuration,
committed authority, DO services or production. The running candidate is RC16,
source `a945231fbf78659c100df5049b5dc77f6a636839`. Exact runtime and protected-state
hashes must be checked immediately before and after the experiment.

## Intended fixture

A separate opt-in Linux helper uses `google/nftables`, never `nft`, `ip` or `wg`
subprocesses. It can affect only RAC `193.233.112.227` and the literal outgoing
TCP endpoints `165.22.196.130:443` or `165.22.196.130:2443`, one at a time. This
is an EXIT outage: other RAC rules sharing that endpoint are temporarily affected.
It is not a per-client firewall and not a failure of WARP inside DO. Both exits
share one DO host, so they do not provide independent-host redundancy.

`prepare` creates a fresh private run journal with a 128-bit run ID and an
absolute deadline no more than 90 seconds away, boot identity and network-namespace
device/inode identity. All observation/revocation requires that same prepared
namespace, even if its RAC interface address later disappears. The controller arms a separate
systemd watchdog timer/service before starting the fault unit. `hold` verifies
its own unit identity and the watchdog's active timer, triggered service, command
and bounded deadline. It then atomically adds one unpredictable owned inet table
with one early OUTPUT chain and exact IPv4/TCP destination counter/DROP rule.
The immutable deadline is checked immediately before and after installation.

Foreground timer/signal cleanup and a bounded fault-unit lifetime complement the
independent watchdog. Cleanup records durable revocation under the same private
file lock before deleting the exact verified table. Persistence or unit-stop
errors still attempt exact owned removal and remain explicit failures; address
loss cannot disable revocation. A delayed installer cannot
add a rule after revocation. The watchdog stops only its deterministic owned fault
unit and retries exact cleanup; it never stops application services. Table absence
and stopped fault unit must be established before canceling the watchdog. Foreign
or replaced tables are never deleted. No application routing or admission changes.

## Acceptance evidence

- Inventory current consumers of each exact outer endpoint. Announce the exit-wide
  sandbox impact and retain a functioning other-exit control.
- Use current exports of the two existing owned test REALITY clients with only
  loopback mixed inbounds substituted. No TUN, host route/DNS change, other user
  WG keys, old cloned production credentials or DO-side operations.
- Compare healthy baseline, bounded outage and recovery for new TCP/HTTPS and
  small UDP STUN requests. Keep one existing TLS socket and UDP association across
  the outage. Generate fresh nonces/transaction IDs after the fault barrier;
  buffered pre-barrier bytes are not a new response. Record retained-flow outcomes
  without promising uninterrupted survival.
- Require negative request results, positive DROP counter hits and an independent
  zero-drop finite WAN capture oracle: no direct target SYN/UDP or owned plaintext
  marker. Positive direct controls must not contaminate the negative interval.
  DNS observations are separate and must reflect actual applied/shared DNS policy;
  lack of one packet is not proof of all-DNS privacy.
- Fresh requests recover after removing the fault, without Apply, restart or
  client/policy changes. Compare protected user rows, config/authority hashes,
  runtime PIDs and released ingress guard before/after. Remove only owned profiles,
  sockets, client processes, captures, temporary session and fault resources.
- Run helper safety/lifecycle/ownership tests, relevant Go/vet checks and independent
  source/operations review before host mutation. Pin the helper source/binary hash.
  Record failed attempts honestly. Source readiness is not live acceptance.

No automatic failover is implemented. The results inform a separate reviewed
proposal for explicitly approved reserve exits; health observations never grant
permission to release selected traffic through Direct.

## Source verification before live use

The helper has 25 Linux safety tests, including exclusive netlink batch flags,
exact schema/counter ownership, foreign replacement refusal, persistent journal
failure cleanup, expired/stalled installers, concurrent cleanup, monotonic timer
bounds, future watchdog credentials, actual executable identity and same-namespace
revocation. Portable tests and focused race checks pass; the compiled Linux test
binary passes in a networkless local container. These tests do not install host
kernel rules. Complete CGO-disabled Go tests and vet pass; the project-pinned
Linux linter reports zero issues for the final helper after applying the pinned
formatters. Application runtime code and dependencies are unchanged.

At initial source delivery, attested isolated-kernel and live operations acceptance
remained pending. The following record distinguishes those later results from
source readiness and preserves unsuccessful attempts.


## Final bounded acceptance — 2026-11-08

### Provenance and unchanged application state

The standalone helper source is `720fc654a7d44fe58e41a1f96a06e71af0926ebf`;
its attested Linux amd64 binary SHA-256 is
`aefcd7c9e1c79711b20d7d9023a9f1aad15fb435c198ac4dcef9df58c7e0c665`.
The native sing-box 1.12.25 client binary SHA-256 is
`ffc693b9edf2f83f3284b94b5e73421555b3f0e2ec166f0193dd6e142e3f4c0d`.
The synthetic browser clock correction at `caaebbfc0ef739289e209a444cf5a8109ca51420`
changes only a test: it seeds time before the fixed sample epoch. All 54 targeted
actual-module cases passed with the real date already later than that epoch;
new-head CI passed. Helper code/dependencies remain byte-identical to the attested
source. No new daemon build, deployment, Apply or application restart occurred.

RAC remained on `0.11.0-rc.16.rac.a945231fbf78`, application source
`a945231fbf78659c100df5049b5dc77f6a636839`. The fresh-process isolated network
namespace kernel selftest passed both endpoint roundtrips on its first attempt,
including exclusive collision rejection and exact production-backend removal.
The parent namespace was preserved; no host firewall rule was changed by that
selftest. Independent source, operations and actual-evidence reviews passed.

Both current owned REALITY profiles used local loopback mixed listeners with only
inbounds substituted. No TUN or machine route/DNS change occurred. The inventory
also found two selected Test Client rules sharing these exits: the blackout was
announced as exit-wide, not peer-isolated. Production and DO service/configuration
were unchanged. The injected failure covered only RAC-to-DO outer TCP443 or
TCP2443; it did not fail WARP inside DO.

Before/after hashes matched exactly:

| Protected object | SHA-256 |
| --- | --- |
| Seven user tables: settings, peers, tunnels, rules, REALITY identity/credentials, WARP registrations | `64f12535ecd4ff925d12ef0b47baf5054006bbaec8b796c0ffc21da7cbd2a950` |
| Checked sing-box configuration | `302faf5b71ae4ab6777d7a5c98575fa9ea516713f17171074b0715f91f9d0e14` |
| Applied network authority | `d253665bb5ea39cef8b9c5ef37e47221ca8dd3e6eddb76fe4efc02d182c0d8d9` |

The ingress guard stayed false. Daemon/sing-box/nginx PIDs stayed
`269709` / `269726` / `8241`. Temporary session rows and ordinary health observations
are separate from the seven protected user tables; this is not a claim that every
SQLite row remained unchanged.

### Accepted cases and preserved failed run

The fresh five-protocol matrix was numeric HTTP/TCP and certificate-verified HTTPS
at `1.0.0.1:80/443`, numeric UDP STUN at `162.159.207.0:3478`, and gateway DNS
transactions over UDP/TCP53 with fresh owned names. Both clients passed healthy
baselines. Every negative window had capture readiness before post-barrier probes,
a positive increase in the exact endpoint DROP counter and a functioning other-exit
control. Separate positive calibration observed direct TCP/HTTP nonce and STUN
transaction traffic outside the negative capture intervals.

| Accepted case | Direct outer leg | WARP outer leg |
| --- | --- | --- |
| Overall attempt | Direct case accepted within **failed** run `cd1168d3a4fefcf185e53f9ee7aa5ab3` | WARP-only run `cde05fd0db8687af296c53d1f10de1e0` completed successfully |
| Endpoint | `165.22.196.130:443` | `165.22.196.130:2443` |
| DROP packet counter | 2 → 56 | 0 → 54 |
| Negative capture packets / kernel drops | 340 / 0 | 162 / 0 |
| Direct fixed-target TCP / STUN UDP packets | 0 / 0 | 0 / 0 |
| Owned plaintext HTTP/STUN markers or DNS names on WAN | 0 | 0 |
| Selected fresh requests | HTTP/HTTPS had no response; STUN and both DNS transports timed out | HTTP/HTTPS had no response; STUN and UDP DNS timed out; TCP DNS closed without a valid response |
| Other exit control | WARP five-protocol matrix healthy | Direct five-protocol matrix healthy |
| Fault removal | Verified manual revocation and table absence | Independent watchdog revocation and table absence |
| Full fresh two-exit matrix after confirmed removal | 5.710561 seconds, two attempts | 5.716319 seconds, two attempts |

The Direct case is accepted from its completed counter/capture/control/recovery
subrecord, not by relabeling the containing run successful. That overall run later
failed in the WARP case because its DNS TCP classifier treated clean EOF as an
indeterminate error. The corrected WARP-only retry preserved the failed artifact.
In the probe wrapper, HTTP/HTTPS `ok=true` can mean a returned failure observation;
acceptance uses `response_seen=false`, `target_contact=false` and timeout fields,
not that wrapper boolean.

For the WARP-only case, the independent timer used a 30-second monotonic trigger
before the immutable 60-second holder deadline. The watchdog actor's journal
message at monotonic microsecond `1329310292420` precedes deadline
`1329338429941`; it reported revoked/table-absent state, and the fault PID was zero
before controller cleanup/cancellation. Thus this case exercised the independent
watchdog, not merely the foreground TTL or the controller's finally block.

Recovery timings measure completion of the full fresh matrix, not an exact
first-possible recovery instant or an SLA. Two attempts were needed; the Direct
case included a transient HTTPS failure in the WARP control. Neither client nor
application services were restarted to recover fresh requests.

### Retained-flow limitation and next priority

Both retained TLS sockets stayed open: their pending post-barrier requests had no
response during the outage and returned HTTP200 after restoration. The Direct
retained UDP association accepted a new STUN transaction after discarding one stale
reply. For WARP, a new STUN transaction on the **same retained UDP association**
received no response within the three-second post-restore observation, while fresh
UDP associations already worked. This is a real bounded recovery gap to investigate;
it does not establish permanent failure, eventual recovery time or seamless old-flow
survival. The next priority is retained-UDP recovery characterization before a
promise about automatic failover or uninterrupted applications. Planned
[follow-up #350](https://github.com/ArghTeam/razdacha/issues/350) requires repeated
3/10/30-second observation windows and a no-fault idle control before attributing
the symptom to the outage, diagnosing expiry or implementing a fix.

Both exits use the same DO host/IP. They cannot protect against that host/provider
failing. A separately reviewed reserve proposal must use explicitly approved exits
and consider an independent host; it must retain rejection rather than implicit
Direct when no approved exit is usable. No failover/fallback was implemented here.

### Failed attempts, cleanup and limits

Preserved attempts are not hidden or counted as successful end-to-end runs:

- `98591adafeb2f233f2d1e97028bd0f74`: local provider-origin allowlist rejection;
  no host operation.
- `b85516beba66d234a78208ce9d6dc51a`: local client IPv4/API baseline bug;
  no fault/capture installed.
- `78152a000b4a21c965bf4f93dc999aa9`: healthy baseline followed by capture-code
  generation failure, no fault table; the owned capture was explicitly stopped.
- `67b860b9a49198569b3dbad17c1616cf`: healthy baseline/calibration followed by
  watchdog metadata compatibility failure, no fault table. Its journal was revoked
  and watchdog elapsed without an active fixture left behind.
- The arm-only rehearsal `a515551037a941b823eefd17c64d97af` passed without installing
  a filter. A separate local hash-guard race abort and its revised metadata-aware
  rehearsal remain recorded as setup/tooling evidence, not network acceptance.
- `cd1168d3a4fefcf185e53f9ee7aa5ab3` remained failed overall despite its accepted
  Direct case; only the later WARP-only retry completed that case.

The final read-only audit confirmed all five tracked journals revoked, all fault
tables absent and zero loaded fixture units. Owned local clients/captures stopped,
temporary profile copies and the owned authenticated session were removed. Private
journals remain as tombstones; raw captures and evidence stay private. No raw pcap,
profile, credential or user settings content is committed.

The WAN oracle is finite and target-specific, supported by zero reported capture
drops and positive calibration. It is not universal leak freedom, per-client DNS
attribution across all shared infrastructure resolvers, authoritative DNS delivery,
all DoH/DoT privacy, a full Android/router test, media quality, arbitrary TCP/UDP,
inner-WARP failure or independent-host outage protection. Existing upstream/bootstrap
DNS infrastructure was inventoried separately from the two applied peer-DNS detours.

Private evidence is retained under `/private/tmp/razdacha-reliability-20261108/`.
Aggregate artifact hashes make the records identifiable without committing payloads:

| Evidence artifact | SHA-256 |
| --- | --- |
| Final private operations hash manifest | `3c534d0e9b4956ba68b058896bc1521b208e42d3a20797ee179aeb8a4c2f4f9b` |
| Failed combined run status containing accepted Direct case | `e341fb1764d122d947110e675534aab0655be2d2b4acbefe081075c47b82e9a2` |
| Successful WARP-only final proof | `ea8281f16b548ea55f6bc41d7f614e5368748975d3e39120a68e31fde3f0fd49` |
| Direct negative pcap | `1c0170a69b71e1d69bead7a8c771df4bd17aa0f2ef77ef132516e86573185175` |
| WARP negative pcap | `b97aa5a7cda7ce1f538157a8a7b303863b9779b640427fa5140ecb2a6622e308` |
| Final resource/state audit | `5009c3e260c8ed59cff50d4ff67a1db0e9ec21b39d4b6ff413a901e3f0b7881e` |
