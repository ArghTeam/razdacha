package packaging

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// probeTarget — куда строится маршрут при определении внешнего адреса. Это
// публичный адрес DNS-резолвера Cloudflare: он нужен только как «что-нибудь
// заведомо не наше», запрос туда не уходит.
const probeTarget = "1.1.1.1:53"

// probeTimeout — потолок на создание сокета. Пакетов нет, ядро отвечает сразу,
// но упереться в неотвечающий netlink на странной системе мы не должны.
const probeTimeout = 2 * time.Second

// DetectExternalAddr возвращает адрес, с которого машина выходит наружу.
//
// Способ — UDP-сокет до публичного адреса: connect на UDP не шлёт ни одного
// пакета, он только просит ядро выбрать маршрут, а вместе с ним и локальный
// адрес WAN-интерфейса. Отсюда три свойства, ради которых способ и выбран:
// не нужен root, не нужен интернет и не нужна сторонняя служба вроде
// ifconfig.me, которой пришлось бы доверять и которая однажды не ответит.
//
// Чего способ не умеет: за NAT он вернёт адрес внутри частной сети, а не тот,
// по которому машину видно снаружи. Для VPS — обычного места установки — это
// один и тот же адрес; для домашней машины в SAN сертификата попадёт локальный
// адрес, и это ровно тот адрес, по которому в панель и заходят.
func DetectExternalAddr() (netip.Addr, error) {
	return detectExternalAddr(probeTarget)
}

func detectExternalAddr(target string) (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp4", target)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("маршрут наружу не найден: %w", err)
	}
	defer func() { _ = conn.Close() }()

	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}, fmt.Errorf("локальный адрес сокета не UDP: %v", conn.LocalAddr())
	}
	addr, ok := netip.AddrFromSlice(local.IP)
	if !ok {
		return netip.Addr{}, fmt.Errorf("локальный адрес %v не разобран", local.IP)
	}
	addr = addr.Unmap()
	if !addr.Is4() || addr.IsLoopback() || addr.IsUnspecified() {
		return netip.Addr{}, fmt.Errorf("внешним адресом выбран %s", addr)
	}
	return addr, nil
}
