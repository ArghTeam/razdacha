# Singbox — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-10-20

### #215 — preserve selected scope after peer deletion

**Changed:** Skip selected rules without existing source addresses before any route, reject, chain or set construction. Disabled surviving clients retain scoped rejection.
**Decision:** [ADR 0026](../../docs/decisions/0026-peer-deletion-preserves-scope.md).
**Validation:** Local tests/static checks and controlled four-case wire observations completed on `14e28b3`: two fixed passes and two positively proven original-base broadening failures, with 114 zero-drop capture barriers. See the [completed specification](../../tasks/done/215-safe-peer-deletion.md) for evidence and limits. Final documentation/evidence review and current CI remain merge gates.

## 2026-10-19

### #222 — Separate immediate DNS matching from destination CIDRs

**Changed:** Manual/plain DNS projections contain domains only; complete route
sets and source-peer constraints remain. Empty projections omit DNS actions.
Diagnostics retain possible CIDR routes after real DNS answers.
**Decision:** ADR 0024 clarifies ADR 0013; native predicates stay unchanged and
remain part of #220. Actual pinned-runtime regression fails on the original base
and passes on the fix. The isolated wire run on `2c18017` reached eight expected
baseline DNS refusals and eight fixed passes; config/nft restoration, restart and
cleanup passed without a product deployment. Attempt 1 remains incomplete/failed.
The nonce barrier covers queued observations; no packet-drop statistics or universal
leakage guarantee is claimed. See [completed evidence](../../tasks/done/222-dns-rule-scope.md).
Separate independent pre-run and final reviews remain required.


## 2026-08-21

### #202 — отсеянный член пула не доходит до конфига

**Changed:** `internal/singbox/pool.go`, `pool_reject.go`, `tunnel.go` — `selectPoolServers` гонит состав через `store.PoolFilter` на пути от БД к конфигу.
**Beware:** фильтра на обходе каталога мало. Пока обход не удался ни разу, конфиг собирается из сохранённого состава, а при мёртвом источнике оно живёт сколько угодно — РФ-нода пережила бы апгрейд.

## 2026-08-18

### #194 — потолок пула 16 → 64

**Changed:** `poolMaxServers` 16 → 64 (равен `lists.PoolConfigServers`, `TestPoolConfigWindowAgrees`). Цена — 64 outbound'а и 64 пробы каждые `PoolTestInterval`, внутри процесса.

### #187 — пул терпит битого участника

**Changed:** `apply.go` цикл: `check` упал на участнике пула → его выкидывают, конфиг пересобирается, окно доберётся (ADR 0019); `pool_reject.go` — `outbound[N]`→тег→сервер.
**Beware:** строго `source==pool`; ручной туннель валит применение; `endpoint[N]` пулу не принадлежит.

### #183 — reality без fp получает uTLS chrome

**Changed:** `parse_url.go`: при включённом `Reality` ставится `uTLS` (fp из ссылки или `chrome`) — без uTLS sing-box валит весь конфиг, и один такой ключ блокировал общий пул #181 (класс #176).

## 2026-08-17

### #176 — неподдерживаемый flow отбраковывается разбором

**Changed:** `parse_url.go` (vless) валидирует `flow` — только `""` и `xtls-rprx-vision`, иное → `ErrParse` (как `encryption`).
**Beware:** легаси-flow из подписки (`xtls-rprx-vision-udp443`) иначе ронял весь конфиг и блокировал 5 из 7 страновых пулов.

## 2026-08-15

### #149 — отказ правила виден в панели

**Changed:** экспортированы `FakeIPRange()` и `RuleInConfig(rule, plain)` в `internal/singbox/ruleset.go` — второй считает через тот же `buildRuleSets`, что генератор.
**New surface:** `FakeIPRange()`, `RuleInConfig(rule, plain)`.
**Beware:** `TestRuleInConfigAgreesWithGenerator` — сторож против расхождения с генератором.

### #153 — тип пула больше не хардкодится в разборе
**Changed:** `parseCatalogURL` в `internal/singbox/parse.go` больше не проставляет `Type` — у `Source: pool` он всегда пуст, тип назначает драйвер каталога.

## 2026-07-28

### #128 — ключ из каталога не отключает проверку сертификата

**Changed:** `forceCertVerify` в `pool.go` гасит `insecure` у участников пула через `OutboundTLSOptionsWrapper` — покрывает все протоколы с TLS сразу.
**Beware:** гасится в сборке пула, а не в `parse_url.go`: разбор один на все источники, и для ручной вставки флаг — законное решение пользователя. Обратный перенос закреплён тестом.

### #130 — правило с недоступным туннелем отказывает

**Changed:** пять веток `continue` в `generate.go` заменены на `reject` в обоих наборах правил (ADR 0013); `routeRule` и `dnsRuleFor` получили признак отказа. Эталоны `full.json` и `chain.json` поехали.
**Beware:** отказ пиров сохраняет адреса **выключенных** пиров в `source_ip_cidr` — пустой список означал бы «для всех» и оборвал бы трафик остальных.

### #124 — ссылка с ключом больше не пишется в лог

