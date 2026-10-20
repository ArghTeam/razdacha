---
type: invariants
title: Singbox
description: генерация option.Options из состояния БД, парсеры proxy-URL, check и reload
---

## Invariants
- WireGuard из `.conf` разбирается в `option.Endpoint{Type: "wireguard"}` с `System: false` и пустым `Name`; `bind_interface` не используется (ADR 0002).
- Структуры sing-box сериализуются через `MarshalJSONContext`: обычный `json.Marshal` теряет содержимое `Options`.
- Невалидный ввод возвращает ошибку на русском, пригодную для показа как есть; паника недопустима даже на обрезанном URL и битом base64.
- Неизвестный `security=` в proxy-URL — отказ, а не продолжение с выключенным TLS (Podkop здесь мягче: пишет в лог и идёт дальше).
- Block and unavailable-tunnel policies retain full destination route rejection (ADR 0013). For manual and daemon-managed plain sources, immediate DNS reject/FakeIP actions reference separate domain-only `-dns` sets from the same source read (ADR 0024). CIDR-only sources contribute no DNS tag; an empty DNS projection omits the DNS action, never the CIDR route protection. Reject takes precedence over `resolve_real_ip`; source-peer constraints remain identical. Supported native sources use full immutable local original artifacts and separate exact domain-only DNS artifacts from the accepted AST (ADR 0025). No autonomous remote rule sets are generated; unknown or unsupported predicates reject the candidate.
- Rules without destinations or without any existing selected source are skipped. Source emptiness is checked before tunnel/chain lookup and source construction; empty source matchers must never create a global route or reject. Existing disabled clients retain their address-constrained reject (ADR 0026).
- Отказ правилу, у которого выключены все выбранные пиры, сохраняет адреса этих пиров в `source_ip_cidr`: пустой список означал бы «для всех» и оборвал бы трафик остальных.
- Свои домены и подсети правила идут в набор типа `inline`, а не `local`: конфиг остаётся единственным артефактом, который генератор пишет на диск. Domain and destination CIDR items share an OR group in the pinned runtime; separate manual/plain entries remain equivalent.
- Внешний список правила не в формате `.srs`/`.json` — тоже `inline`-набор, собранный из того, что скачал и разобрал слой lists (`PlainLists`, замыкание от демона; сам слой singbox про кэш списков не знает). Выбросить его содержимое нельзя: `sing-box` такой файл не читает, а правило, у которого этот список единственный, осталось бы без условий и отдало бы трафик `route.final` — прямому выходу (issue #125). Тег `rule-<id>-plain-<i>` отличается от `rule-<id>-remote-<i>`: у одного правила бывают оба вида списков, а номер в `RemoteLists` общий.
- Ссылка `http(s)://` — это каталог ключей, то есть туннель-пул: `Parse` узнаёт её раньше
  proxy-схем и отдаёт `Source = pool` с пустыми `Outbound` и `Endpoint`. **Тип разбор не
  назначает** — его сообщает драйвер каталога (ADR 0015), и проставляет его слой api через
  `lists.PoolKeyType`. Прошитый здесь `Type = vless` пережил свой источник: у outlinekeys
  раздел outline отдаёт shadowsocks. Сети разбор не касается; содержимое каталога проверяет
  первый обход. Без этой ветки пул нельзя было создать вовсе при готовых store, генераторе
  и панели (issue #66).
- Серверы пула отбираются в конфиг по порядку в БД — первые `poolMaxServers` штук, без
  пересортировки по пингу: порядок это приоритет, который ведёт `lists.MergePool`, а
  позиция в нём равна тегу участника. Пересортировка по свежему пингу меняла бы состав
  конфига от шума измерений чужого сайта и перезапускала sing-box на каждом обходе
  каталога (ADR 0010, issue #68). Окно конфига по обе стороны границы слоёв одно и то же:
  `poolMaxServers == lists.PoolConfigServers`, сверяется тестом.
- Ссылка на сервер целиком в лог не пишется: в ней UUID или пароль, а журнал демона читают шире, чем БД. В `log.Warn` о пропущенном сервере пула идут имя туннеля, подпись карточки и `хост:порт` из `serverAddr` — а хост берётся только когда выглядит адресом: у `ss://` на его месте бывает base64 со всем содержимым ключа (issue #124).
- Участник пула, отвергнутый `sing-box check`, выкидывается на применении, а не роняет весь конфиг (ADR 0019). `apply.go` в цикле: генерация целиком → `check` целиком; провал на участнике пула → его убирают из состояния (`withoutPoolServer`), конфиг пересобирают, окно доберётся годным из остатка до `poolMaxServers`. Виноватого рантайм называет номером `initialize outbound[N]`/`endpoint[N]` без тега; номер → тег по `opts.Outbounds[N].Tag`, тег → сервер по той же схеме `poolMemberTag`+`selectPoolServers`, что у генератора. Терпимость строго к членам пула (`source == pool`, всегда outbound): ручной туннель, второе звено цепи, любой не-pool outbound и `endpoint[N]` под неё не попадают — их отказ по-прежнему валит применение целиком, прежний конфиг остаётся. Это про негодность ключа, а не про доступность туннеля (ADR 0013 — другое). Повтор ограничен числом серверов пула; выкинутый уходит в лог адресом `хост:порт`, без ссылки с ключом (#124).
- Сервер пула попадает в конфиг с проверкой сертификата всегда: `allowInsecure`/`insecure` из ссылки каталога гасится при сборке группы (`forceCertVerify` в `pool.go`), а не в разборе. Разбор одинаков для всех источников — у ручной вставки флаг остаётся, это решение того, кто ссылку вставил; ключ пула не вставлял никто, его демон взял с чужой страницы (ADR 0010), и согласие на перехват в туннеле давать некому (issue #128). Снятый флаг уходит в лог одной строкой на пул: каталог раздаёт `allowInsecure=1` пачками.
- Из `Tunnel.Parsed` выбрасываются `type` и `tag`: их проставляет генератор, иначе чужой ключ перебил бы наш тег при слиянии объектов.
- Цепь правила материализуется **клоном** эндпоинта второго звена с `detour` на тег первого, под тегом `chain-<второе>-via-<первое>`; исходный туннель второго звена остаётся без `detour`. Одинаковые пары из разных правил дают один клон. `detour` принадлежит outbound, а не действию `route`, поэтому цепь не выражается route-правилом (ADR 0012).

- `Applier.Prepare` generates/checks a complete candidate without publishing it; `Prepared.Commit` renames and syncs the config directory before invoking the reloader. Pool-member validation retries remain bounded and preserve ADR 0019. The daemon wraps publication in the guarded generation protocol (ADR 0023).
- Production publication unconditionally runs `systemctl restart`, never asynchronous `reload-or-restart`: the old runtime must stop before nft is updated. A failed or uncertain publication is rechecked and restarted even when candidate bytes match disk. `Type=simple` start acknowledgment is not application readiness; intercepted traffic remains fail-closed while the new runtime starts.

## Key entry points
- `internal/singbox/parse.go` — `Parse(raw)`: определение формы ввода (URL / INI / JSON) и единая точка разбора.
- `internal/singbox/generate.go` — `Generate(store.Snapshot)`: сборка конфига целиком, частичного патча нет; `Marshal` — JSON для `/etc/sing-box/config.json`.
- `internal/singbox/testdata/proxy-urls.txt` — корпус ссылок из `podkop/String-example.md`; фикстуры живут в репозитории, а не в чужом каталоге.

- Native local paths are content-addressed and owner-only; runtime file watchers make replacement/removal unsafe. Generation v2 pins original format/hash, DNS hash and projection version, verifies all blobs and rederives nft/DNS before prepare and before durable publication. Explicit no-op Apply also verifies active artifacts. Legacy v1 native policy cannot recover autonomously; administrative access remains guarded until supported sources are acquired and explicit Apply succeeds.

- Relative daemon `-db` arguments resolve once before opening storage; native verification also canonicalizes its generation-relative artifact root with propagated path errors. Apply, no-op active audits and fresh committed recovery therefore verify the same absolute native store.

## Proposed restore contract (not implemented)

[ADR 0027](../../docs/decisions/0027-safe-cross-host-restore.md) and
[task 217](../../tasks/217-safe-cross-host-restore.md) propose the following behavior;
current runtime invariants above remain the implementation record until validated.

Independent runtime remains boot-inhibited after restore; no quarantined config replay. Only checked explicit guarded Apply may release that inhibitor. Paused first/second hops retain scoped rejection.

Proposed publication clarification: runtime marker removal occurs only inside a durable
restore-only publishing phase after checked config sync and ingress guarding. Finish
requires coherent runtime/nft/authority/bootstrap publication; Abort recreates inhibition
before restoring retryable state. Startup/reset/installer/recovery fail closed on an
interrupted publishing phase. See ADR 0027; this is not validated runtime behavior yet.
