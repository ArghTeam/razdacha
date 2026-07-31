# Панели sing-box и xray: есть ли соседи в нише селективной маршрутизации

Дата: 2026-07-31. Разбор по [issue #147](https://github.com/ArghTeam/razdacha/issues/147).

Не обзор «какие бывают панели», а ответы на конкретные вопросы: **кто ещё решает, какой
трафик уходит в туннель, а какой мимо**, и **что происходит, когда туннель недоступен**.
Повод — [ADR 0013](../decisions/0013-unavailable-tunnel-rejects.md) и
[аудит утечек](security-audit-2026-07.md), пункт 1: у нас `route.final = direct-out`, и до
ADR 0013 правило, выпавшее из конфига, отдавало трафик наружу с адреса сервера при зелёной
диагностике.

## Чем проверялось

Репозитории склонированы и прочитаны локально (`git clone --depth 1`), все ссылки
`file:line` получены грепом по этим копиям. README доказательством не считается: где
утверждение опирается только на документацию или осталось непроверенным, это сказано прямо.

| Проект | Коммит | Что это |
|---|---|---|
| [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui) | `264f61e` (2026-07-30) | панель, xray |
| [XTLS/Xray-core](https://github.com/XTLS/Xray-core) | `5ca6f4b` | ядро |
| [SagerNet/sing-box](https://github.com/SagerNet/sing-box) | `f141553` (2026-07-31) | ядро |
| [Gozargah/Marzban](https://github.com/Gozargah/Marzban) | `7f396db` (2025-01-09) | панель, xray |
| [marzneshin/marzneshin](https://github.com/marzneshin/marzneshin) | `d3b25e2` (2025-10-03) | панель, xray |
| [hiddify/hiddify-core](https://github.com/hiddify/hiddify-core) | `db74dfc` (2026-07-06) | клиентское ядро, sing-box |
| [alireza0/s-ui](https://github.com/alireza0/s-ui) | `647b43f` (2026-07-31) | панель, sing-box |
| [remnawave/backend](https://github.com/remnawave/backend) | `ba51868` (2026-07-13) | панель, xray |
| [Openwrt-Passwall/openwrt-passwall](https://github.com/Openwrt-Passwall/openwrt-passwall) | `207d3ae` | OpenWrt |
| [immortalwrt/homeproxy](https://github.com/immortalwrt/homeproxy) | `4b2617a` (2026-07-25) | OpenWrt, sing-box |
| [Matsuridayo/nekoray](https://github.com/Matsuridayo/nekoray) | `adef6cd` (2024-12-12) | десктоп-клиент |
| [amnezia-vpn/amnezia-client](https://github.com/amnezia-vpn/amnezia-client) | `e38a233` | клиент |
| [itdoginfo/podkop](https://github.com/itdoginfo/podkop) | `a64923d` (2026-07-25) | OpenWrt, наш источник |

Чего посмотреть не удалось:

- `xiaorouji/openwrt-passwall` больше не существует, проект переехал в организацию
  `Openwrt-Passwall`; смотрели новый адрес.
- **Marzneshin** хранит серверный конфиг на ноде (`marznode`), самого `marznode` в разборе
  нет — какой там шаблон, не проверено. Прокси-outbound'ы в подписку дописывает внешняя
  библиотека `v2share`, её кода в репозитории нет — итоговый порядок outbound'ов у клиента
  не проверен.
- **s-ui**: фронтенд подключён сабмодулем `s-ui-frontend` и в клоне отсутствует. Всё, что
  ниже сказано про s-ui, выведено из бэкенда; как выглядит его UI — не проверено.
- **Hiddify-Manager** (серверная часть Hiddify) не разбирался — разбирали только
  `hiddify-core`.
- **Remnawave**: разобран открытый `backend`. Панель-фронтенд закрыта, поведение UI не
  проверено.

## Селективная маршрутизация

Короткий ответ: **соседи есть, и их несколько.** Ниша делится не по линии «панель против
роутера», а по тому, чья это маршрутизация — сервера или клиентского устройства, и есть ли
у оператора модель правила или только текстовое поле с JSON.

### Ядра: что они делают с несовпавшим трафиком

**sing-box.** `route.final` — необязательное поле (`option/route.go:10`). Если ни одно
правило не совпало, берётся `r.outbound.Default()` (`route/route.go:155`, `:283`, `:436`).
Дефолтный outbound — либо тот, чей тег указан в `final`, либо, когда `final` пуст,
свежесозданный `direct` (`adapter/outbound/manager.go:59-75`, фабрика — `box.go:386-393`).
То есть **пустой `final` в sing-box означает прямой выход**, а не отказ.

**Xray.** Дефолтный outbound — просто первый добавленный
(`app/proxyman/outbound/outbound.go:109-111`); диспетчер уходит в него, когда роутер не
подобрал маршрут (`app/dispatcher/default.go:476-479`). Но есть отличие, важное для второго
вопроса: если правило совпало, а его `outboundTag` не существует, Xray **рвёт соединение и
не пускает его в дефолтный outbound** — с комментарием «DO NOT CHANGE»
(`app/dispatcher/default.go:465-471`). Утечку через висячий тег Xray закрывает на уровне
ядра; утечку через *исчезнувшее правило* — нет.

### 3x-ui: сосед, и близкий

Вывод «3x-ui про раздачу доступа», сделанный 2026-07-27, применительно к маршрутизации на
состоянии `264f61e` **не держится**. У него есть полноценный редактор route-правил.

Схема правила (`frontend/src/schemas/routing.ts:19-38`): `domain`, `ip`, `port`,
`sourcePort`, `localPort`, `network`, `sourceIP`, `localIP`, `user`, `inboundTag`,
`protocol`, `attrs`, `process`, назначение — `outboundTag` **или** `balancerTag`, плюс
`ruleTag` и флаг `enabled`. Приоритет — порядок в списке, переставляется стрелками
(`frontend/src/pages/xray/routing/RoutingTab.tsx:231-241`).

Рядом:

- балансировщики со стратегиями `random`/`roundRobin`/`leastPing`/`leastLoad` и полем
  `fallbackTag` (`frontend/src/schemas/routing.ts:41-77`) — их аналог нашего пула;
- управление geo-файлами по белому списку источников, включая русские правила
  (`internal/web/service/server.go:1913-1920`);
- **пробник маршрута**: форма дёргает `RoutingService.TestRoute` в живом ядре и показывает,
  какое правило и какой outbound сработают
  (`frontend/src/pages/xray/routing/RouteTester.tsx:15-16`, `:45`, `:136-150`);
- подписки на outbound'ы с сохранением тегов между обновлениями, чтобы правила и
  балансировщики не разъезжались (`internal/web/service/outbound_subscription.go:595-628`).

Отличие от нас не в наличии функции, а в её форме: правило у 3x-ui — это поля конфига Xray,
названные как в Xray, а списки — `.dat`-файлы с префиксами `geosite:`/`geoip:`. Каталога
списков как отдельной сущности нет.

### Marzban, Marzneshin, Remnawave: раздача доступа

**Marzban.** Своей модели outbound'ов и правил у панели нет: единственный способ тронуть
маршрутизацию — правка сырого JSON всего конфига через `GET`/`PUT /api/core/config`
(`app/routers/core.py:101-132`), в UI это редактор JSON целиком
(`app/dashboard/src/components/CoreSettingsModal.tsx:261`). Дефолтный серверный шаблон
содержит одно правило — блокировать `geoip:private` — и первым outbound'ом ставит
`freedom/DIRECT` (`xray_config.json:5-15`, `:29-32`). Валидация проверяет только непустоту
массивов и наличие тегов (`app/xray/config.py:127-141`); ссылочная целостность правил не
проверяется, хелпер `get_outbound` объявлен и нигде не вызывается
(`app/xray/config.py:350-353`).

**Marzneshin.** Конфиг ядра живёт даже не в панели: `PUT /api/nodes/{id}/{backend}/config`
принимает строку и передаёт её ноде без разбора (`app/routes/node.py:212-232`), UI — голый
Monaco (`dashboard/src/modules/nodes/dialogs/settings/node-config-editor.tsx:26-38`). Слова
`outbound` в питоновском коде панели нет ни разу. Селективность есть только в *клиентском*
шаблоне подписки и зашита статически: иранский трафик и приватные сети — `direct`, реклама
— `block` (`app/templates/xray.json:16-36`), меняется подменой файла через env
(`app/config/env.py:67`), не через панель.

**Remnawave.** Модели правила маршрутизации в схеме нет вовсе
(`prisma/schema.prisma` — 35 моделей, среди них `Users`, `Nodes`, `Hosts`, `ConfigProfiles`,
`SubscriptionTemplate`; ни одной про routing). Маршрутизация фигурирует как непрозрачный
JSON: `ConfigProfiles.config Json` (`prisma/schema.prisma:419-423`), валидатор умеет
подставлять сниппеты внутрь `config.routing.rules` и `config.routing.balancers`
(`src/common/helpers/xray-config/xray-config.validator.ts:322-331`). Модуль
`subscription-response-rules` — про выбор ответа подписки, не про сетевую маршрутизацию
(`src/modules/subscription-response-rules/subscription-response-rules.module.ts:3-5`).

### Роутерные решения: маршрутизация как центральная сущность

**Passwall** — самый развитый редактор правил из увиденных. Правило («shunt rule») — это
отдельная UCI-секция: протокол (`http`/`tls`/`quic`/`bittorrent`), inbound, network, source
(IP/CIDR/`geoip:`), port и два текстовых списка — домены (`shunt_rules.lua:158`) и IP
(`shunt_rules.lua:210`). Домен задаётся подстрокой, `regexp:`, `domain:`, `full:`,
`geosite:` (`shunt_rules.lua:175`), `ext:` и `rule-set:`/`rs:` с `local:`/`remote:`
(`shunt_rules.lua:179`, описание — `:200-204`) — то есть их аналог нашего каталога списков
это rule-set'ы sing-box, задаваемые URL'ом. Каждому правилу отдельно выбирается узел, и в
списке узлов всегда есть `_default`, `_direct` и `_blackhole`
(`include/shunt_options.lua:132-134`). Все пути — от
`luci-app-passwall/luasrc/model/cbi/passwall/client/`.

**homeproxy** — тот же класс на sing-box, с собственной моделью правила. Правило матчит
`ip_version`, `protocol`, `network`, `domain`/`domain_suffix`/`domain_keyword`/`domain_regex`,
`source_ip_cidr`, `ip_cidr`, порты и диапазоны, `process_name`/`process_path`, `user`,
`rule_set` и флаг `invert` (`root/etc/homeproxy/scripts/generate_client.uc:905-937`,
форма — `htdocs/luci-static/resources/view/homeproxy/client.js:646-858`). Действие правила —
`route`/`route-options`/`reject`/`resolve` (`client.js:669-674`), у `route` выбирается
outbound из «Direct» или включённых узлов (`client.js:678-693`). Списки — секции `ruleset`
(remote/local, binary/source, `.srs` по URL, `generate_client.uc:944-957`). Приоритет —
порядок секций, first-match. Пользовательские правила работают только в режиме
`routing_mode = custom`; по умолчанию стоит пресет `bypass_mainland_china`
(`root/etc/config/homeproxy:30`). Есть и до-роутинговый отбор на nftables: per-host,
per-MAC, per-WAN-адрес (`root/etc/config/homeproxy:38-53`).

**Podkop** — наш источник. Правило = секция с `connection_type` (`proxy`/`vpn`, `block`,
`exclusion`) и набором списков: community-списки, пользовательские домены и подсети,
локальные и удалённые списки доменов и подсетей (`podkop/files/usr/bin/podkop:933-991`).
Community-список превращается в удалённый `.srs` rule-set и подшивается и в route-правило,
и в FakeIP DNS-правило (`podkop/files/usr/bin/podkop:994-1008`). Приоритет — порядок секций.

### Клиенты: селективность на устройстве

**nekoray** — профиль маршрутизации из шести списков: `direct_ip`, `direct_domain`,
`proxy_ip`, `proxy_domain`, `block_ip`, `block_domain`, плюс поле сырых правил `custom`
(`main/NekoGui_DataStore.hpp:5-14`). Строки разбираются по префиксам `geoip:`, `geosite:`,
`full:`, `domain:`, `regexp:`, `keyword:` (`db/ConfigBuilder.cpp:474-527`). Порядок правил
фиксированный, не настраивается: dns → block-домены → proxy-домены → direct-домены →
block-IP → proxy-IP → direct-IP (`db/ConfigBuilder.cpp:630-651`); сырые пользовательские
правила ставятся впереди всех сгенерированных (`db/ConfigBuilder.cpp:703-706`).

**hiddify-core** — редактора правил **нет**, хотя API под него описан. Цикл, который
превращал бы пользовательские правила в правила sing-box, целиком закомментирован
(`v2/config/builder.go:688-725`), файл с разбором доменов и IP пользовательских правил не
содержит ни одной активной строки кода (`v2/config/rules.go:10-96`), protobuf-схема правила
существует (`v2/config/route_rule.proto:5-35`), но сгенерированный тип не используется. В
конфиг попадают только тумблеры: bypass LAN, block ads (шесть remote rule-set'ов,
`v2/config/builder.go:782-863`), block QUIC и **пресет региона** — при `Region != "other"`
весь трафик по `domain_suffix: ".<регион>"` и по `geoip-<регион>`/`geosite-<регион>` уходит
direct (`v2/config/builder.go:877-962`).

**s-ui** — модели правила в БД нет (`database/model/` содержит endpoints, inbounds,
outbounds, services). Правила задаются свободным JSON в двух настройках: `config` для
самого сервера (`service/setting.go:20-42`, `:73`) и `subJsonExt` для клиентской подписки
(`sub/jsonService.go:269`, `:294-308`). Порядок = порядок в массиве.

**Amnezia** делает split tunneling **на клиенте**: режим маршрутизации —
`VpnAllSites` / `VpnOnlyForwardSites` / `VpnAllExceptSites`
(`client/core/utils/routeModes.h:13-15`), плюс раздельный split по приложениям
(`client/core/controllers/appSplitTunnelingController.cpp`) и по IP
(`client/core/controllers/ipSplitTunnelingController.cpp`). Серверная часть AmneziaWG
селективности не знает — её задаёт устройство.

## Поведение при недоступном туннеле

Короткий ответ: **гипотеза подтверждается наполовину, и половина эта проходит ровно по
границе нашего продукта.** Фейл-оупен по умолчанию — не всеобщее свойство, а свойство
**шлюзов и роутеров**, которые обязаны пропускать и прямой трафик тоже. Клиенты, которые
заворачивают всё, по умолчанию фейл-клоуз.

### Кто течёт по умолчанию, а кто нет

| Проект | `final` по умолчанию | Дефолт |
|---|---|---|
| sing-box (ядро) | пустой → `direct` (`adapter/outbound/manager.go:59-75`) | наружу |
| Xray (ядро) | первый outbound (`outbound.go:109-111`) | зависит от шаблона |
| 3x-ui | первый outbound = `freedom`/`direct` (`internal/web/service/config.json:31-41`) | наружу |
| Marzban | `freedom/DIRECT` первым (`xray_config.json:29-32`) | наружу |
| Podkop | `direct-out` явно (`podkop/files/usr/bin/podkop:802`) | наружу |
| homeproxy | `direct-out` в поставляемом конфиге (`root/etc/config/homeproxy:57`) | наружу |
| Passwall | узел по умолчанию `_direct` (`include/shunt_options.lua:109`) | наружу |
| nekoray | `def_outbound = "proxy"` (`main/NekoGui_DataStore.hpp:13`, применение — `db/ConfigBuilder.cpp:722`) | в туннель |
| hiddify-core | `Final: OutboundMainDetour` = селектор `select` (`v2/config/builder.go:980-983`, `:42`, `:58`) | в туннель |
| s-ui (подписка) | `"proxy"` — селектор (`sub/jsonService.go:265`) | в туннель, но в селекторе есть `direct` (`:224`) |
| Marzneshin | серверный шаблон у `marznode` — не проверено | — |
| Remnawave | в sing-box-шаблоне `route.final` отсутствует вовсе (`default-templates.ts:259-283`) | не проверено |

Podkop ставит `direct-out` через `sing_box_cm_configure_route`
(`podkop/files/usr/lib/sing_box_config_manager.sh:1055-1076`); `reject` у него появляется
только для секций с `connection_type = block` (`podkop/files/usr/bin/podkop:857-872`).

### Правило, потерявшее назначение: у нас это была утечка, у соседей тоже

**3x-ui** — два места, где правило исчезает из конфига и его трафик забирает дефолтный
outbound (то есть `direct`):

1. правило с `enabled: false` вырезается при генерации рантайм-конфига — `stripDisabledRules`
   (`internal/web/service/xray.go:819-856`);
2. при удалении outbound'а правило, у которого он был единственным назначением, **удаляется
   вместе с ним** (`frontend/src/pages/xray/reference-cleanup.ts:5-15`, поведение закреплено
   тестом `frontend/src/test/routing-reference-cleanup.test.ts:22-31`).

Мотивировка записана у них в комментарии: висячий `outboundTag` «black-holes matched
traffic», а висячий `balancerTag` вообще не даёт роутеру стартовать
(`reference-cleanup.ts:5-15`). То есть авторы **знают** про фейл-клоуз Xray и сознательно
выбирают размен, обратный нашему: пусть трафик течёт напрямую, лишь бы не пропал. Смягчено
это тем, что удаление показывает список пострадавших правил в модалке подтверждения
(`frontend/src/pages/xray/outbounds/OutboundsTab.tsx:225-238`) — исчезновение правила у них
хотя бы не молчаливое.

Точного аналога нашего «выключил туннель в панели» у 3x-ui нет: выключить outbound, не
удаляя, нельзя. Ближайшее — outbound-подписка: когда сервер пропадает из неё, его тег
перестаёт существовать, и, по их же документации в коде, «connections that would have used
the missing member may fail or be routed by the next rule»
(`internal/web/service/outbound_subscription.go:620-623`).

**homeproxy** — то же самое, без предупреждения. Правило с `enabled != 1` молча
выбрасывается из конфига (`root/etc/homeproxy/scripts/generate_client.uc:901-903`), то же
для ruleset (`:945-946`) и для узла (`:738-739`); `reject`-заглушка не подставляется, трафик
падает на `route.final`, который из коробки `direct-out`. Случай «узел выключен, но правило
на него ссылается» даёт висячий тег outbound; чем это кончается — отказом `sing-box check`
при старте (`root/etc/init.d/homeproxy:60-62`) или чем-то другим — не проверено.

**Passwall** — правило, у которого узел выбран как «Close (Not use)», получает `nil` вместо
тега и в конфиг не попадает
(`luci-app-passwall/luasrc/passwall/util_sing-box.lua:1517-1543`, `:1554-1559`), трафик
уходит в дефолтный узел.

**s-ui** — удаление outbound'а снимает его с живого ядра и удаляет запись из БД
(`service/outbounds.go:98-113`), а правила в настройке `config`, ссылающиеся на этот тег,
не трогаются вовсе. То есть висячий тег остаётся; что сделает ядро — не проверено.

**Marzban / Marzneshin** — вопрос не возникает: панель не моделирует ни outbound, ни
правило, конфиг это строка. Ссылочная целостность не проверяется
(`app/xray/config.py:127-141`), 502 от мёртвой ноды означает «конфиг не применён»
(`app/routes/node.py:221-231`), а не изменение маршрутизации.

**Podkop** — правила, пережившего свой туннель, не бывает: секция без включённых списков
пропускается целиком (`podkop/files/usr/bin/podkop:916-919`). Это архитектурная развязка, а
не защита: у них туннель и правило — одна сущность, у нас разные
([ADR 0003](../decisions/0003-tunnel-separate-from-rule.md)).

**nekoray** — по конструкции не течёт: правила ссылаются не на конкретный узел, а на
фиксированные теги `proxy`/`bypass`/`block` (`db/ConfigBuilder.cpp:646-651`), а если сборка
цепочки не удалась, конфиг не генерируется вовсе (`db/ConfigBuilder.cpp:440`).

**hiddify-core** — тоже не течёт, потому что пользовательских правил нет, а все теги в
правилах константы. Зато течёт другое: включённый пресет региона отправляет домены и IP
региона напрямую всегда, независимо от состояния туннеля
(`v2/config/builder.go:892-962`).

### Кто умеет fail-closed

**Passwall — умеет, и это два клика.** У каждого правила и у строки «Default» узлом можно
выбрать `_blackhole` (`include/shunt_options.lua:132-134`). Когда дефолтным выбран
blackhole, генератор **убирает `route.final` и вставляет catch-all `action = reject`**:

```lua
if COMMON.default_outbound_tag == "block" then
    route.final = nil
    table.insert(route.rules, { action = "reject" })
end
```

(`luci-app-passwall/luasrc/passwall/util_sing-box.lua:2095-2100`). Из коробки дефолтный узел
`_direct` (`include/shunt_options.lua:109`), то есть по умолчанию они тоже fail-open.

**homeproxy — умеет.** Дефолтный outbound выбирается из списка «Disable (the service)» /
«Direct» / «Block» / конкретный узел (`client.js:357-374`), и `block-out` даёт
`route.final = block` (`generate_client.uc:941`). В поставляемом конфиге стоит `direct-out`
(`root/etc/config/homeproxy:57`).

**3x-ui — умеет глобально.** «Дефолтным outbound'ом» у них называется первый в массиве, и UI
позволяет назначить им `blocked` (blackhole), создав его при необходимости
(`frontend/src/pages/xray/basics/helpers.ts:59-79`, закреплено тестом
`frontend/src/test/routing-default-outbound.test.ts:28-33`); переключатель — на вкладке
маршрутизации (`frontend/src/pages/xray/routing/RoutingBasic.tsx:46`, `:73`).

**nekoray — умеет.** Выбор финального outbound'а `proxy`/`bypass`/`block` в диалоге
маршрутов (`ui/dialog_manage_routes.ui:582-598`), дефолт — `proxy`.

Названного «kill switch» нет ни у кого: греп по `killswitch`/`kill switch` не даёт
совпадений ни в homeproxy, ни в nekoray, ни в hiddify-core, ни в remnawave/backend. Есть
именно выбор финального outbound'а.

Чего **не нашлось ни у кого** — того, что мы завели в ADR 0013: правила, которое при
недоступном туннеле само превращается в `reject`, не трогая остальной трафик. Везде выбор
бинарный и глобальный: либо весь несовпавший трафик наружу, либо весь в стену. Правило, чьё
назначение исчезло, у всех, кто вообще моделирует правила (3x-ui, homeproxy, Passwall),
просто пропадает из конфига — ровно как у нас до ADR 0013.

## Вывод: гипотеза не подтвердилась

**«Соседей в нише почти нет» — неверно.** По первому вопросу соседей минимум пятеро, и трое
из них ближе, чем предполагалось:

1. **3x-ui** — на состоянии `264f61e` у него полноценный редактор route-правил: домены, IP,
   порты, протоколы, приоритет стрелками, балансировщики с `fallbackTag`, geo-файлы и живой
   пробник маршрута. Раздача доступа у него по-прежнему центральная, но селективная
   маршрутизация — не отсутствующая функция, а вторая полноценная. Это прямой сосед.
2. **Passwall** — самый развитый редактор правил из всех: семь способов задать домен,
   rule-set'ы по URL, per-rule выбор узла, и доведённый до конца fail-closed.
3. **homeproxy** — собственная модель правила со всеми полями sing-box, действие `reject`
   прямо в правиле и выбираемый `block-out` в качестве финального.
4. **Podkop** — известный нам родственник, подтверждён как таковой.
5. **nekoray** — клиентский, но с настоящим профилем маршрутизации.

По второму вопросу гипотеза подтверждается **для шлюзов и роутеров** и опровергается для
клиентов. Podkop, homeproxy, Passwall, 3x-ui и Marzban по умолчанию отдают несовпавший
трафик напрямую — ровно как мы; nekoray, hiddify-core и s-ui по умолчанию заворачивают всё
в туннель. Это не разница в аккуратности, а разница в продукте: у шлюза прямой трафик —
половина смысла, и `final = direct` для него единственный работающий дефолт
([ADR 0001](../decisions/0001-gateway-not-desktop.md)).

Что остаётся нашим и не найдено ни у кого:

- **правило, которое при недоступном туннеле само становится `reject`**, оставляя остальной
  трафик прямым. У всех остальных выбор глобальный: весь трафик наружу или весь в стену;
- **клиент, который ничего не настраивает.** У Amnezia селективность живёт на устройстве, у
  Passwall/Podkop/homeproxy — на роутере, у 3x-ui и s-ui клиент ставит прокси-клиент.
  Штатный WireGuard и правило на сервере — это наше;
- **туннель и правило как независимые сущности**
  ([ADR 0003](../decisions/0003-tunnel-separate-from-rule.md)). У Podkop это одна секция, у
  3x-ui правило удаляется вместе с outbound'ом. Именно из этой развязки и вырос вопрос ADR
  0013, которого у остальных не возникает.

Что стоит забрать:

- **пробник маршрута** (3x-ui, `RouteTester.tsx`): «куда уйдёт вот этот домен и по какому
  правилу» — прямой ответ на «почему не работает», которого у нас нет;
- **показывать последствия перед разрушающим действием** (3x-ui,
  `OutboundsTab.tsx:225-238`): список правил, которые пострадают, в модалке подтверждения;
- **явный выбор финального outbound'а как опция** (Passwall, homeproxy, 3x-ui, nekoray): не
  вместо ADR 0013, а рядом — «весь трафик только через туннели» для тех, кому прямой выход
  не нужен вовсе.

Чего нет **намеренно**: глобального `route.final = reject` по умолчанию — отклонено в
[ADR 0013](../decisions/0013-unavailable-tunnel-rejects.md) и не пересматривается; сырого
редактора JSON-конфига (Marzban, Marzneshin, s-ui) — конфиг генерируется целиком из БД и не
патчится; per-peer настроек маршрутизации — правило одно на всех, клиент не настраивает
ничего.
