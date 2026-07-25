package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// unknownClient — ключ для запросов, чей адрес не разобрался. Такие попытки
// считаются одной корзиной, а не пропускаются мимо блокировки.
const unknownClient = "неизвестный адрес"

// clientKey возвращает адрес клиента, по которому считаются неудачные попытки
// входа.
//
// Заголовкам `X-Real-IP` и `X-Forwarded-For` можно верить **только** когда
// соединение пришло с loopback: там сидит наш nginx, который эти заголовки
// перезаписывает. Запрос с любого другого адреса приносит их от клиента, и
// доверие к ним означало бы, что перебор обходится сменой строки в заголовке.
//
// Из `X-Forwarded-For` берётся последний элемент, а не первый: nginx добавляет
// реальный адрес соединения в конец списка ($proxy_add_x_forwarded_for), тогда
// как начало списка пришло от клиента и подделывается свободно.
func clientKey(r *http.Request) string {
	peer := peerAddr(r.RemoteAddr)
	if !peer.IsValid() {
		return unknownClient
	}
	if !peer.IsLoopback() {
		return peer.String()
	}
	if addr, ok := parseClientAddr(r.Header.Get("X-Real-IP")); ok {
		return addr.String()
	}
	if addr, ok := lastForwarded(r.Header.Values("X-Forwarded-For")); ok {
		return addr.String()
	}
	// nginx без проброса заголовков: все запросы попадают в одну корзину.
	// Это хуже точности, но безопаснее, чем отсутствие блокировки.
	return peer.String()
}

// peerAddr разбирает адрес соединения из RemoteAddr.
func peerAddr(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	addr, ok := parseClientAddr(host)
	if !ok {
		return netip.Addr{}
	}
	return addr
}

// parseClientAddr разбирает адрес, снимая зону и приводя v4-in-v6 к v4:
// иначе один и тот же клиент получал бы разные корзины блокировки.
func parseClientAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	addr = addr.Unmap().WithZone("")
	return addr, addr.IsValid()
}

// lastForwarded достаёт последний разобранный адрес из X-Forwarded-For.
func lastForwarded(values []string) (netip.Addr, bool) {
	for i := len(values) - 1; i >= 0; i-- {
		parts := strings.Split(values[i], ",")
		for j := len(parts) - 1; j >= 0; j-- {
			if addr, ok := parseClientAddr(parts[j]); ok {
				return addr, true
			}
		}
	}
	return netip.Addr{}, false
}
