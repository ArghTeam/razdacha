# DNS и FakeIP

Селективность держится на DNS. Это самая важная часть системы и одновременно самая
простая — потому что мы контролируем весь путь резолва.

## Почему здесь проще, чем в Podkop

Podkop работает на роутере, где DNS уже занят dnsmasq. Ему приходится перехватывать
конфигурацию: сохранять прежние `server`/`noresolv`/`cachesize` в резервные ключи UCI,
подменять их своими, восстанавливать при остановке, и защищать всё это флагом
`shutdown_correctly` на случай некорректного завершения. Это самая хрупкая часть Podkop
и главный источник поломок «после перезагрузки нет интернета».

У нас перехватывать нечего **на сервере**. В клиентском конфиге стоит `DNS = 10.8.0.1`,
на этом адресе слушает sing-box. Ни `systemd-resolved`, ни dnsmasq, ни `/etc/resolv.conf`
сервера в этом пути не участвуют.

**Следствие:** ~150 строк самой опасной логики Podkop просто не портируются.

Но строка в конфиге — это то, что клиент получил, а не то, чему он обязан следовать.
Резолвер меняется руками, приложением с зашитым адресом, Android Private DNS. Поэтому
путь резолва принуждается на сервере — правилами nft, а не доверием к конфигу
(см. [Перехват DNS](#перехват-dns)).

## Перехват DNS

Клиент, чей резолвер задан мимо нас, резолвит домен в настоящий адрес: FakeIP не
выдаётся, метка nft не ставится, и трафик уходит напрямую — мимо туннеля и мимо
блокировок. Правило по домену для такого клиента просто не существует. Поэтому:

| Что | Порт | Что делает сервер |
|---|---|---|
| plaintext DNS | udp/tcp 53 | DNAT на `10.8.0.1:53`, куда бы клиент ни спрашивал |
| DoT, DoQ | tcp/udp 853 | `reject` с ICMP admin-prohibited |
| DoH | tcp 443 | ничего — перехватить нельзя |

**Почему DNAT, а не метка.** Метка увела бы DNS в таблицу 105 и в tproxy, где его
пришлось бы разбирать sniff-ом и отдельным правилом `hijack-dns` на tproxy-inbound.
DNAT доводит пакет до inbound `dns-in`, для которого перехват уже настроен, и не
трогает маркировку: она как была «адрес назначения совпал со списком», так и осталась,
порт в неё не входит. Ответ разворачивает обратно conntrack, поэтому клиент видит его
от того адреса, на который спрашивал, — перехват прозрачен, и `8.8.8.8` в настройках
клиента продолжает «работать».

Чтобы порядок цепочек не решал исхода, DNS-порты выходят из `mangle` до маркировки:
запрос на резолвер, чей адрес случайно попал в `razdacha_subnets`, иначе уехал бы в
tproxy и не дошёл бы ни до перехвата, ни до отказа. Правила — в
[сети](03-networking.md#nftables).

**Петли нет.** Цепочка перехвата висит на `prerouting` и смотрит только на `iifname
wg0`; собственные запросы sing-box к апстриму идут через `output`. Плюс сравнение
`ip daddr != 10.8.0.1` не даёт переписывать то, что и так адресовано нам.

**Граница.** DoH на 443 неотличим от обычного HTTPS: перехват означал бы подмену TLS,
которой у нас нет. Против него работают только косвенные меры — reject
`use-application-dns.net` и блокировка `query_type: HTTPS`, — и Chrome с явно заданным
DoH-провайдером их игнорирует. Это остаётся документированным ограничением, но
единственным: обычный plaintext-DNS на чужой резолвер больше не проходит.

## Схема

```
клиент → любой адрес:53 → DNAT → 10.8.0.1:53 (sing-box dns-in)
              │
              ├─ домен совпал с rule_set правила  → FakeIP из 198.18.0.0/15
              ├─ HTTPS-запись (тип 65)            → reject
              ├─ use-application-dns.net          → reject (отключает DoH в Firefox)
              └─ иначе                            → апстрим (1.1.1.1), только A
```

## Конфигурация sing-box

```jsonc
"dns": {
  "strategy": "ipv4_only",
  "independent_cache": true,
  "final": "upstream",
  "servers": [
    { "tag": "upstream",  "type": "udp",    "server": "1.1.1.1" },
    { "tag": "bootstrap", "type": "udp",    "server": "1.1.1.1" },
    { "tag": "fakeip",    "type": "fakeip", "inet4_range": "198.18.0.0/15" }
  ],
  "rules": [
    { "query_type": ["HTTPS"], "action": "reject" },
    { "domain_suffix": ["use-application-dns.net"], "action": "reject" },
    { "rule_set": ["rule-<id>-dns", "..."], "server": "fakeip", "rewrite_ttl": 60 }
  ]
}
```

Ключевые моменты:

- **`strategy: ipv4_only`** — слой 1 отключения IPv6
  ([ADR 0005](decisions/0005-ipv6-off-via-dns-strategy.md)). AAAA не отдаются никогда.
- **`inet4_range` без `inet6_range`** — FakeIP только для v4, следствие того же решения.
- **`bootstrap`** нужен, когда `dns_upstream` задан доменом (DoH/DoT): им резолвится сам
  адрес апстрим-сервера. При `dns_type = udp` с IP-адресом не используется, но
  генерируется всегда — дешевле, чем ветвление.
- **`rewrite_ttl: 60`** — короткий TTL на FakeIP-ответах, чтобы изменение правил в UI
  вступало в силу за минуту, а не через сутки кэша клиента.
- **reject на HTTPS-записи (тип 65)** — иначе браузеры получают ECH/HTTP3-подсказки в
  обход FakeIP.

## How a policy becomes DNS and route rules

Manual domains and daemon-managed plain lists have separate projections
([ADR 0024](decisions/0024-domain-only-dns-projections.md)). Full route sets retain
both domains and CIDRs under their existing tags. Their additional `-dns` sets
contain only domain suffixes, taken from the same single plain-list read. Immediate
DNS actions reference these domain-only tags. In sing-box v1.12.25 an IP-only
headless entry can otherwise match an unrelated DNS question during preliminary
matching and cause an unrelated reject or FakeIP response.

A CIDR-only source contributes no DNS tag. If no DNS tags remain, the DNS action
is omitted; the complete route rule and nft CIDR steering remain active. Never emit
an empty-tag DNS action: even a source-peer restriction would reject or rewrite all
questions for that peer. Selected-peer constraints are identical in DNS and route
rules. CIDR-only policies protect destination traffic without blocking unrelated
name resolution.

Block and unavailable-tunnel policies reject matching domains, including when
`resolve_real_ip` is enabled. A live tunnel uses FakeIP unless real resolution is
requested. Direct policies have no DNS action. Real-resolution policies require a
CIDR source for nft steering; domain-only real-resolution policies are rejected at
save time. CIDR presence in a referenced list is still a diagnostic responsibility.

Native community, SRS and JSON sets retain their complete predicates and shared
tags. Their DNS matching and autonomous refresh remain an explicit unresolved
scope in issue #220. This fix does not flatten native logical, inverted or regex
conditions and does not claim all-format closure. The IPv4 strategy, HTTPS-query
reject and application-DNS guard remain unchanged.

**FakeIP переживает перезапуск.** `experimental.cache_file` с `store_fakeip: true` хранит
соответствие адрес ↔ домен в `/var/lib/sing-box/cache.db`. Без него после перезагрузки
sing-box клиент с закэшированным FakeIP уходит в никуда до истечения TTL.

## Почему одного FakeIP недостаточно

FakeIP работает, только если клиент **резолвит домен**. Не работает:

- при обращении по литеральному IP-адресу;
- для списков, которые изначально заданы подсетями (Cloudflare, Telegram, Discord);
- когда приложение использует собственный DoH в обход системного DNS (plaintext-DNS и
  DoT закрыты перехватом, DoH на 443 — нет).

Поэтому подсети из правил дополнительно попадают в nft-сет `razdacha_subnets`, и трафик к
ним маркируется независимо от DNS ([сеть](03-networking.md#nftables)). Два независимых
механизма, покрывающие разные случаи. Podkop устроен так же.

Действие правила при этом не смотрится: метка не решает судьбу пакета, она только заводит
его в sing-box, где судьбу решают `route.rules[]`. Правилу `block` метка нужна не меньше,
чем правилу в туннель, — чтобы отбросить пакет по `reject`, sing-box должен сперва его
увидеть; без метки блокировка по подсети молча не работает. Подсети правила `direct` тоже
заливаются: половина сета приходит из скачанных списков, а там действия правила уже нет,
и отделить в сете `direct` от остального нечем — цена вопроса — крюк через tproxy для
трафика, который всё равно уйдёт напрямую.

Против приложений с собственным DoH помогает reject `use-application-dns.net` (Firefox
уважает этот сигнал) и блокировка `query_type: HTTPS`. Обе меры работают только для тех,
кто и так спрашивает у нас, — принуждает к этому [перехват](#перехват-dns). Chrome с явно
заданным DoH-провайдером обойти нельзя: это документированное ограничение.

## Списки

Источник community-списков — [itdoginfo/allow-domains](https://github.com/itdoginfo/allow-domains),
тот же, что у Podkop.

| Вид | Формат | Как используется |
|---|---|---|
| Домены сервиса | `.srs` из релизов | `rule_set` типа `remote`, sing-box обновляет сам |
| Подсети сервиса | plain `.lst` | скачиваются демоном, заливаются в nft-сет |
| Свои домены | текст из UI | `rule_set` типа `inline`, `domain_suffix` |
| Свои подсети | текст из UI | тот же `inline`-набор + nft-сет |
| Внешний список | URL | `.srs`/`.json` → `remote`; иное → скачиваем сами, содержимое в `inline` |

**Plain-список — не только подсети.** Демон качает его, разбирает на домены и подсети, и
то и другое кладёт в `rule_set` типа `inline` того правила, где список указан: sing-box
такой файл не читает, а выбросить разобранное нельзя — правило, у которого этот список
единственный, осталось бы без единого условия совпадения, выпало бы из конфига и отдало
бы свой трафик `route.final`, то есть напрямую с адреса сервера (issue #125). Подсети
дублируются в nft-сет, как и у любого другого списка.

Набор собирается из того, что лежит в кэше на момент сборки конфига. Списка ещё нет —
его домены в конфиг не попадут, в лог уйдёт предупреждение, а диагностика назовёт правило
и адрес списка. Свежескачанное содержимое доезжает до sing-box следующим применением
конфигурации: применяется она по кнопке, и делать это за пользователя расписание списков
не вправе — вместе со списком уехали бы и не применённые им правки правил.

**Расписание.** `remote`-наборы обновляет сам sing-box по `update_interval`. Plain-списки
обновляет демон по `Settings.list_update_interval`. При старте — отложенное
обновление, после проверки доступности сети (Podkop делает до 10 попыток с растущим
таймаутом; повторить, иначе на медленном VPS первое обновление стабильно проваливается).

**Кэш** — `/var/lib/razdacha/lists/`. При недоступности источника используется прошлая
версия, в UI показывается возраст списков. Пустой список никогда не заменяет непустой.

## Локальный кэш DNS

В первых версиях не делаем: `independent_cache` sing-box достаточно для типовых
1–20 клиентов. Если понадобится блокировка рекламы — ставить AdGuard Home перед
sing-box отдельно, не встраивать. Границы продукта важнее функциональности.
