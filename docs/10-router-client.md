# 10 — OpenWrt router client

## Verified Xiaomi and Deco setup

Verified on 2026-11-27 with OpenWrt 24.10.4 on a Xiaomi Mi Router 3G. One razdacha
client has WireGuard and VLESS + REALITY enabled. Native WireGuard and sing-box run
independently on the router. The client's server-side policy covers the bridged
Deco network. A phone on Deco Wi-Fi confirmed VPN internet access after the DHCP
repair described below.

Use this section for the current setup. The collapsed August record preserves the
previous WireGuard-only procedure. Its interface names, port membership, policy
tables and shared DNS changes belong to that older configuration.

| Physical connection | OpenWrt port or bridge | Purpose |
|---|---|---|
| Jack 1, wired administration | `lan2` on `br-lan` | LuCI, SSH and direct WAN access |
| Jack 2, Deco uplink | `lan1` on `br-rzd-mesh` | VPN-only access for Deco Wi-Fi and downstream Ethernet |
| Recovery Wi-Fi `X-Direct` | `br-direct` | LuCI, SSH and direct WAN access |

This mapping was established by testing the actual cables. Confirm it again on
different hardware. Bridged Deco clients receive addresses from OpenWrt's mesh
DHCP pool. Keep addresses, credentials and private profiles in protected router
configuration and encrypted backups. Replace every angle-bracket placeholder
before running an example. Keep command output containing addresses private.

### Routing and bootstrap

The `main` table retains its WAN default. Router DNS, NTP and the outer connections
to both VPN endpoints remain reachable through WAN during startup and outages.
TX with zero RX can have several causes. Check time synchronization, endpoint
reachability, keys and actual traffic before attributing a failure to one cause.

| Component | Current configuration |
|---|---|
| WireGuard | `rzd_wg`, MTU 1280, keepalive 25, `route_allowed_ips=0` |
| VLESS + REALITY | sing-box TUN `rzvless0`, network `rzd_vless`, gVisor stack, IPv4, automatic routes disabled |
| Recovery Wi-Fi | Priority 100 looks up table 200, containing its connected subnet and WAN default |
| WireGuard probe | Priority 110 looks up table 1100 by tunnel source address, with unreachable rule 111 |
| VLESS probe | Priority 112 looks up table 1101 by tunnel source address, with unreachable rule 113 |
| Mesh traffic | Priority 120 looks up table 1110 by mesh subnet, with unreachable rule 121 |

The recovery bridge has `bridge_empty=1`, preserving its address and routes before
the Wi-Fi interface attaches. Its connected-subnet route keeps router replies on
the recovery network.

Table 1110 contains a default through the selected tunnel. The selector removes
that default when both checks fail. The following unreachable rule blocks
fallthrough to WAN. The mesh firewall zone forwards only to the VPN zone, which
provides masquerading and MSS clamping. A narrow forwarding guard at priority -5
drops mesh IPv6 and mesh traffic whose output device is outside `rzd_wg` and
`rzvless0`, including established flows after a route change. Validate the complete
ruleset with `fw4 check` before reloading the firewall.

Mesh IPv6 is disabled on the bridge, with RA and DHCPv6 disabled and a forwarding
drop. Mesh TCP and UDP DNS on port 53 are redirected to razdacha gateway DNS through
the selected VPN. The shared router resolver keeps its independent WAN bootstrap
path, preserving DNS for direct management when both VPNs fail.

### Services and independent health checks

| File or service | Role |
|---|---|
| `/etc/init.d/sing-box` | Independent VLESS service |
| `/etc/sing-box/razdacha-router.json` | Private VLESS TUN configuration |
| `/etc/init.d/razdacha-router-vpn` | procd-managed selector |
| `/etc/razdacha-router-vpn/select.sh` | End-to-end probes and selected route |
| `/etc/razdacha-router-vpn/probe.conf` | Private tunnel sources, DNS and expected egress |
| `/tmp/razdacha-router-vpn/active` | `wg`, `vless` or `blocked` |
| `/etc/nftables.d/90-rzd-mesh-guard.nft` | Independent forwarding guard |

