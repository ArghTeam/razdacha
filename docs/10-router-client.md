# 10 — Роутер как клиент razdacha

Как подключить OpenWrt-роутер к razdacha так, чтобы **весь трафик LAN и устройств за
мэшем** уходил на сервер razdacha, а устройства в сети не требовали собственных
WireGuard-конфигов. Селективный роутинг (правила, туннели) применяется на сервере.

Проверено 2026-08-20 на OpenWrt 24.10.4 (Xiaomi Mi Router 3G) с сервером razdacha на
Debian 13, за двойным NAT провайдера, с мэшем Deco в режиме моста.

> Значения `wg_MSK`, `10.8.0.2`, IP сервера, ключи — из вашего клиентского `.conf`
> (razdacha → экран пиров → скачать конфиг). Ниже они как placeholder.

## 0. Главное решение: заворот делается policy routing, а не дефолтом

Наивная схема «прописать `0.0.0.0/1` + `128.0.0.0/1` через туннель» **отвергнута**. Она
утаскивает в туннель и трафик самого роутера, а это порождает тупик, из которого сеть
не выходит сама:

1. роутер без RTC после перезагрузки имеет часы из прошлого;
2. WireGuard кладёт в handshake TAI64N-таймстамп, сервер хранит последний виденный и
   молча отбрасывает инициацию «из прошлого» — туннель не поднимается;
3. NTP починил бы часы, но его трафик уже завёрнут в мёртвый туннель.

Итог: роутер не поднимается, **пока не передёрнешь WireGuard на сервере** (это сбрасывает
хранимый таймстамп). Симптом со стороны роутера — интерфейс с растущим TX и нулевым RX.

Поэтому: **в `main` всегда остаётся дефолт через WAN**, роутер ходит наружу напрямую, а
LAN заворачивается отдельной таблицей по правилу. NTP синхронизируется, анти-реплей не
срабатывает, endpoint достижим.

## 1. Аварийный канал — первым, до всего остального

Прежде чем трогать туннель, поднимите сеть, которая **всегда** ходит напрямую и через
которую вы попадёте на роутер, когда основная схема ляжет. Без неё любая ошибка означает
кабель, failsafe или сброс до заводских.

```sh
# отдельный мост и подсеть
uci set network.br_direct=device
uci set network.br_direct.type='bridge'
uci set network.br_direct.name='br-direct'
uci set network.direct=interface
uci set network.direct.proto='static'
uci set network.direct.device='br-direct'
uci set network.direct.ipaddr='192.168.9.1'
uci set network.direct.netmask='255.255.255.0'

# правило и ОБЕ строки своей таблицы — их держит netifd, не hotplug
uci set network.directrule=rule
uci set network.directrule.src='192.168.9.0/24'
uci set network.directrule.lookup='200'
uci set network.directrule.priority='100'
uci set network.directroute=route
uci set network.directroute.interface='wan'
uci set network.directroute.target='0.0.0.0'
uci set network.directroute.netmask='0.0.0.0'
uci set network.directroute.gateway='<шлюз WAN>'
uci set network.directroute.table='200'
uci set network.directlocal=route
uci set network.directlocal.interface='direct'
uci set network.directlocal.target='192.168.9.0'
uci set network.directlocal.netmask='255.255.255.0'
uci set network.directlocal.table='200'
```

> **Связанный маршрут в таблице 200 обязателен.** Правило `from 192.168.9.0/24` ловит и
> пакеты с исходником `192.168.9.1`, то есть **ответы самого роутера**. Без строки
> `192.168.9.0/24 dev br-direct` они уйдут в WAN: роутер будет пинговать клиента, а
> клиент роутера — нет, и управление пропадёт.

DHCP этой сети раздаёт **публичный** DNS, чтобы не зависеть ни от dnsmasq, ни от туннеля:

```sh
uci set dhcp.direct=dhcp
uci set dhcp.direct.interface='direct'
uci set dhcp.direct.start='100'
uci set dhcp.direct.limit='50'
uci add_list dhcp.direct.dhcp_option='6,1.1.1.1,8.8.8.8'
```

