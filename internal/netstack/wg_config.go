package netstack

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/ArghTeam/razdacha/internal/store"
)

// Фиксированные поля клиентского конфига (docs/03-networking.md, «Клиентский
// конфиг»). Ни одно из них не настраивается на уровне пира.
const (
	// wgClientAllowedIPs включает ::/0 без выдачи IPv6-адреса намеренно: с одним
	// лишь 0.0.0.0/0 v6-трафик на литеральные адреса уходит мимо туннеля
	// (ADR 0005).
	wgClientAllowedIPs = "0.0.0.0/0, ::/0"
	// wgClientKeepalive — без keepalive мобильный клиент за NAT теряет связь
	// после простоя и не восстанавливает её до следующей исходящей активности.
	wgClientKeepalive = 25
)

// WGConfigFromSettings собирает параметры интерфейса из настроек демона.
//
// MTU берётся из настроек, а не из константы: значение общее для сервера и
// клиентов и обязано совпадать (ADR 0004), а дефолт настроек — 1280.
func WGConfigFromSettings(s store.Settings) (WGConfig, error) {
	pool, err := netip.ParsePrefix(s.WGPool)
	if err != nil {
		return WGConfig{}, fmt.Errorf("%w: пул адресов %q не разобран", ErrWGConfig, s.WGPool)
	}
	addr, err := netip.ParseAddr(s.WGServerAddress)
	if err != nil || !addr.Is4() {
		return WGConfig{}, fmt.Errorf("%w: адрес сервера %q не IPv4",
			ErrWGConfig, s.WGServerAddress)
	}
	if !pool.Contains(addr) {
		return WGConfig{}, fmt.Errorf("%w: адрес сервера %s вне пула %s",
			ErrWGConfig, addr, pool)
	}

	cfg := WGConfig{
		Name:       DefaultWGInterface,
		Address:    netip.PrefixFrom(addr, pool.Bits()),
		ListenPort: s.WGListenPort,
		MTU:        s.ClientMTU,
	}
	if err := cfg.validate(); err != nil {
		return WGConfig{}, err
	}
	return cfg, nil
}

// ClientConfig собирает клиентский конфиг по docs/03-networking.md.
//
// Все поля фиксированы и не настраиваются на уровне пира: DNS держит всю
// селективность (docs/04-dns-fakeip.md), MTU обязан совпадать с MTU wg0
// (ADR 0004), AllowedIPs включает ::/0 без выдачи IPv6-адреса намеренно,
// PersistentKeepalive нужен мобильному клиенту за NAT, PresharedKey выдаётся
// всегда.
//
// Без ключа сервера или адреса подключения конфига не будет: файл без них
// выглядит рабочим, но не подключается, и разбираться с этим будет
// пользователь на телефоне.
func ClientConfig(p store.Peer, s store.Settings, serverPublicKey string) (string, error) {
	var missing []string
	if serverPublicKey == "" {
		missing = append(missing, "публичный ключ сервера")
	}
	if s.EndpointHost == "" {
		missing = append(missing, "адрес сервера для подключения")
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("конфиг пока не выдать: не задан %s", strings.Join(missing, ", "))
	}

	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", p.PrivateKey)
	fmt.Fprintf(&b, "Address    = %s/32\n", p.Address)
	fmt.Fprintf(&b, "DNS        = %s\n", s.WGServerAddress)
	fmt.Fprintf(&b, "MTU        = %d\n", s.ClientMTU)
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey           = %s\n", serverPublicKey)
	fmt.Fprintf(&b, "PresharedKey        = %s\n", p.PresharedKey)
	fmt.Fprintf(&b, "Endpoint            = %s\n",
		net.JoinHostPort(s.EndpointHost, strconv.Itoa(s.WGListenPort)))
	fmt.Fprintf(&b, "AllowedIPs          = %s\n", wgClientAllowedIPs)
	fmt.Fprintf(&b, "PersistentKeepalive = %d\n", wgClientKeepalive)
	return b.String(), nil
}
