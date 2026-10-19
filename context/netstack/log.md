# Netstack — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

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
**Review:** pre-canary and owner gates passed; distinct final evidence/docs review is
pending. See the [completed implementation record](../../tasks/done/214-coherent-network-state.md).