Точка доступа — с **фиксированным каналом** (`channel='6'`, не `auto`): после перезагрузки
`auto` уезжает на другой канал, клиент прилипает к соседней точке и держит старую лизу,
что выглядит как «роутер недоступен».

Зона firewall — штатная, с `input ACCEPT`; форвардинг `direct → wan`:

```sh
uci set firewall.direct=zone
uci set firewall.direct.name='direct'
uci add_list firewall.direct.network='direct'
uci set firewall.direct.input='ACCEPT'
uci set firewall.direct.output='ACCEPT'
uci set firewall.direct.forward='REJECT'
uci set firewall.direct_wan=forwarding
uci set firewall.direct_wan.src='direct'
uci set firewall.direct_wan.dest='wan'
```

> **Не пишите свои файлы в `/etc/nftables.d`.** Include, переоткрывающий цепочку `input`,
> не собирается, и nft отвергает **весь** ruleset: роутер остаётся без NAT, а интернет
> пропадает у всей сети, не только у аварийной. Штатной зоны достаточно.

Проверка, что канал действительно резервный, а не «пока работает»: `/etc/init.d/network
restart`, после него правило, таблица и лиза должны быть на месте, а пинг наружу с
исходником `192.168.9.1` — проходить.

## 2. Туннель без заворота

Сначала поднимите `wg_MSK` так, чтобы он **ничего не маршрутизировал**, и убедитесь, что
он живой:

```sh
uci set network.wg_MSK=interface
uci set network.wg_MSK.proto='wireguard'
uci set network.wg_MSK.private_key='<PrivateKey клиента>'
uci add_list network.wg_MSK.addresses='10.8.0.2/32'
uci set network.wg_MSK.mtu='1280'

uci add network wireguard_wg_MSK
uci set network.@wireguard_wg_MSK[-1].public_key='<PublicKey сервера>'
uci set network.@wireguard_wg_MSK[-1].preshared_key='<PSK>'
uci set network.@wireguard_wg_MSK[-1].endpoint_host='<IP сервера>'
uci set network.@wireguard_wg_MSK[-1].endpoint_port='51820'
uci add_list network.@wireguard_wg_MSK[-1].allowed_ips='0.0.0.0/0'
uci set network.@wireguard_wg_MSK[-1].persistent_keepalive='25'
uci set network.@wireguard_wg_MSK[-1].route_allowed_ips='0'   # ключевое

# endpoint через WAN, иначе шифрованные пакеты уйдут в сам туннель
uci set network.wgmskpin=route
uci set network.wgmskpin.interface='wan'
uci set network.wgmskpin.target='<IP сервера>'
uci set network.wgmskpin.netmask='255.255.255.255'
uci set network.wgmskpin.gateway='<шлюз WAN>'
```

Живая проверка — не по наличию интерфейса, а по факту прохождения трафика: временно
завернуть один адрес и посмотреть, с чьего адреса выходим.

```sh
ip route replace 1.1.1.1/32 dev wg_MSK
uclient-fetch -q -O - https://1.1.1.1/cdn-cgi/trace | grep ^ip=   # ждём IP сервера
ip route del 1.1.1.1/32 dev wg_MSK
```

> **Ключи не путать:** на интерфейс — приватный ключ клиента, в пир — публичный ключ
> сервера. Перепутанные ключи дают молча мёртвый интерфейс.

## 3. Заворот LAN и kill-switch

```sh
# таблица 100: дефолт в туннель И связанный маршрут LAN (та же ловушка, что в §1)
uci set network.lantunnel=route
uci set network.lantunnel.interface='wg_MSK'
uci set network.lantunnel.target='0.0.0.0'
uci set network.lantunnel.netmask='0.0.0.0'
uci set network.lantunnel.table='100'
uci set network.lanlocal=route
uci set network.lanlocal.interface='lan'
uci set network.lanlocal.target='192.168.1.0'
uci set network.lanlocal.netmask='255.255.255.0'
uci set network.lanlocal.table='100'

# правило заворота и следом заглушка
uci set network.lanrule=rule
uci set network.lanrule.src='192.168.1.0/24'
uci set network.lanrule.lookup='100'
uci set network.lanrule.priority='90'
uci set network.languard=rule
uci set network.languard.src='192.168.1.0/24'
uci set network.languard.action='unreachable'
uci set network.languard.priority='91'
```