**Changed:** `pool.go` пишет `хост:порт` вместо полной ссылки; хост берётся ручным разбором.
**Beware:** не `url.Parse` — он спотыкается о не-ASCII в логин-части, а у `ss://` там бывает base64 со всем ключом.

### #117 — аудит утечек: что нашлось в слое

**Changed:** ничего в коде — разбор по коду, `docs/research/security-audit-2026-07.md`.
**Beware:** `route.final = direct-out` делал пропуск правила утечкой, а не отказом — закрыто ADR 0013 и #130, устройство отказа в инвариантах слоя. Осталось: `allowInsecure` из ключа публичного каталога снимает проверку сертификата (#128).

### #107 — снятие устройства WARP у Cloudflare

**Changed:** `warp.go` — `Unregister(ctx, deviceID, accessToken)` и сентинел `ErrWARPUnregisterRejected` отдельно от `ErrWARPRejected`.
**Beware:** два сентинела вместо одного с подстановкой операции: слой api снимает префикс сентинела и собирает из остатка фразу для панели, поэтому операция внутри детали дала бы «отказ в регистрации (регистрация, код …)».

### #99 — цепь «туннель → WARP» через detour

**Changed:** `singbox.go` — `ChainTag`; `tunnel.go` — `chainEndpoint`, клон с `detour`; `generate.go` — сбор и дедупликация пар. Эталон `testdata/chain.json` гоняется настоящим `sing-box check` в CI.
**Beware:** устройство цепи — в инвариантах слоя. На стенде цепь «пул → WARP» дала адрес Cloudflare, тот же пул без цепи — адрес своего сервера.

## 2026-07-27

### #100 — регистрация WARP у Cloudflare

**Changed:** `internal/singbox/warp.go` — устройство регистрируется запросом к `api.cloudflareclient.com`, ответ собирается в обычный `.conf` и идёт через тот же `Parse`; хост в домене `cloudflareclient.com` метится как `source = warp`.
**Beware:** `wgcf` в цепочку демона не тянем — та же причина, что у `wg` и `nft`. Путь сборки endpoint один: кнопка и ручная вставка разойтись не могут.

### #98 — WireGuard Reserved разбирается из `[Peer]`

**Changed:** `buildPeer` в `parse_wireguard.go` читает `Reserved` из `[Peer]` (числа через запятую или base64) в `option.WireGuardPeer.Reserved`; отсутствие поля законно.
**Beware:** на стенде WARP работает и **без** `reserved` — тот же адрес и задержка, а контроль с битым ключом даёт `down`. Поле разбирается ради совместимости, не потому что без него пакеты отбрасываются.


## 2026-07-26

### #62, #66, #68 — пул разворачивается в группу urltest

**Changed:** `internal/singbox/{pool,tunnel,generate,parse}.go` — включённый пул даёт N vless-outbound'ов плюс `urltest` под тегом туннеля (ADR 0010).
**New surface:** `PoolMembers(store.Tunnel)` — тег участника → сервер. `PoolTestInterval` = 3 минуты.
**Beware:** порядок отбора и потолок участников — в инвариантах слоя; пересортировка по пингу была дефектом #68.

### трафик — селективность проверена вживую

**Changed:** на стенде прошла полная цепочка: FakeIP → метка nft → tproxy → sing-box → туннель; прямой трафик уходит masquerade с адресом сервера.
**Beware:** для первого применения нужен `reload-or-restart` — sing-box ещё не запущен. Ошибку окружения нельзя отдавать как `invalid_config`.


## 2026-07-25

### #3 — генератор конфига из состояния БД

**Changed:** `internal/singbox/{generate,dns,tunnel,ruleset}.go` — сборка `option.Options` из снимка состояния; доки 02 и 04 приведены к реализации.
**New surface:** `Generate(store.Snapshot)` и `Marshal(option.Options)`.
**Beware:** `sing-box check` ещё не вызывается; правило с одним plain-списком в конфиг не попадёт — пути для plain-списков доменов нет.

### #4 — парсеры proxy-URL и WireGuard .conf

**Changed:** `internal/singbox/parse*.go` — разбор vless/ss/trojan/hysteria2/socks и INI WireGuard; зависимость `sing-box v1.12.25`.
**New surface:** `Parse(raw)` — единственная точка разбора пользовательского ввода.
**Beware:** `kcp` и `xhttp` из корпуса Podkop не поддерживаются; `ech` игнорируется — там base64, а sing-box ждёт PEM.

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

Whole-Internet peer source policies precede ordinary destination rules after DNS hijack. Same-exit DNS upstream and hostname bootstrap, plus an explicit source resolve action, prevent destination DNS fallback and stale FakeIP leakage. A post-resolve source-scoped private/special CIDR reject precedes the terminal exit. Missing/disabled/empty targets retain source-only route/DNS rejection. Health never authorizes fallback (ADR 0028).

Implementation and verification record: [task 231](../../tasks/done/231-peer-egress.md). The bounded isolated Linux fixture and independent synthetic-browser checks passed; the completed record retains the failed first attempt and exact evidence boundaries. The fixture uses veth identities and a synthetic SOCKS exit, not encrypted incoming WireGuard or a DO client flow.
