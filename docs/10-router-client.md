# 10 — Роутер как full-tunnel клиент

Как подключить OpenWrt-роутер к razdacha так, чтобы **весь трафик роутера и его
LAN-клиентов** уходил на сервер razdacha, а устройства в сети не требовали
собственных WireGuard-конфигов. Клиенты просто в сети роутера — селективный роутинг
(правила, туннели) применяется на сервере.

Проверено на OpenWrt 24.10 (Xiaomi Mi Router 3G) с сервером razdacha на Debian 13.

> Значения `wg_MSK`, `10.8.0.2`, IP сервера, ключи — из вашего клиентского `.conf`
> (razdacha → экран пиров → скачать конфиг). Ниже они как placeholder.

## 0. Идея и подводные камни

Роутер завершает WireGuard-туннель у себя и форвардит **расшифрованный** трафик
LAN-клиентов в туннель. Это отличается от устройства со своим конфигом, где туннель
живёт внутри устройства. Отсюда три места, где всё ломается, если не учесть:

1. **FakeIP.** razdacha раздаёт клиентам fake-адреса `198.18.0.0/15` для доменного
   роутинга (см. [04-dns-fakeip.md](04-dns-fakeip.md)). Роутер видит эти адреса
   **открытым текстом** — и любой локальный перехватчик их сломает:
   - **podkop** на роутере использует **тот же** диапазон `198.18.0.0/15` и tproxy'ит
     его в свой sing-box → пакеты клиентов дропаются, до сервера не доходят.
     **Podkop несовместим с этой схемой — его надо остановить и отключить.**
   - dnsmasq по умолчанию **кэширует** ответы и **режет** `198.18.0.0/15` защитой от
     DNS-rebind. И то и другое ломает FakeIP.
2. **DNS.** Устройство со своим конфигом спрашивает `10.8.0.1` напрямую и получает
   свежий FakeIP. За роутером между клиентом и сервером стоит dnsmasq — он обязан быть
   прозрачным, иначе отдаёт протухший/порезанный FakeIP.
3. **MTU.** Туннель узкий (MTU 1280), а вложенные серверные туннели ещё уже. Без
   MSS-клампинга в обе стороны крупные TLS-хендшейки виснут (см. §4).

## 1. WireGuard-интерфейс клиента

Из клиентского `.conf` заведите WG-интерфейс (пример `wg_MSK`):

```
uci set network.wg_MSK=interface
uci set network.wg_MSK.proto='wireguard'
uci set network.wg_MSK.private_key='<PrivateKey из [Interface]>'
uci add_list network.wg_MSK.addresses='10.8.0.2/32'   # Address из конфига
uci set network.wg_MSK.dns='10.8.0.1'
uci set network.wg_MSK.mtu='1280'
uci add_list network.@wireguard_wg_MSK[0]... # либо через LuCI «Import»
```

Пир (сервер): `public_key` — это `PublicKey` из `[Peer]` (ключ **сервера**),
`preshared_key`, `endpoint_host`/`endpoint_port`, `allowed_ips = 0.0.0.0/0, ::/0`,
`persistent_keepalive = 25`.

> **Грабли:** не перепутайте ключи. На интерфейс — **приватный** ключ клиента, в пир —
> **публичный** ключ сервера. Если поменять местами (или забыть PSK/keepalive),
> рукопожатие не проходит, интерфейс молча мёртв. Проверка: `wg show wg_MSK` должен
> показать `latest handshake` и растущий transfer.

## 2. Full-tunnel маршрутизация

Заворачиваем весь трафик в туннель, но **endpoint сервера пиним через WAN**, иначе
зашифрованные пакеты уйдут в сам туннель → петля.

netifd `config route` на wg-устройство применяется ненадёжно — половинные маршруты
кладём hotplug-скриптом `/etc/hotplug.d/iface/99-wg_MSK-fulltunnel`:

```sh
#!/bin/sh
[ "$ACTION" = "ifup" ] || exit 0
[ "$INTERFACE" = "wg_MSK" ] || exit 0
GW=$(ip route show default dev wan | sed -n 's/.*via \([0-9.]*\).*/\1/p' | head -n1)
[ -n "$GW" ] && ip route replace <SERVER_IP>/32 via "$GW" dev wan
ip route replace 0.0.0.0/1 dev wg_MSK
ip route replace 128.0.0.0/1 dev wg_MSK
ip -6 route replace ::/1 dev wg_MSK 2>/dev/null || true
ip -6 route replace 8000::/1 dev wg_MSK 2>/dev/null || true
```