> **Заглушка обязательна.** Policy routing при **пустой таблице проваливается дальше по
> правилам** — в `main`, где дефолт через WAN. Упал `wg_MSK` — ядро убрало маршрут,
> таблица 100 опустела, и без правила 91 трафик LAN тихо потечёт напрямую. Правило 91
> закрывает и второе окно: на загрузке правила встают раньше, чем поднимется туннель.
> `unreachable` предпочтительнее `blackhole` — клиент получает ICMP и падает сразу,
> вместо повисших соединений.
>
> Задавать заглушку маршрутом (`config route` с типом) на этой сборке нельзя: валидатор
> netifd знает `option action` только у `config rule`.

Второй, независимый от маршрутизации слой — firewall. `wg_MSK` выносится в **свою** зону,
а форвардинг `lan → wan` удаляется совсем:

```sh
uci set firewall.vpn=zone
uci set firewall.vpn.name='vpn'
uci add_list firewall.vpn.network='wg_MSK'
uci set firewall.vpn.input='REJECT'
uci set firewall.vpn.output='ACCEPT'
uci set firewall.vpn.forward='REJECT'
uci set firewall.vpn.masq='1'        # без NAT сервер отбросит пакеты с src 192.168.1.x
uci set firewall.vpn.mtu_fix='1'     # MSS штатным способом, без своих nft-файлов
uci set firewall.lan_vpn=forwarding
uci set firewall.lan_vpn.src='lan'
uci set firewall.lan_vpn.dest='vpn'
# и удалить forwarding lan -> wan
```

Побочный эффект, который надо принять осознанно: LAN теряет доступ к админке модема
провайдера — она остаётся доступной только из аварийной сети.

## 4. DNS

razdacha раздаёт FakeIP `198.18.0.0/15` для доменного роутинга
([04-dns-fakeip.md](04-dns-fakeip.md)), поэтому DNS клиентов обязан приходить от неё.

```sh
uci set dhcp.@dnsmasq[0].cachesize='0'            # FakeIP нельзя кэшировать
uci set dhcp.@dnsmasq[0].rebind_protection='0'    # иначе режет 198.18.0.0/15
uci set dhcp.@dnsmasq[0].noresolv='1'
uci add_list dhcp.@dnsmasq[0].server='10.8.0.1'
uci add_list dhcp.@dnsmasq[0].server='/pool.ntp.org/1.1.1.1'   # см. ниже
```

Плюс **host-маршрут к DNS сервера** — это трафик самого роутера, правило `from
192.168.1.0/24` его не ловит, а в `main` маршрута к `10.8.0.0/24` нет:

```sh
uci set network.razdns=route
uci set network.razdns.interface='wg_MSK'
uci set network.razdns.target='10.8.0.1'
uci set network.razdns.netmask='255.255.255.255'
```

> **Клиентам туннельный DNS DHCP-опцией не раздавать.** Лиза на 12 часов переживает откат
> конфига: маршруты вернулись, а устройства продолжают спрашивать адрес, до которого
> больше нет пути — откат выглядит бесполезным. Пусть клиенты спрашивают роутер, а
> апстрим меняется одной строкой на роутере.
>
> **Исключение для NTP обязательно.** При `noresolv` имена пулов резолвятся через
> туннель, а туннель после ребута ждёт правильных часов — тупик из §0. Строка
> `/pool.ntp.org/1.1.1.1` разрывает круг.

## 5. Мэш и охват правила

Deco (и подобные) в режиме моста **не делают NAT**: их блоки и все клиенты получают
адреса от роутера и живут в `192.168.1.0/24`. Проверяется по лизам — если в
`/tmp/dhcp.leases` видны и блоки мэша, и обычные устройства, режим мостовой.

