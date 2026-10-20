package singbox

import (
	"fmt"
	"net/netip"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	"github.com/ArghTeam/razdacha/internal/ippolicy"
	"github.com/ArghTeam/razdacha/internal/store"
)

// peerEgress prepends source policy after DNS hijack and the DNS safety guards.
// Explicit resolution is essential: cached FakeIP becomes a hostname before routing,
// and allowing the endpoint to resolve it with the global resolver would leak DNS.
func peerEgress(snap store.Snapshot, dns *option.DNSOptions, tunnels map[string]store.Tunnel, skipped map[string]bool) ([]option.Rule, error) {
	var routes []option.Rule
	seen := make(map[string]bool)
	for _, p := range snap.Peers {
		if p.EgressTunnelID == nil {
			continue
		}
		addr, err := netip.ParseAddr(p.Address)
		if err != nil || !addr.Is4() {
			return nil, fmt.Errorf("invalid whole-Internet client address")
		}
		source := []string{netip.PrefixFrom(addr, 32).String()}
		raw := option.RawDefaultRule{SourceIPCIDR: source}
		dnsRaw := option.RawDefaultDNSRule{SourceIPCIDR: source}
		id := *p.EgressTunnelID
		target, exists := tunnels[id]
		if !p.Enabled || !exists || !target.Enabled || skipped[id] {
			routes = append(routes, option.Rule{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultRule{RawDefaultRule: raw, RuleAction: option.RuleAction{Action: C.RuleActionTypeReject, RejectOptions: option.RejectActionOptions{Method: C.RuleActionRejectMethodDefault}}}})
			dns.Rules = append(dns.Rules, reject(dnsRaw))
			continue
		}
		tag := "peer-dns-" + id
		if !seen[id] {
			server, err := upstreamServer(snap.Settings)
			if err != nil {
				return nil, err
			}
			var remote *option.RemoteDNSServerOptions
			switch opts := server.Options.(type) {
			case *option.RemoteDNSServerOptions:
				remote = opts
			case *option.RemoteTLSDNSServerOptions:
				remote = &opts.RemoteDNSServerOptions
			case *option.RemoteHTTPSDNSServerOptions:
				remote = &opts.RemoteDNSServerOptions
			default:
				return nil, fmt.Errorf("unsupported peer DNS transport")
			}
			remote.Detour = TunnelTag(id)
			if remote.DomainResolver != nil {
				bootstrapTag := tag + "-bootstrap"
				remote.DomainResolver = &option.DomainResolveOptions{Server: bootstrapTag, Strategy: option.DomainStrategy(C.DomainStrategyIPv4Only)}
				dns.Servers = append(dns.Servers, option.DNSServerOptions{Type: C.DNSTypeUDP, Tag: bootstrapTag, Options: &option.RemoteDNSServerOptions{LocalDNSServerOptions: option.LocalDNSServerOptions{DialerOptions: option.DialerOptions{Detour: TunnelTag(id)}}, DNSServerAddressOptions: option.DNSServerAddressOptions{Server: bootstrapAddr}}})
			}
			server.Tag = tag
			dns.Servers = append(dns.Servers, server)
			seen[id] = true
		}
		dns.Rules = append(dns.Rules, option.DNSRule{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultDNSRule{RawDefaultDNSRule: dnsRaw, DNSRuleAction: option.DNSRuleAction{Action: C.RuleActionTypeRoute, RouteOptions: option.DNSRouteActionOptions{Server: tag, Strategy: option.DomainStrategy(C.DomainStrategyIPv4Only)}}}})
		blocked := option.RawDefaultRule{SourceIPCIDR: source, IPCIDR: ippolicy.LocalIPv4()}
		routes = append(routes,
			option.Rule{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultRule{RawDefaultRule: raw, RuleAction: option.RuleAction{Action: C.RuleActionTypeResolve, ResolveOptions: option.RouteActionResolve{Server: tag, Strategy: option.DomainStrategy(C.DomainStrategyIPv4Only)}}}},
			option.Rule{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultRule{RawDefaultRule: blocked, RuleAction: option.RuleAction{Action: C.RuleActionTypeReject, RejectOptions: option.RejectActionOptions{Method: C.RuleActionRejectMethodDefault}}}},
			option.Rule{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultRule{RawDefaultRule: raw, RuleAction: option.RuleAction{Action: C.RuleActionTypeRoute, RouteOptions: option.RouteActionOptions{Outbound: TunnelTag(id)}}}},
		)
	}
	return routes, nil
}
