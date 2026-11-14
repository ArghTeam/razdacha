# Netstack — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-11-06

### #325 — WireGuard admission inside guarded publication

**Changed:** Guarded publication reconciles live WireGuard peer rows after interface
setup and before replacing source policy or releasing ingress. Periodic reads and
kernel diffs share the publication gate; startup starts that loop after recovery.
**New surface:** ADR 0040 extends ADR 0023 with kernel admission coherence and
failure/reservation ordering. Recovery cannot replay committed credentials.
**Beware:** Pending live peer edits retain the ten-second minimal-diff schedule.
Unit regressions establish ordering and serialization, not external packet delivery.

## 2026-11-04

### #297 — Isolated encrypted WG and REALITY coexistence

**Changed:** Added a separate opt-in exercise to the existing private kernel WG
quality topology. The generated REALITY runtime and delivered-profile-derived
client run alongside the reciprocal encrypted WG peer.
**New surface:** A reviewed isolated Linux execution passed in 5.31s, requiring
repeated REALITY TCP/UDP and TCP/UDP DNS controls during WG matrix/persistent
traffic, a stable WG peer key and increasing encrypted bidirectional counters.
The supervisor verified unchanged installed binaries/config/service PIDs and
complete owned process/cgroup cleanup.
**Beware:** The first frozen attempt safely failed on a fixture-only directory
mode; production authority checks were retained. The corrected wrapper binds its
owned direct underlay, while WG address/MTU publication still uses the test adapter.
This is not full delivered-TUN, systemd, provider/DPI or production key-lifecycle
evidence. Exact frozen hashes and limitations remain in
[`tasks/297-wg-reality-coexistence.md`](../../tasks/297-wg-reality-coexistence.md).

## 2026-10-21

### #266 — Isolated encrypted client connection-quality fixture

**Changed:** Added an explicitly requested kernel WireGuard ingress to the existing
private NET+MOUNT network fixture, retaining default veth behavior otherwise.
**New surface:** Handshake/counter proof, exact TCP/UDP MTU payload matrix,
DF/fragmentation behavior, bounded persistent sockets/reconnect, and deterministic
underlay loss with measured qdisc drops and recovery.
**Beware:** Synthetic exits and the address/MTU WG publication adapter do not prove
real-provider performance, Meet/WebRTC or production key lifecycle. Kernel capture
reassembly cannot establish absence of incomplete fragments. A reviewed isolated
Linux canary passed all three routing stages, 47 zero-drop barriers and measured
underlay loss/recovery with unchanged production baselines. Build-only
checks are not Linux runtime evidence; see `docs/12-connection-quality.md`.

## 2026-07-29

### #126 — в nft-сет идут подсети правил с любым действием

**Changed:** `RuleMarksSubnets` в `internal/netstack/rule_subnets.go` — единый отбор; `ruleSubnets` переехал в `cmd/razdachad/lists.go` и стал чистой функцией.
**Beware:** `direct` метится тоже, хотя сам по себе метки не требует: списки приходят из `lists` без действия правила, и без этого вышло бы «список работает, ручная подсеть — нет». Сторож — `TestRuleSubnetsAgreesWithListSources`.

### #120 — маршрутизация по метке заводится в каждом прогоне

**Changed:** `route.Apply()` первым шагом замыкания `apply`, до `nft.Apply()`; в диагностике проверка `route`.
**Beware:** на стенде проверено — без правила по метке трафик **не утекает**, а обрывается: помеченный пакет пропадает, а не форвардится. Формулировка аудита «уходит напрямую» неверна.

## 2026-07-28

### #122 — DNS клиента перехватывается принудительно

**Changed:** цепочка `dnsnat` переписывает `udp/tcp dport 53` с `wg0` на наш резолвер; 853 отклоняется; DNS-порты выходят из `mangle` до маркировки.
**Beware:** выход из `mangle` обязателен — иначе запрос на резолвер, чей адрес попал в `razdacha_subnets`, метился бы и уезжал в tproxy мимо перехвата.