Следствие: правило на подсеть накрывает **весь дом**, включая телевизор и чужие телефоны.
Внешний адрес у всех станет адресом сервера, а падение туннеля оставит без интернета всех
сразу. Если нужен выбор — закрепите адреса за нужными устройствами (статические лизы) и
пишите правила по IP; телефоны при этом должны отключить приватный MAC, иначе привязка
слетает.

## 6. Порядок работ, который спасает от сброса до заводских

1. **Снимок вне роутера.** `sysupgrade -b /root/backup.tar.gz` плюс копия на рабочей
   машине: `/root` переживает перезагрузку и откат, но **не** заводский сброс.
2. **Сторож в кроне.** Скрипт раз в минуту: dead-man (взведён флаг с дедлайном, нет
   подтверждения — восстановить конфиги из снимка) и проверка инвариантов аварийной сети
   (адрес на мосту, правило, обе строки таблицы, `dhcp-range` в рабочем конфиге dnsmasq).
   Отложенный откат, запущенный внутри ssh-сессии, умирает вместе с ней — крон нет.
3. **Изменение — скриптом с самопроверкой.** Скрипт сохраняет
   `/etc/config/{network,wireless,firewall,dhcp}`, применяет, прогоняет проверки и при
   первой неудаче возвращает конфиги и перезапускает сервисы. Запускать отвязанно:
   `setsid sh -c ... </dev/null &`, вывод в лог-файл.
4. **Порядок проверок:** сначала «не сломалось ли то, чем пользуются» — NAT
   (`nft list chain inet fw4 srcnat_wan` содержит `masquerade`), интернет у роутера, выход
   аварийной сети, её DHCP; и только потом «работает ли туннель».
5. **`fw4 check` до применения.** Он собирает набор правил и печатает его, ничего не
   применяя. Не собралось — не применять.
6. **Раскатка от малого:** сначала правило для одного адреса, потом на всю подсеть.

## 7. Проверка результата

```sh
wg show wg_MSK | grep handshake                  # туннель жив
ip rule show                                     # 90 lookup 100, 91 unreachable, 100 lookup 200
ip route show table 100                          # дефолт в wg_MSK + связанный маршрут LAN
ip route get 8.8.8.8 from <IP клиента LAN> iif br-lan   # должен показать dev wg_MSK
nft list chain inet fw4 forward_lan               # accept только в зону vpn, не в wan
ping -c2 -I 192.168.9.1 1.1.1.1                   # аварийная сеть ходит напрямую
```

С реального устройства LAN внешний адрес должен стать адресом сервера, а с устройства в
аварийной сети — остаться адресом провайдера.

## Где набили шишки

Каждый случай наблюдался на живом роутере 2026-08-20, симптом → причина:

| Симптом | Причина |
|---|---|
| Wi-Fi «отваливается» через 30 секунд после подключения | DHCP не выдавался: секция создана, но dnsmasq не перезапущен, в рабочем конфиге нет `dhcp-range`. hostapd выкидывает клиента `deauthenticated due to inactivity` |
| Роутер пингует клиента, клиент роутера — нет | В таблице аварийной сети не было связанного маршрута; ответы с `192.168.9.1` уходили в WAN |
| Правила пропадают после `network restart` | Ставились hotplug-скриптом, который молча выходит, если шлюз ещё не поднялся. Держать должен netifd |
| «Роутер недоступен», хотя он цел | `channel=auto`: после ребута точка уехала на другой канал, клиент прилип к соседней с прежней лизой |
| Интернет пропал у всей сети, не только у LAN | Свой include в `/etc/nftables.d` не собрался, nft отверг весь ruleset — роутер остался без NAT |
| Откат не помог, интернета всё равно нет | Клиентам раздали DNS `10.8.0.1` лизой на 12 часов; маршруты вернулись, а устройства продолжали спрашивать мёртвый адрес |
| Туннель не поднимается после перезагрузки роутера, TX растёт, RX ноль | Часы из прошлого + анти-реплей WireGuard; NTP заперт внутри неподнявшегося туннеля (§0) |
| Отложенный откат не сработал | Запускался внутри ssh-сессии и умер вместе с ней; нужен крон |
