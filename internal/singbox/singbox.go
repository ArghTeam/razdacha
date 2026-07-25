package singbox

import (
	"fmt"
	"net/netip"
)

// Теги, на которые ссылаются правила. Меняются только вместе с генератором:
// на них завязаны route.final, dns.final и клиент Clash API.
const (
	TagDirect       = "direct-out" // прямой выход, он же route.final
	TagDNSIn        = "dns-in"     // inbound на 10.8.0.1:53
	TagTProxyIn     = "tproxy-in"  // inbound tproxy на 127.0.0.1:1602
	TagDNSUpstream  = "upstream"   // апстрим-резолвер из настроек
	TagDNSBootstrap = "bootstrap"  // резолвер адреса апстрима, когда тот задан доменом
	TagDNSFakeIP    = "fakeip"     // выдача FakeIP доменам из правил
)

// Фиксированные адреса из docs/01-architecture.md. Не настраиваются: tproxy и
// Clash API слушают только на loopback, DNS — только на адресе wg0.
const (
	tproxyListen  = "127.0.0.1"
	tproxyPort    = 1602
	clashListen   = "127.0.0.1:9090"
	dnsPort       = 53
	bootstrapAddr = "1.1.1.1"

	// cacheFile хранит соответствие FakeIP ↔ домен между перезапусками sing-box.
	// Без него после reload клиент с закэшированным FakeIP уходит в никуда.
	cacheFile = "/var/lib/sing-box/cache.db"

	// fakeIPTTL — короткий TTL на FakeIP-ответах: правка правил в UI вступает в
	// силу за минуту, а не через сутки клиентского кэша.
	fakeIPTTL = 60
)

// fakeIPRange — диапазон FakeIP. Только v4: IPv6 отключён (ADR 0005).
var fakeIPRange = netip.MustParsePrefix("198.18.0.0/15")

// communityListURL — шаблон адреса .srs-набора сервиса в релизах allow-domains,
// тот же источник, что у Podkop (podkop/files/usr/lib/constants.sh:55).
// Каталог ключей ведёт слой lists, здесь только сборка адреса.
const communityListURL = "https://github.com/itdoginfo/allow-domains/releases/latest/download/%s.srs"

// TunnelTag — тег outbound либо endpoint туннеля.
func TunnelTag(id string) string { return "tun-" + id }

// ruleSetTag — тег набора со своими доменами и подсетями правила.
func ruleSetTag(id string) string { return "rule-" + id }

// communityTag — тег удалённого набора community-списка.
func communityTag(key string) string { return "list-" + key }

// remoteListTag — тег удалённого набора из своего URL правила.
func remoteListTag(ruleID string, i int) string {
	return fmt.Sprintf("rule-%s-remote-%d", ruleID, i)
}