### #123 — границы nft-сета читаются из ядра

**Changed:** `diagRanges` собирает интервалы из элементов ядра и сортирует их сам; `DiagSubnetIndex.Missing` сверяет сет с подсетями правил.
**Beware:** ядро отдаёт границы парами «конец, начало» в порядке убывания, `KeyEnd` пуст. Разбор «по порядку» давал один интервал до конца адресного пространства, и покрытие проходило всегда. Роундтрип-тест этого не ловит.

### #117 — аудит утечек: что нашлось в слое

**Changed:** ничего в коде — разбор по коду, `docs/research/security-audit-2026-07.md`.
**Beware:** четыре находки слоя. Закрыты: сет доезжал до ядра за сутки (#119), DNS не принуждался к нашему резолверу (#122). Осталось: `route.Apply` зовётся один раз, после `nft.Apply`, и не диагностируется (#120); подсети правил `action = block` в сет не попадают, хотя списки того же правила попадают (#126).

## 2026-07-26

### #29 — nft-правила и проверка на стенде

**Changed:** таблица `inet razdacha` заливается одной транзакцией; проводка в демон, флаг `--reset-network`.
**Beware:** элементы интервального сета с полем `KeyEnd` ядро 6.12 отвергает с EINVAL — только пара «начало + маркер конца». Тесты с подменённым соединением этого не видят.

### #18 — wg0 через wgctrl

**Changed:** `internal/netstack/wg*.go` — интерфейс, ключи сервера, дифф пиров, статистика, клиентский конфиг.
**New surface:** `WGManager.PublicKey` подключён к хуку `api.Config.ServerPublicKey` — до поднятия интерфейса API честно отдаёт `null`.
**Beware:** `nft` показывает `reject with icmpv6` без `meta nfproto ipv6` — это нормализация вывода, а не потерянное условие.


## 2026-10-19

### #214 — Guarded plain-list network generations

**Changed:** one serialized gate publishes frozen policy/list content through a persistent
client-ingress barrier, with checked preparation, unconditional runtime restart, nft
publication and durable recovery authority. Applied peer addresses remain reserved
across deletion/restart until explicit Apply. Fresh setup uses a distinct checked seed;
legacy setup and DB restore require explicit policy authorization when authority is absent.
**Decision:** [ADR 0023](../../docs/decisions/0023-coherent-network-generation.md).
**Validation:** deterministic local regression/failure/concurrency/recovery tests and
Linux fixture compilation are in progress. Staging wire execution and final independent
security acceptance remain pending; no production deployment is claimed.
**Limit:** autonomous remote `.srs`/`.json` synchronization remains [#220](https://github.com/ArghTeam/razdacha/issues/220).


### Issue 214 prerequisite integration and observer gate

Merged the domain-only DNS prerequisite (ADR 0024) without changing guarded generation
semantics (ADR 0023). The three earlier wire attempts remain unsuccessful precondition
runs and all four original-watcher baseline cases remain unexecuted. The rebuilt baseline
uses the prerequisite main with its original watcher. Fixture evidence now requires a
same-receiver nonce ACK and zero reported packet drops; receiver/output failures invalidate
an observation. These finite isolated checks make no universal lossless-capture claim.


## 2026-10-20

### #214 — Controlled generation validation

**Validation:** canonical attempt 5 at `50aa62a` passed the fixed matrix with four
zero-drop collector ACKs and one final PASS marker. All four original-watcher cases
on prerequisite base `085cbf7` produced the expected ordered desired-protection
failures. Cleanup preserved service/file/host state and removed owned processes and
uploaded payloads. Earlier failed/uncertified attempts remain historical evidence.
**Limits:** actual nft/route/sing-box with a veth WG-address adapter, no encrypted WG
handshake; finite queued capture observations, no universal lossless claim; native
SRS/JSON remains #220. No application or production deployment occurred.
**Review:** pre-canary and owner gates passed. Distinct final review and current CI
are required before merge. See the [completed implementation record](../../tasks/done/214-coherent-network-state.md).


## 2026-10-20

### #220 — Daemon-owned native generations validated

**Changed:** supported native JSON/SRS originals, exact DNS projections and finite
nft projections share daemon-owned immutable authority and the guarded generation.
Unsupported predicates and resource-budget failures retain healthy last-good data;
active artifact failures close ingress. [ADR 0025](../../docs/decisions/0025-daemon-owned-native-rule-sets.md)
records compatibility, quotas and guarded legacy recovery.
**Validation:** source `d5cb9c1` passed four fixed JSON/SRS skew cases and four expected
original-baseline TCP/UDP protection failures, with 16 zero-drop capture barriers.
Distinct independent source/evidence review accepted the result and cleanup. Full
BEFORE-to-AFTER database semantics, policy identity and host/services were stable.
**Limits:** finite isolated nft/route/sing-box observations, a veth address adapter,
no encrypted WG handshake, no application or production deployment. Final docs/current
CI remain merge gates. See the [implementation record](../../tasks/done/220-native-ruleset-coherence.md).

## 2026-10-20 — #231 per-client Internet exit

Whole-Internet interception uses exact committed IPv4 peer sources, not a global destination catch-all. DNS, configured gateway and canonical local destinations precede source TCP/UDP capture; source return prevents unsupported protocols entering ordinary marks. Residual FORWARD traffic from selected sources is rejected without established-flow exemptions. Gateway INPUT remains available, private forwarding and unsupported protocols fail closed, other peers retain behavior (ADR 0028).

Implementation and verification record: [task 231](../../tasks/done/231-peer-egress.md). The bounded isolated Linux fixture and independent synthetic-browser checks passed; the completed record retains the failed first attempt and exact evidence boundaries. The fixture uses veth identities and a synthetic SOCKS exit, not encrypted incoming WireGuard or a DO client flow.

## 2026-11-04 — #297 parallel authenticated client ingress

Added the dormant-by-default Reality backend contract, private live credential boundary, committed admission and guarded publication/lifecycle integration. See [ADR 0032](../../docs/decisions/0032-parallel-authenticated-client-ingress.md) and [task 297](../../tasks/297-alternative-ingress.md) for scope and validation evidence. The netstack invariants record final behavior; this entry does not claim deployment or general DPI resistance.

## 2026-11-06 — #324 final isolated WireGuard admission verification

The final integrated candidate source `c1e8ef1a6e6cfbdfe74dcba222092383b8041c2e`
passed the isolated encrypted WG admission fixture in 3.184 seconds. The Linux test
executable SHA-256 was
`b80d50d79e8c50ed3578213e7499e82ac2c40f411fd6352b7fa2e75efc298019`.
Independent capture barriers and sink controls verified removal before guard
release, old-key TCP/UDP denial and unchanged periodic handshakes. Installed
state and service PIDs were preserved. This was a namespace-isolated synthetic
proof, not an installed-key mutation or production/client lifecycle proof.
See the [audit verification record](../../tasks/done/324-audit-verification.md)
and [completed task 325](../../tasks/done/325-wg-publication-admission.md).

## 2026-11-08 — Bounded selected-exit reliability fixture (#348)

Added a separate opt-in RAC-only exact-endpoint outage tool with exclusive
netlink installation, durable revocation, private bounded serialization, verified
systemd watchdog and isolated kernel roundtrip prerequisite. No production routing,
admission or automatic fallback behavior changed. The roadmap now prioritizes
fail-closed access, approved reserve exits, recovery and real family/friend clients;
subscriptions and commercial access features are excluded. At initial source
delivery, live acceptance remained pending separately from unit tests; the following
entry records its later bounded results.


## 2026-11-08 — Selected REALITY exit acceptance and retained UDP limit (#348)

The [completed task](../../tasks/done/348-reality-exit-reliability.md) records attested
helper source `720fc654a7d44fe58e41a1f96a06e71af0926ebf`, isolated kernel roundtrips,
finite WAN/counter controls and unchanged RC16 application state. Direct succeeded
as a case within a preserved failed combined run; a WARP-only retry completed that
case. Fresh five-protocol matrices recovered in 5.710561/5.716319 seconds over two
attempts. Manual revocation and independent 30-second-watchdog revocation were both
verified; all five fault tables were absent and journals revoked, fixture units stopped,
and temporary clients/profiles/session removed. Configuration, seven protected
user tables, authority, guard and service PIDs were unchanged; no app restart,
Apply, production or DO service/configuration change occurred.

Both retained TLS requests resumed. A new transaction on the same retained WARP
UDP association got no reply within three seconds despite fresh UDP recovery;
this does not establish permanent failure or seamless recovery. Prioritize that
characterization ([#350](https://github.com/ArghTeam/razdacha/issues/350), with
3/10/30-second windows and a no-fault idle control) and explicitly approved
independent-host reserves next. Current
Direct/WARP share one DO host. No automatic failover or fallback was implemented.

## 2026-11-13 — Retained UDP characterization preparation (#350)

- Added an inert borrowed-association STUN observer with identity checks, bounded
  frame parsing and a cross-window transaction ledger; older replies remain
  distinct from responses to a fresh request.
- Specified exact RAC outer-leg fault, independently armed watchdog, sampled
  recovery windows, concurrent fresh controls, comparable no-fault idle control,
  protected-state comparison and unconditional owned cleanup.
- A control-first gate compares five- and twelve-second idle cases and passive
  SOCKS control EOF before any new fault; idle effects must not be labeled outage
  recovery defects.
- Offline socket doubles cover timeouts, delayed transactions, malformed/foreign
  responses, identity replacement and bounded observation. At this preparation
  stage live characterization was pending; the following entry records its result.
  No daemon behavior or recovery guarantee changed.


## 2026-11-13 — Bounded retained UDP idle-confound acceptance (#350)

- [Task #350](../../tasks/done/350-retained-udp-recovery.md) records two complete
  no-fault runs and exact observer/operations/proof hashes. Four retained STUN
  transactions after 5/12/5/12-second idle intervals replied in 0.089–0.247 seconds.
  Separate 30/45-second idle cases already showed SOCKS control EOF before the
  post-idle request; the unchanged local UDP sockets then received no reply within
  three seconds while fresh Direct/WARP associations worked.
- This is an idle confound for the earlier retained-flow observation, not proof of
  a closing hop, precise timeout, historical root cause or active-flow outage bug.
  Nominal ten-second source timeout did not reproduce as failure at twelve seconds.
  No new outage/capture was executed. Preserve the historical #348 record.
- Before/after protected user tables, config/authority hashes, released guard,
  schema/version and application PIDs matched exactly. Owned clients, profiles and
  temporary sessions were removed and independently audited. RC16 remains running;
  no product-code change, Apply, application restart or deployment is needed.
- Next, [#352](https://github.com/ArghTeam/razdacha/issues/352) will characterize
  active UDP recovery with keepalive and matched no-fault history before approved
  independent-host reserves. It is planned; no general UDP/media/failover guarantee
  or implicit Direct fallback is introduced.


## 2026-11-14 — Active retained UDP source preparation (#352)

- Added a single-owner active request/history helper and matched offset replay to
  separate active-flow recovery from the idle confound measured under #350.
- Observer records successful local datagram completion separately from attempt
  start. History checks include prime/phase tails and distinguish bounded sends,
  explicit EOF/send-loss prefixes and unknown malformed/foreign observations.
- Retained negative checks join the complete received ledger to original send
  times, including late/prior transaction IDs. Recovery cannot count a request
  sent before the first verified absence as fresh post-restoration traffic.
- Specified independently watched exact RAC outer-leg fault, post-capture installed
  counter recheck, immediate recovery dispatch, matched no-fault history and
  unconditional exact fault/capture/session/client cleanup. Offline tests pass;
  root-owned live execution and acceptance remain pending. No runtime code,
  configuration, automatic failover or general UDP/media guarantee changed.
