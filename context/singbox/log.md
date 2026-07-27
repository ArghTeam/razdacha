# Singbox — log

<!-- Dated entries appended by the scribe agent, newest first. -->
<!-- Schema: `## YYYY-MM-DD` then `### <ref> — <title>` with Changed / New surface / Beware. -->

## 2026-07-28

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
