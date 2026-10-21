# Netstack — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

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