`0.0.0.0/1` + `128.0.0.0/1` перебивают дефолт по длине префикса детерминированно
(надёжнее игр с метриками). IPv6 заворачивается в туннель без v6-адреса — гасится,
утечки нет. Endpoint-пин можно продублировать как uci `config route` на зону `wan`.

> LAN-подсеть (`192.168.x.0/24`) — connected-маршрут, более специфичный, поэтому
> **доступ к роутеру по LAN не теряется** даже при полном туннеле.

## 3. Firewall

Интерфейс `wg_MSK` — в зону `wan` (у неё `masq=1` и форвардинг из `lan`). Тогда трафик
LAN-клиентов маскарадится и уходит в туннель без доп. правил.

## 4. MSS-клампинг для вложенных туннелей

Штатный `mtu_fix` зоны клампит MSS только `set rt mtu` и **асимметрично** (обратный
SYN-ACK клампится по маршруту к LAN = 1500). Добавьте симметричный кламп для `wg_MSK`
в `/etc/nftables.d/10-wg_MSK-mss.nft` (fw4 подхватит сам):

```
chain mangle_forward {
    iifname "wg_MSK" tcp flags & (fin | syn | rst) == syn tcp option maxseg size > 1240 tcp option maxseg size set 1240
    oifname "wg_MSK" tcp flags & (fin | syn | rst) == syn tcp option maxseg size > 1240 tcp option maxseg size set 1240
}
```

1240 = 1280−40. Если на сервере трафик уходит во **вложенный** WG-туннель с MTU меньше
1280, опустите значение под него.

## 5. DNS: dnsmasq прозрачным, podkop прочь

```
uci set dhcp.@dnsmasq[0].cachesize='0'          # не кэшировать FakeIP
uci set dhcp.@dnsmasq[0].noresolv='1'           # только апстрим ниже
uci -q delete dhcp.@dnsmasq[0].server
uci add_list dhcp.@dnsmasq[0].server='10.8.0.1' # весь DNS на сервер razdacha
uci set dhcp.@dnsmasq[0].rebind_protection='0'  # не резать 198.18.0.0/15
uci commit dhcp
/etc/init.d/dnsmasq restart
```

И обязательно, если на роутере стоял **podkop** (или другой FakeIP-роутер на
`198.18.0.0/15`):

```
/etc/init.d/podkop stop
/etc/init.d/podkop disable
```

Podkop и razdacha-full-tunnel делят один FakeIP-диапазон и несовместимы на одном
роутере. Оставьте что-то одно.

## 6. Проверка

```
wg show wg_MSK | grep handshake                 # туннель жив
curl -sk https://1.1.1.1/cdn-cgi/trace | grep ip=   # внешний IP = сервер razdacha
```

С реального LAN-устройства (не только с роутера): откройте сайты, которые роутит
правило на сервере, и убедитесь, что внешний IP сменился. `198.18.x` в захвате
`tcpdump -ni wg0 net 198.18.0.0/15` на **сервере** должен показывать трафик от адреса
роутера — если его нет, FakeIP всё ещё перехватывается на роутере (см. §5).

## Отладка «половина сайтов не открывается»

Симптом: прямые сайты и устройства со своим конфигом работают, а за роутером домены
из правил рвутся/висят.

1. `nslookup <домен> 10.8.0.1` с роутера vs напрямую — FakeIP должны **совпадать** и
   быть в `198.18.0.0/15`. Не совпадают/пусто → dnsmasq (§5).
2. `nft list tables` на роутере — есть ли `PodkopTable`? Есть → podkop перехватывает
   FakeIP (§5).
3. На сервере `journalctl -u sing-box | grep -i "missing fakeip\|empty result"` —
   протухший FakeIP или сбой резолва.
4. Живость туннеля сервера — Clash API: `curl 127.0.0.1:9090/proxies/<tag>/delay?...`.
   Метка `up` в панели надёжность данных не гарантирует, delay-тест — да.
