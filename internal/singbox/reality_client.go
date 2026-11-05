package singbox

import (
	"fmt"
	"net/netip"

	"github.com/ArghTeam/razdacha/internal/store"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// RealityClientProfile supplies a complete sing-box client policy. A URI alone
// cannot configure an application's DNS routing or prevent native IPv6 escape.
func RealityClientProfile(settings store.Settings, credential, publicKey, shortID string) ([]byte, error) {
	if settings.EndpointHost == "" || credential == "" || publicKey == "" || shortID == "" {
		return nil, fmt.Errorf("REALITY client profile requires a configured endpoint and credential")
	}
	mtu := settings.ClientMTU
	if mtu < store.MinClientMTU || mtu > store.MaxClientMTU {
		return nil, fmt.Errorf("invalid configured client MTU")
	}
	encoding := "xudp"
	opts := option.Options{
		Log:       &option.LogOptions{Level: "warn"},
		Inbounds:  []option.Inbound{{Type: C.TypeTun, Tag: "client-tun", Options: &option.TunInboundOptions{MTU: uint32(mtu), Address: []netip.Prefix{netip.MustParsePrefix("172.19.0.1/30"), netip.MustParsePrefix("fdfe:dcba:9876::1/126")}, AutoRoute: true, StrictRoute: true, RouteAddress: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}, Stack: "mixed"}}},
		Outbounds: []option.Outbound{{Type: C.TypeVLESS, Tag: "proxy", Options: &option.VLESSOutboundOptions{ServerOptions: option.ServerOptions{Server: settings.EndpointHost, ServerPort: uint16(settings.RealityListenPort)}, UUID: credential, PacketEncoding: &encoding, DialerOptions: option.DialerOptions{DomainResolver: &option.DomainResolveOptions{Server: "endpoint-bootstrap", Strategy: option.DomainStrategy(C.DomainStrategyIPv4Only)}}, OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, ServerName: settings.RealityServerName, UTLS: &option.OutboundUTLSOptions{Enabled: true, Fingerprint: "chrome"}, Reality: &option.OutboundRealityOptions{Enabled: true, PublicKey: publicKey, ShortID: shortID}}}}}},
		DNS: &option.DNSOptions{RawDNSOptions: option.RawDNSOptions{Servers: []option.DNSServerOptions{
			{Type: C.DNSTypeLocal, Tag: "endpoint-bootstrap", Options: &option.LocalDNSServerOptions{}},
			{Type: C.DNSTypeUDP, Tag: "gateway", Options: &option.RemoteDNSServerOptions{LocalDNSServerOptions: option.LocalDNSServerOptions{DialerOptions: option.DialerOptions{Detour: "proxy"}}, DNSServerAddressOptions: option.DNSServerAddressOptions{Server: "10.8.0.1", ServerPort: 53}}},
		}, Rules: []option.DNSRule{reject(option.RawDefaultDNSRule{QueryType: []option.DNSQueryType{option.DNSQueryType(28), option.DNSQueryType(65)}})}, Final: "gateway", DNSClientOptions: option.DNSClientOptions{Strategy: option.DomainStrategy(C.DomainStrategyIPv4Only), IndependentCache: true}}},
		Route: &option.RouteOptions{AutoDetectInterface: true, Final: "proxy", Rules: []option.Rule{
			{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultRule{RawDefaultRule: option.RawDefaultRule{Port: []uint16{53}}, RuleAction: option.RuleAction{Action: C.RuleActionTypeHijackDNS}}},
			{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultRule{RawDefaultRule: option.RawDefaultRule{IPVersion: 6}, RuleAction: option.RuleAction{Action: C.RuleActionTypeReject}}},
		}},
	}
	return Marshal(opts)
}
