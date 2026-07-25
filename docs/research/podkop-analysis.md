# Разбор Podkop

Источник архитектуры razdacha. Все ссылки — на репозиторий
[itdoginfo/podkop](https://github.com/itdoginfo/podkop), коммит `a64923d`
(состояние на 2026-07-25).

Документ описывает, **что** в Podkop устроено определённым образом и **почему** мы это
повторяем или не повторяем. Решения зафиксированы в [ADR](../decisions/).

## Состав проекта

```
podkop/            ядро: ash-скрипты, procd-сервис, UCI-конфиг    ~5300 строк
luci-app-podkop/   LuCI-страница: CBI-формы, ACL, переводы
fe-app-podkop/     TypeScript-исходники UI → компилируются в luci-app/main.js
install.sh, sdk/, Dockerfile-{apk,ipk}, .github/
```

### Ядро `podkop/`

| Файл | Строк | Назначение |
|---|---|---|
| `files/usr/bin/podkop` | 2770 | CLI и вся бизнес-логика |
| `files/usr/lib/sing_box_config_manager.sh` | 1508 | конструктор JSON через `jq`, ~40 функций `sing_box_cm_*` |
| `files/usr/lib/helpers.sh` | 355 | валидаторы, URL-парсер, миграции, загрузка |
| `files/usr/lib/sing_box_config_facade.sh` | 335 | разбор proxy-URL → вызовы менеджера |
| `files/usr/lib/rulesets.sh` | 180 | работа с rule-set sing-box |
| `files/usr/lib/nft.sh` | 69 | обёртки над `nft` |
| `files/usr/lib/constants.sh` | 66 | теги, URL, список сервисов |
| `files/etc/init.d/podkop` | 52 | procd-сервис |
| `files/etc/config/podkop` | 39 | UCI-конфиг по умолчанию |

Зависимости пакета (`podkop/Makefile:18`): `sing-box`, `curl`, `jq`, `kmod-nft-tproxy`,
`coreutils-base64`, `bind-dig`.

### Фронтенд

`fe-app-podkop` — TypeScript без единой рантайм-зависимости, собирается **tsup**
напрямую в `../luci-app-podkop/htdocs/luci-static/resources/view/podkop/main.js`
(4942 строки). Post-build хук (`tsup.config.ts:19-34`) переписывает `export {...}` в
`return baseclass.extend({...})`, превращая ES-модуль в LuCI-модуль.

Транспорт к бэкенду — `fs.exec('/usr/bin/podkop', [method])` с разбором JSON из stdout,
плюс `uci.load('podkop')`. Своего rpcd-плагина нет; ACL
(`luci-app-podkop/root/usr/share/rpcd/acl.d/luci-app-podkop.json`) разрешает exec ровно
двух путей и чтение/запись UCI-пакета.

Внутри — самописный мини-фреймворк: `store.service.ts` (стейт со сравнением через
стабильный JSON), `tab.service.ts` (MutationObserver над классами CBI-вкладок),
`podkopLogWatcher.service.ts` (опрос логов раз в 3 с), 17 SVG-иконок,
`renderButton`/`renderModal`.

## Жизненный цикл

`start` (`bin/podkop:179` → `start_main:110`):

```
check_requirements (:36)      версии sing-box ≥1.12.0, jq ≥1.7.1, base64 ≥9.7
  → синхронизация времени ntpd по 5 хостам (:122)
  → route_table_rule_mark (:253)   таблица 105, ip rule fwmark 0x00100000
  → create_nft_rules (:282)        таблица inet PodkopTable, tproxy 127.0.0.1:1602
  → sing_box_init_config (:581)    генерация config.json
  → dnsmasq_configure (:350)       подмена DNS
  → /etc/init.d/sing-box start
  → list_update & (:482)           фоновое обновление списков
```

`stop` (`:191`) выполняет обратное; `reload` (`:204`) — `stop_main` + `start_main` без
касания dnsmasq.

## Что переносится

### Механика перехвата — переносится целиком

`create_nft_rules` (`bin/podkop:282`): таблица `inet PodkopTable`, сеты `localv4`
(приватные диапазоны), `podkop_subnets` (`ipv4_addr` + `interval` + `auto-merge`),
`interfaces`. Цепочки `mangle` (prerouting, priority −150), `mangle_output` (output,
−150), `proxy` (prerouting, −100). Трафик с `@interfaces` на `@podkop_subnets` или на
`198.18.0.0/15` маркируется `0x00100000`, затем `tproxy ip to 127.0.0.1:1602`.

`route_table_rule_mark` (`:253`): `105 podkop` в `rt_tables`,
`ip route add local 0.0.0.0/0 dev lo table podkop`,
`ip -4 rule add fwmark 0x00100000/0x00100000 table podkop priority 105`.

**У нас:** идентично, `br-lan` → `wg0`. Цепочка `mangle_output` не нужна — трафик самого
сервера не обрабатываем ([ADR 0001](../decisions/0001-gateway-not-desktop.md)).
См. [сеть](../03-networking.md#nftables).

### FakeIP и DNS-механика — переносится, обвязка отбрасывается

`sing_box_cm_configure_dns` (`manager:62`): `strategy=ipv4_only`,
`independent_cache=true`. `sing_box_cm_add_fakeip_dns_server` (`manager:221`):
`inet4_range=198.18.0.0/15`, без v6. DNS-правила reject для `query_type=HTTPS` и
`use-application-dns.net` (`bin/podkop:788-789`). Центральное FakeIP-правило с тегом,
к которому по мере обработки списков патчатся `rule_set` (`:994-1008`, `:1027`, `:1143`).

**Не переносится:** `dnsmasq_configure` (`:350`) / `dnsmasq_restore` (`:381`) с
резервным копированием `server`/`noresolv`/`cachesize` в ключи `podkop_*` и защитой
флагом `shutdown_correctly`. Нам нечего перехватывать — резолвер клиента задаётся в его
конфиге ([DNS и FakeIP](../04-dns-fakeip.md#почему-здесь-проще-чем-в-podkop)).

### Модель конфигурации — переносится с изменением

Секция `config section 'main'`: `connection_type` (`proxy|vpn|block|exclusion`),
`proxy_config_type` (`url|outbound|selector|urltest`), источники списков
(`community_lists`, `user_domains`, `user_subnets`, `remote_domain_lists`,
`local_domain_lists`), `fully_routed_ips`, `resolve_real_ip_for_routing`.

**У нас:** та же семантика, но туннель и правило разделены
([ADR 0003](../decisions/0003-tunnel-separate-from-rule.md)) и добавлен явный приоритет.

### Разбор proxy-URL — переносится по смыслу

`sing_box_config_facade.sh`: `socks4/4a/5` (`:70`), `vless` (`:96`), `ss` (`:109`),
`trojan` (`:139`), `hysteria2|hy2` (`:150`). Security — `_add_outbound_security` (`:174`):
`tls`/`reality` с `sni`, `alpn`, `fp`, `pbk`, `sid`, `insecure`. Transport —
`_add_outbound_transport` (`:236`): `tcp|raw`, `ws` (path/host/ed), `grpc` (serviceName).

Тестовый корпус — `podkop/String-example.md`, 118 строк примеров URI всех протоколов и
транспортов.

**У нас:** та же логика на Go с типами `sing-box/option`
([ADR 0006](../decisions/0006-go-daemon.md)).

### Списки — переносится источник и механика

`constants.sh:66` — 24 сервиса в `COMMUNITY_SERVICES`. Домены берутся как `.srs` из
релизов [allow-domains](https://github.com/itdoginfo/allow-domains) и подключаются
`rule_set` типа `remote` с `update_interval` (`bin/podkop:994`). Подсети для части
сервисов (twitter, meta, telegram, cloudflare, hetzner, ovh, digitalocean, cloudfront,
discord, roblox) качаются plain-списками и заливаются в nft-сет (`:1291-1326`).

`list_update` (`:482`) перед загрузкой делает до 10 попыток проверки DNS и доступности
GitHub с растущим таймаутом — **важная деталь**, без неё первое обновление на медленном
VPS стабильно проваливается. Повторяем.

### Валидаторы фронтенда — копируются как есть

`fe-app-podkop/src/validators/` — `validateIp`, `validateSubnet`, `validateDomain`,
`validateDns`, `validateUrl`, `validatePath`, `validateOutboundJson`, `validateProxyUrl`
и валидаторы конкретных схем (vless/trojan/shadowsocks/socks/hysteria), плюс
`bulkValidate`. Чистый TS без зависимостей, 11 тестовых файлов на Vitest.

`fe-app-podkop/src/helpers/` — `maskIP` (нужен для отчётов диагностики), `prettyBytes`,
`parseProxyString`, `splitProxyString`.

Лицензия позволяет прямое копирование
([ADR 0007](../decisions/0007-license-gplv2.md)).

### Структура диагностики — переносится как UX-паттерн

Вкладка Diagnostic: набор проверок (`runDnsCheck`, `runFakeIPCheck`, `runNftCheck`,
`runSectionsCheck`, `runSingBoxCheck`), общий стор, рендер секций, доступные действия,
дисклеймер со ссылкой на документацию. Диагностические домены `ip.podkop.fyi` /
`fakeip.podkop.fyi` — свои поднимать не обязательно, механика проверки та же.

## Что не переносится

| Что | Почему |
|---|---|
| UCI (`/etc/config/podkop`, `config_foreach`) | только OpenWrt → YAML + SQLite |
| procd (`init.d`, `service_triggers`, netdev-триггеры) | → systemd + netlink-мониторинг |
| Перехват dnsmasq (`:350`, `:381`) | не нужен, см. выше |
| LuCI (CBI-формы, `fs.exec`, rpcd ACL, `baseclass.extend`) | → REST/WS + SPA |
| `sing_box_config_manager.sh` (1508 строк jq) | → типизированные структуры |
| `store.service.ts`, `tab.service.ts` | костыли под LuCI, во фреймворке не нужны |
| Kernel-интерфейсы для исходящих туннелей (`:722`, `manager:914`) | → userspace ([ADR 0002](../decisions/0002-userspace-wireguard-outbound.md)) |
| Чанкование импорта списков | следствие медленного `jq`, у нас не требуется |
| Сборка через OpenWrt SDK, `.apk`/`.ipk` | → `.deb` через nfpm |

## Замечания, найденные при разборе

Не критика проекта — заметки, чтобы не воспроизвести те же места.

**Дефекты**

- `bin/podkop:2706` и `:2727` — в диспетчере есть команды `main` и `check_sing_box_logs`,
  но одноимённых функций в файле нет; вызов упадёт. `check_sing_box_logs` при этом
  документирована в `show_help` (`:2661`).
- `bin/podkop:741` — ветка `connection_type = vpn` использует глобальный `$dns_server`
  из настроек вместо `$domain_resolver_dns_server` секции.
- `check_nft` (`:1699`) опирается на опции `domain_list_enabled`/`domain_list`, которых
  в схеме больше нет (теперь `community_lists`) — ветка расширенного вывода мертва.
- Требуемая версия sing-box указана в трёх местах по-разному: `1.12.0`
  (`SB_REQUIRED_VERSION`), `1.12.4` (хардкод в тексте `check_nft_rules`/`check_sing_box`),
  `1.12.4` (`install.sh`). **Вывод для нас:** версия — одна константа, проверяемая тестом.
- `nolog` печатает только при `[ -t 1 ]`, поэтому `echolog` из фонового `list_update`
  уходит лишь в syslog.
- `list_update:543` проверяет `$? -eq 0` после `config_foreach` — там всегда 0;
  `download_to_file` не возвращает код неуспеха явно, проверки идут по `-s "$tmpfile"`.
- Bash-измы (`[[ ]]`, `==`) в `#!/bin/ash`-скриптах: `logging.sh:9`, `facade:93,136`,
  `rulesets.sh:167`. В busybox ash работают, но нестандартны.

**Архитектурные наблюдения**

- Конфиг хранится в shell-переменной и на каждом шаге прогоняется через `echo | jq` в
  подоболочке. Основная причина медленной генерации и чанкования импортов.
- Локализация продублирована: `luci-app-podkop/xgettext.sh` извлекает строки из
  **собранных** `.js`, а `fe-app-podkop/scripts` — из `.ts` через babel с последующим
  `distribute-locales.js` в `luci-app-podkop/po/`. Два конвейера на одни строки.
  **Вывод для нас:** один источник строк, один конвейер.
- Тестами покрыты только валидаторы и пара хелперов; стор, сервисы и генератор конфига
  не тестируются. **Вывод для нас:** генератор конфига — чистая функция, покрывается
  в первую очередь.
- Очистка при удалении неполная (следы в `/etc/iproute2/rt_tables`, изменённый
  `/etc/config/dhcp`). **Вывод для нас:** все системные изменения перечислены и
  обратимы, тест на чистое удаление — часть CI
  ([установка](../08-install-upgrade.md#удаление)).
