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
`file:line` получены грепом по этим копиям. README не считается доказательством: где
утверждение опирается только на документацию, это отмечено словами «по документации».

| Проект | Коммит | Дата коммита |
|---|---|---|
| [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui) | `264f61e` | 2026-07-30 |
| [XTLS/Xray-core](https://github.com/XTLS/Xray-core) | `5ca6f4b` | — |
| [SagerNet/sing-box](https://github.com/SagerNet/sing-box) | `f141553` | 2026-07-31 |
| [Gozargah/Marzban](https://github.com/Gozargah/Marzban) | `7f396db` | 2025-01-09 |
| [marzneshin/marzneshin](https://github.com/marzneshin/marzneshin) | `d3b25e2` | 2025-10-03 |
| [hiddify/hiddify-core](https://github.com/hiddify/hiddify-core) | `db74dfc` | 2026-07-06 |
| [hiddify/Hiddify-Manager](https://github.com/hiddify/Hiddify-Manager) | `7b1475f` | 2026-05-29 |
| [alireza0/s-ui](https://github.com/alireza0/s-ui) | `647b43f` | 2026-07-31 |
| [remnawave/backend](https://github.com/remnawave/backend) | `ba51868` | 2026-07-13 |
| [Openwrt-Passwall/openwrt-passwall](https://github.com/Openwrt-Passwall/openwrt-passwall) | `207d3ae` | — |
| [immortalwrt/homeproxy](https://github.com/immortalwrt/homeproxy) | `4b2617a` | 2026-07-25 |
| [Matsuridayo/nekoray](https://github.com/Matsuridayo/nekoray) | `adef6cd` | 2024-12-12 |
| [amnezia-vpn/amnezia-client](https://github.com/amnezia-vpn/amnezia-client) | `e38a233` | — |
| [itdoginfo/podkop](https://github.com/itdoginfo/podkop) | `a64923d` | 2026-07-25 |

Кого посмотреть не удалось: `xiaorouji/openwrt-passwall` переехал в организацию
`Openwrt-Passwall` (старые ссылки в интернете мертвы), смотрели новый адрес. Marzneshin
хранит серверный конфиг на ноде (`marznode`), самого `marznode` в разборе нет — что там за
шаблон, не проверено. У Marzneshin прокси-outbound'ы в подписку дописывает внешняя
библиотека `v2share`, её кода в репозитории нет — итоговый порядок outbound'ов не проверен.

## Селективная маршрутизация

Короткий ответ: **соседи есть, и их больше одного.** Ниша делится надвое, и линия проходит
не по «панель против роутера», а по тому, чья это маршрутизация — сервера или клиента.

### Ядра: что они делают с несовпавшим трафиком

Оба ядра по умолчанию **выпускают напрямую**, и это не наша особенность, а их.

**sing-box.** `route.final` — необязательное поле (`option/route.go:10`). Если ни одно
правило не совпало, берётся `r.outbound.Default()` (`route/route.go:155`, `:283`, `:436`).
Дефолтный outbound — это либо тот, чей тег указан в `final`, либо, когда `final` пуст,
свежесозданный `direct` (`adapter/outbound/manager.go:67-75`, фабрика —
`box.go:386-393`). То есть **пустой `final` в sing-box означает прямой выход**, а не отказ.

**Xray.** Дефолтный outbound — просто первый добавленный
(`app/proxyman/outbound/outbound.go:109-111`), и диспетчер уходит в него, когда роутер не
подобрал маршрут (`app/dispatcher/default.go:476-479`). Но есть важное отличие: если
правило совпало, а его `outboundTag` не существует, Xray **рвёт соединение и не пускает
его в дефолтный outbound** — с комментарием «DO NOT CHANGE»
(`app/dispatcher/default.go:465-471`). Дырка от висячего тега в Xray закрыта на уровне
ядра; дырка от *исчезнувшего правила* — нет.

### Панели на xray: раздача доступа

**Marzban** — раздача доступа. Своей модели outbound'ов и правил у панели нет: единственный
способ тронуть маршрутизацию — правка сырого JSON всего конфига через `GET/PUT
/api/core/config` (`app/routers/core.py:101-132`), в UI это редактор JSON целиком
(`app/dashboard/src/components/CoreSettingsModal.tsx:261`). Дефолтный серверный шаблон
содержит одно правило — блокировать `geoip:private` — и первым outbound'ом ставит
`freedom/DIRECT` (`xray_config.json:5-15`, `:29-32`). Валидация конфига проверяет только
непустоту массивов и наличие тегов (`app/xray/config.py:127-141`); ссылочная целостность
правил не проверяется вовсе, хелпер `get_outbound` объявлен и нигде не вызывается
(`app/xray/config.py:350-353`).

**Marzneshin** — раздача доступа, причём конфиг ядра живёт даже не в панели. `PUT
/api/nodes/{id}/{backend}/config` принимает строку и передаёт её ноде без разбора
(`app/routes/node.py:212-232`), UI — голый Monaco
(`dashboard/src/modules/nodes/dialogs/settings/node-config-editor.tsx:26-38`). Слова
`outbound` в питоновском коде панели нет ни разу. Селективность есть только в *клиентском*
шаблоне подписки и зашита статически: иранский трафик и приватные сети — `direct`, реклама
— `block` (`app/templates/xray.json:16-36`), меняется подменой файла через env
(`app/config/env.py:67`), не через панель.

**Remnawave** — по разбору ядро панели про подписки и пользователей; отдельного понятия
правила маршрутизации в модели нет (см. таблицу ниже; часть выводов помечена как
непроверенная).

**3x-ui — а вот это сосед, и близкий.** На состоянии `264f61e` у него есть полноценный
редактор route-правил: схема правила (`frontend/src/schemas/routing.ts:19-38`) — это
`domain`, `ip`, `port`, `sourcePort`, `network`, `sourceIP`, `user`, `inboundTag`,
`protocol`, `attrs`, `process`, плюс назначение через `outboundTag` **или** `balancerTag`,
плюс `ruleTag` и флаг `enabled`. Приоритет — порядок в списке, переставляется стрелками
(`frontend/src/pages/xray/routing/RoutingTab.tsx:231-241`). Есть балансировщики со
стратегиями `random`/`roundRobin`/`leastPing`/`leastLoad` и полем `fallbackTag`
(`frontend/src/schemas/routing.ts:41-77`) — то есть их аналог нашего пула. Есть управление
geo-файлами с белым списком источников, включая русские правила
(`internal/web/service/server.go:1913-1920`), и есть «пробник маршрута»: форма, которая
дёргает `RoutingService.TestRoute` в живом ядре и показывает, какое правило и какой
outbound сработают (`frontend/src/pages/xray/routing/RouteTester.tsx:15-19`, `:44`).

Отличие от нас остаётся, но оно не в наличии функции, а в её форме: правило у 3x-ui — это
поля конфига Xray, названные как в Xray, а списки — это `.dat`-файлы с префиксами
`geosite:`/`geoip:`. Каталога списков как сущности нет.

### Роутерные решения: маршрутизация как центральная сущность

**Passwall** — самый развитый редактор правил из увиденных. Правило («shunt rule») — это
отдельная UCI-секция с полями: протокол (`http`/`tls`/`quic`/`bittorrent`), inbound,
network, source (IP/CIDR/`geoip:`), port и два текстовых списка — домены и IP
(`luci-app-passwall/luasrc/model/cbi/passwall/client/shunt_rules.lua:62-155`). Домен можно
задать подстрокой, `regexp:`, `domain:`, `full:`, `geosite:`, `ext:` и `rule-set:`/`rs:` с
`local:`/`remote:` — то есть у них есть и наш «каталог списков», в виде rule-set'ов
(`shunt_rules.lua:167-180`). Каждому правилу отдельно выбирается узел, и в списке узлов
всегда есть `_default`, `_direct` и `_blackhole`
(`luci-app-passwall/luasrc/model/cbi/passwall/client/include/shunt_options.lua:132-134`).

**Podkop** — наш источник. Правило = секция с `connection_type` (`proxy`/`vpn`, `block`,
`exclusion`) и набором списков: community-списки, пользовательские домены и подсети,
локальные и удалённые списки доменов и подсетей
(`podkop/files/usr/bin/podkop:933-991`). Community-список превращается в удалённый
`.srs` rule-set и подшивается и в route-правило, и в FakeIP DNS-правило
(`podkop/files/usr/bin/podkop:994-1008`). Приоритет — порядок секций.

**homeproxy** и **nekoray** — см. таблицу; homeproxy это тот же класс решения на sing-box,
nekoray — десктопный клиент с профилем маршрутизации.

**Hiddify** и **s-ui** — sing-box-панели; их устройство разобрано ниже в таблице.

### Клиенты: селективность на устройстве

**Amnezia** делает split tunneling **на клиенте**, а не на сервере: режим маршрутизации —
`VpnAllSites` / `VpnOnlyForwardSites` / `VpnAllExceptSites`
(`client/core/utils/routeModes.h:13-15`), плюс раздельный split по приложениям
(`client/core/controllers/appSplitTunnelingController.cpp`) и по IP
(`client/core/controllers/ipSplitTunnelingController.cpp`). Серверная часть AmneziaWG
селективности не знает — её задаёт устройство.

Это ровно та граница, по которой мы от них отличаемся: у нас клиент подключается штатным
WireGuard и не настраивает ничего, а правило живёт на сервере и одно на всех.

## Поведение при недоступном туннеле

Короткий ответ: **гипотеза «у большинства та же дыра» подтверждается — но не целиком.**
Дефолт «мимо туннеля напрямую» действительно почти всеобщий. Однако как минимум у двух
проектов есть явный переключатель в «блокировать», а Xray закрывает один из двух наших
путей утечки прямо в ядре.

### Дефолт: прямой выход

- **sing-box** без `route.final` уходит в свежесозданный `direct`
  (`adapter/outbound/manager.go:67-75`, `box.go:386-393`). Fail-open — дефолт самого ядра.
- **Xray**: дефолтный outbound — первый в списке (`app/proxyman/outbound/outbound.go:109-111`).
  В шаблоне 3x-ui первый — `freedom` с тегом `direct` (`internal/web/service/config.json:31-41`),
  в шаблоне Marzban — `freedom/DIRECT` (`xray_config.json:29-32`).
- **Podkop** ставит `route.final = direct-out` явно (`podkop/files/usr/bin/podkop:802`,
  через `sing_box_cm_configure_route`, `podkop/files/usr/lib/sing_box_config_manager.sh:1055-1076`).
  Отдельного «нет туннеля — нет связи» у него нет; `reject` появляется только для секций с
  `connection_type = block` (`podkop/files/usr/bin/podkop:857-872`).

### Правило, потерявшее назначение: то же самое, что было у нас

**3x-ui.** Два места, где правило исчезает из конфига и его трафик забирает дефолтный
outbound:

1. Правило с `enabled: false` вырезается при генерации рантайм-конфига —
   `stripDisabledRules` (`internal/web/service/xray.go:819-856`). Это осознанное действие
   пользователя, но результат — не отказ, а прямой выход.
2. При удалении outbound'а правило, у которого он был единственным назначением,
   **удаляется вместе с ним** (`frontend/src/pages/xray/reference-cleanup.ts:5-15`,
   поведение зафиксировано тестом `frontend/src/test/routing-reference-cleanup.test.ts:22-31`).

Мотивировка у них записана в комментарии: висячий `outboundTag` «black-holes matched
traffic», а висячий `balancerTag` вообще не даёт роутеру стартовать
(`reference-cleanup.ts:5-15`). То есть авторы **знают** про фейл-клоуз Xray и сознательно
выбирают противоположный нашему размен: пусть трафик течёт напрямую, лишь бы не пропал.
Смягчено это тем, что удаление показывает список пострадавших правил в модалке
подтверждения (`frontend/src/pages/xray/outbounds/OutboundsTab.tsx:225-238`) — то есть
исчезновение правила у них хотя бы не молчаливое.

Отдельного «выключить outbound, не удаляя» у 3x-ui нет — то есть точного аналога нашего
сценария «выключил туннель в панели» у него не возникает. Ближайший аналог —
outbound-подписка: когда сервер пропадает из подписки, его тег просто перестаёт
существовать, и, по их же документации в коде, «connections that would have used the
missing member may fail or be routed by the next rule»
(`internal/web/service/outbound_subscription.go:620-623`).

**Marzban / Marzneshin.** Вопрос не возникает, потому что панель не моделирует ни outbound,
ни правило: конфиг — это строка, которую админ правит целиком. Ссылочная целостность не
проверяется (`app/xray/config.py:127-141`), 502 от мёртвой ноды означает «конфиг не
применён» (`app/routes/node.py:221-231`), а не изменение маршрутизации.

**Podkop.** Правила, пережившего свой туннель, не бывает: секция уходит целиком со своими
списками (`podkop/files/usr/bin/podkop:915-918` — секция без включённых списков
пропускается). Это архитектурная развязка, а не защита: у них туннель и правило — одна
сущность, у нас разные ([ADR 0003](../decisions/0003-tunnel-separate-from-rule.md)).

### Кто умеет fail-closed

**Passwall — умеет, и это настраивается.** У каждого правила и у строки «Default» узлом
можно выбрать `_blackhole`
(`luci-app-passwall/luasrc/model/cbi/passwall/client/include/shunt_options.lua:132-134`).
Когда дефолтным узлом выбран blackhole, генератор **убирает `route.final` и вставляет
catch-all `action = reject`**:

```lua
if COMMON.default_outbound_tag == "block" then
    route.final = nil
    table.insert(route.rules, { action = "reject" })
end
```

(`luci-app-passwall/luasrc/passwall/util_sing-box.lua:2095-2100`). По умолчанию дефолтный
узел — `_direct` (`shunt_options.lua:105-113`), то есть из коробки они тоже fail-open, но
глобальный kill switch есть и переключается в два клика. Правило, у которого узел выбран
как «Close (Not use)», получает `nil` вместо тега и в конфиг не попадает
(`util_sing-box.lua:1517-1543`, `:1554-1559`) — то есть исчезновение правила у них тоже
означает прямой выход, но выход этот определяется дефолтным узлом, который может быть
`_blackhole`.

**3x-ui — умеет глобально.** «Дефолтным outbound'ом» у них называется первый в массиве, и
UI позволяет назначить им `blocked` (blackhole), создав его при необходимости
(`frontend/src/pages/xray/basics/helpers.ts:59-79`, поведение закреплено тестом
`frontend/src/test/routing-default-outbound.test.ts:28-33`); переключатель живёт на вкладке
маршрутизации (`frontend/src/pages/xray/routing/RoutingBasic.tsx:46`, `:73`). Это ровно та
«альтернатива с `route.final = reject`», которую мы отклонили в
[ADR 0013](../decisions/0013-unavailable-tunnel-rejects.md) — и отклонили правильно для
своего продукта: у нас прямой трафик обязан ходить, у 3x-ui сервер и так выходная нода.

Чего **ни у кого** не нашлось — того, что мы завели в ADR 0013: правила, которое при
недоступном туннеле само превращается в `reject`, не трогая остальной трафик. Везде выбор
бинарный и глобальный: либо весь несовпавший трафик наружу, либо весь в стену.

### Сводка по вопросу 2

| Проект | `final` по умолчанию | Правило без живого назначения | Явный fail-closed |
|---|---|---|---|
| sing-box (ядро) | `direct` (`adapter/outbound/manager.go:67-75`) | — | только руками через `final` |
| Xray (ядро) | первый outbound (`outbound.go:109-111`) | висячий тег → соединение рвётся (`dispatcher/default.go:465-471`) | — |
| 3x-ui | `direct` (`config.json:31-41`) | вырезается: `enabled:false` (`xray.go:819-856`), удаление outbound'а (`reference-cleanup.ts:5-15`) | да, глобально: дефолтный outbound → `blocked` (`helpers.ts:59-79`) |
| Marzban | `freedom/DIRECT` (`xray_config.json:29-32`) | не моделируется; ссылки не валидируются (`app/xray/config.py:127-141`) | нет |
| Marzneshin | не проверено (конфиг у `marznode`) | не моделируется (`app/routes/node.py:212-232`) | нет |
| Passwall | узел `_direct` (`shunt_options.lua:105-113`) | узел «Close» → правило не в конфиге (`util_sing-box.lua:1517-1543`) | да: `_blackhole` → `final = nil` + `reject` (`util_sing-box.lua:2095-2100`) |
| Podkop | `direct-out` (`bin/podkop:802`) | не бывает: секция уходит целиком (`bin/podkop:915-918`) | нет; `reject` только для `connection_type = block` (`bin/podkop:857-872`) |
| Amnezia (клиент) | режимы `AllSites`/`ForwardSites`/`ExceptSites` (`routeModes.h:13-15`) | не проверено | не проверено |

## Вывод: гипотеза не подтвердилась

**«Соседей в нише почти нет» — неверно.** Соседи есть, и как минимум трое из них ближе,
чем предполагалось:

1. **3x-ui** — на состоянии `264f61e` у него есть полный редактор route-правил с доменами,
   IP, портами, протоколами, приоритетом стрелками, балансировщиками с `fallbackTag`,
   управлением geo-файлами и живым пробником маршрута. Вывод «3x-ui про раздачу доступа»,
   сделанный 2026-07-27, применительно к маршрутизации больше не держится. Раздача доступа
   у него по-прежнему центральная, но селективная маршрутизация — не отсутствующая функция,
   а вторая полноценная.
2. **Passwall** — самый развитый редактор правил из всех: домены семью способами задания,
   rule-set'ы, per-rule выбор узла, и единственный найденный **глобальный kill switch,
   доведённый до `route.final = nil` + `reject`**.
3. **Podkop** — известный нам родственник, подтверждён как таковой.

Что остаётся нашим и **не найдено ни у кого**:

- **правило, которое при недоступном туннеле само становится `reject`**, оставляя остальной
  трафик прямым. Везде выбор глобальный: весь трафик наружу или весь в стену;
- **клиент, который ничего не настраивает.** У Amnezia селективность живёт на устройстве, у
  Passwall/Podkop/homeproxy — на роутере, у 3x-ui клиент ставит прокси-клиент. Штатный
  WireGuard и правило на сервере — это наше;
- **туннель и правило как независимые сущности** ([ADR 0003](../decisions/0003-tunnel-separate-from-rule.md)).
  У Podkop это одна секция, у 3x-ui правило удаляется вместе с outbound'ом. Именно из этой
  развязки и вырос вопрос ADR 0013, которого у остальных не возникает.

Что стоит забрать:

- **пробник маршрута** (3x-ui, `RouteTester.tsx`): «куда уйдёт вот этот домен и по какому
  правилу» — прямой ответ на «почему не работает», которого у нас нет;
- **показывать последствия перед разрушающим действием** (3x-ui, `OutboundsTab.tsx:225-238`):
  список правил, которые пострадают от удаления туннеля, в модалке подтверждения;
- **явный kill switch как опция** (Passwall): не вместо ADR 0013, а рядом — «весь трафик
  только через туннели» для тех, кому прямой выход не нужен вовсе.

Чего нет **намеренно**: глобального `route.final = reject` по умолчанию — отклонено в
[ADR 0013](../decisions/0013-unavailable-tunnel-rejects.md) и не пересматривается; сырого
редактора JSON-конфига (Marzban, Marzneshin) — конфиг генерируется целиком из БД и не
патчится; per-peer настроек маршрутизации — правило одно на всех, клиент не настраивает
ничего.