Each probe uses its own source address and policy table. It checks HTTPS against
the expected egress and performs a UDP DNS lookup through the gateway. A handshake
or existing interface alone cannot establish health. On this device the VLESS
system TUN stack answered DNS while HTTPS failed. The gVisor stack passed both.

The selector prefers WireGuard. It requires two consecutive failed samples to
leave an active channel and four healthy WireGuard samples for normal failback
from healthy VLESS. Startup requires two successes. If the active channel fails,
an alternate with two successes can take over. Probe timeouts affect elapsed
failover time. Applications can need to reconnect after a path change.

Run the following checks once per tunnel using private profile values.
`<PROBE_URL>` is a numeric HTTPS endpoint with a valid certificate and a trace
response containing an `ip=` field. `<PROBE_IPV4>` is its destination address.

```sh
set -e
source_address='<TUNNEL_SOURCE_IPV4>'
device='<rzd_wg_OR_rzvless0>'
probe_address='<PROBE_IPV4>'
probe_url='<PROBE_URL>'
gateway_dns='<GATEWAY_DNS_IPV4>'
expected_egress='<EXPECTED_EGRESS_IPV4>'

ip -4 route get "$probe_address" from "$source_address" |
    grep -Fq "dev $device "
echo 'Probe route verified'
trace=$(curl -q -4 --interface "$source_address" -fsS \
    --connect-timeout 5 --max-time 10 "$probe_url")
printf '%s\n' "$trace" | grep -Fxq "ip=$expected_egress"
echo 'HTTPS egress verified'
for transport in +notcp +tcp; do
    answer=$(dig -b "$source_address" @"$gateway_dns" example.net A \
        "$transport" +time=3 +tries=1 +short)
    printf '%s\n' "$answer" | grep -Eq '^([0-9]{1,3}\.){3}[0-9]{1,3}$'
    echo "DNS response verified over $transport"
done
cat /tmp/razdacha-router-vpn/active
```

### DHCP recovery on the Deco network

During the November checks, competing DHCP replies arrived from the physical Deco
port. OpenWrt omitted the mesh DHCP pool from the generated dnsmasq configuration.
A fresh client received a different gateway and failed HTTPS even though both
router VPN probes passed. The observation established the ingress port. Ownership
of the competing responder remained unverified.

The repair explicitly enabled OpenWrt's DHCP pool on the dedicated mesh interface.
Verify the section belongs to the intended network, then apply through wired
management or X-Direct.

```sh
test "$(uci -q get dhcp.rzd_mesh.interface)" = 'rzd_mesh' || exit 1
uci set dhcp.rzd_mesh.force='1'
uci commit dhcp
/etc/init.d/dnsmasq restart
```

Reconnect the affected Wi-Fi client to obtain a fresh lease. Compare its DHCP
server, gateway and DNS with the private mesh configuration, then test HTTPS and
gateway DNS. Three fresh leases passed DHCP, HTTPS and DNS after the repair.
Competing offers stopped appearing during those trials and subsequent observation
of the physical port. The user confirmed VPN internet on a phone through Deco.
The repair required a dnsmasq restart.

The `force` option enables the local pool when another DHCP server is detected.
Another device can still answer inside Deco. If competing offers persist,
identify that responder and disable its DHCP service. Where available, inspect
Deco's Smart DHCP option under **More → Advanced → Smart DHCP**. Availability
depends on model and firmware. The vendor describes the option in its
[DHCP guidance](https://community.tp-link.com/en/home/forum/topic/832526?moduleId=39).

### Acceptance checks after changes

Perform disruptive checks in an agreed maintenance window with direct wired
access and an armed rollback timer. Stage the VPN forwarding path with a separate
client before moving the physical Deco port.

1. Check LuCI, SSH, DHCP and direct internet independently over jack 1 and X-Direct.
2. Check each tunnel's own route, expected HTTPS egress and UDP/TCP DNS response.
3. Renew a real Deco Wi-Fi client's lease, verify its gateway, and test HTTPS and
   DNS. Check a downstream Ethernet client as well when used.
4. Interrupt each active transport and verify forwarding through the other.
   Include an outage with the interface still present.
5. Stop both transports and verify mesh HTTPS and UDP/TCP DNS fail while wired
   management and X-Direct work. Restore VLESS first, then WireGuard, and verify
   recovery and preferred-path failback.
6. In a maintenance window, test full boots with each transport available alone.
   Restore normal startup with both enabled and repeat real-client and management
   checks after boot.

The November tests covered full boots with each transport alone, transport failure
while the WireGuard interface remained present, simultaneous failure, recovery and
failback. Both tunnel HTTPS and DNS checks passed after the interface was renamed
to its current name, `rzd_wg`. The rename and DHCP repair were
applied without another reboot, so boot persistence of those final changes still
needs verification in a future maintenance window.

### Backups, rollback and recovery after a factory reset

Create a new encrypted post-cutover backup outside the router, including the
custom files below. The verified off-device pre-cutover copy supports rollback to
the previous topology and predates the selector. Check `sysupgrade -l` privately
before creating the new configuration archive. The November audit included the UCI
network, wireless, firewall, DHCP and sing-box configuration, the nftables guard,
private sing-box JSON and root crontab. It omitted the following custom files.

- `/etc/init.d/razdacha-router-vpn`.
- `/etc/razdacha-router-vpn/probe.conf` and `select.sh`.
- `/root/razdacha-mesh-recovery/watchdog.sh`.

Include these files and the intended rollback snapshot explicitly in a protected
supplementary backup or configure their inclusion before creating the archive.
Preserve private file permissions. Exclude an active `armed` marker and runtime
state from a baseline restore. A copied cron entry needs its matching watchdog
script. Confirm archive contents before relying on the backup.

The tested watchdog runs from cron once a minute. Its armed state records a boot
ID, a same-boot uptime deadline and a separate next-boot uptime deadline. It can
restore the private pre-cutover UCI snapshot and stop the selector when reverting
port membership. Choose the intended snapshot before arming it. A current backup
and a pre-cutover snapshot represent different recovery points. Disarm the timer
after client traffic and both administration paths pass.

After a factory reset, files under `/root` and installed packages may be gone.
Recover through direct Ethernet in the following order.

1. Establish administration and WAN, verify the hardware port mapping, and enable
   time synchronization.
2. Install signed `kmod-wireguard`, `wireguard-tools`, `kmod-tun`, `sing-box-tiny`,
   `curl` and `bind-dig` from the matching official feeds. Kernel modules must match
   the installed kernel ABI. Configuration archives do not reinstall binaries.
3. Extract the protected backup to a private staging directory. Review saved port
   membership, firewall and startup settings before applying them. Begin with the
   pre-cutover management topology or stage the post-cutover configuration with
   the Deco port kept on its current network and VPN autostart held disabled.
   Preserve private permissions and check both profiles against production.
4. Restore and apply X-Direct, its empty bridge, DHCP, firewall and both table 200
   routes. Verify it and wired management independently. Arm the watchdog against
   the intended known-good rollback snapshot.
5. Validate the staged VPN configuration with
   `sing-box check -c /etc/sing-box/razdacha-router.json` and validate the full
   firewall with `fw4 check` before starting services. Enable both VPN services and
   the selector, then run the isolated-client acceptance checks.
6. Move only the verified Deco port into `br-rzd-mesh`, renew downstream leases,
   and check actual Deco Wi-Fi and Ethernet traffic before disarming rollback.

The deployed custom selector and private profiles currently live on the router.
Verify their presence in the new off-device backup before relying on it for a
factory-reset recovery. WAN and the production gateway are shared dependencies of
both protocols.

Operational evidence is tracked in [epic #363](https://github.com/ArghTeam/razdacha/issues/363),
[mesh isolation #365](https://github.com/ArghTeam/razdacha/issues/365) and
[failover #366](https://github.com/ArghTeam/razdacha/issues/366).

<details>
<summary>Historical WireGuard-only setup verified on 2026-08-20</summary>

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

</details>
