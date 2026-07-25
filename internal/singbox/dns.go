package singbox

import (
	"fmt"
	"net/netip"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/ArghTeam/razdacha/internal/store"
)

// buildDNS собирает секцию dns без правил конкретных наборов — их дописывает
// [Generate] по мере обхода правил.
//
// strategy = ipv4_only и FakeIP только в inet4_range — первый слой отключения
// IPv6 (ADR 0005). AAAA не отдаются никогда, поэтому приложения не пытаются идти
// по v6 ещё до всякого firewall.
func buildDNS(s store.Settings) (*option.DNSOptions, error) {
	upstream, err := upstreamServer(s)
	if err != nil {
		return nil, err
	}

	fakeIP := fakeIPRange
	return &option.DNSOptions{
		RawDNSOptions: option.RawDNSOptions{
			Servers: []option.DNSServerOptions{
				upstream,
				{
					Type: C.DNSTypeUDP,
					Tag:  TagDNSBootstrap,
					Options: &option.RemoteDNSServerOptions{
						DNSServerAddressOptions: option.DNSServerAddressOptions{Server: bootstrapAddr},
					},
				},
				{
					Type:    C.DNSTypeFakeIP,
					Tag:     TagDNSFakeIP,
					Options: &option.FakeIPDNSServerOptions{Inet4Range: (*badoption.Prefix)(&fakeIP)},
				},
			},
			Rules: []option.DNSRule{
				// HTTPS-записи (тип 65) несут ECH и подсказки HTTP/3 в обход FakeIP.
				reject(option.RawDefaultDNSRule{
					QueryType: badoption.Listable[option.DNSQueryType]{option.DNSQueryType(65)},
				}),
				// Сигнал, которым Firefox выключает собственный DoH.
				reject(option.RawDefaultDNSRule{
					DomainSuffix: badoption.Listable[string]{"use-application-dns.net"},
				}),
			},
			Final: TagDNSUpstream,
			DNSClientOptions: option.DNSClientOptions{
				Strategy:         option.DomainStrategy(C.DomainStrategyIPv4Only),
				IndependentCache: true,
			},
		},
	}, nil
}

// upstreamServer собирает апстрим-резолвер из настроек. Когда адрес задан доменом
// (DoT/DoH), его резолвит bootstrap — иначе резолв замкнулся бы сам на себя.
func upstreamServer(s store.Settings) (option.DNSServerOptions, error) {
	host, port, err := hostPort(s.DNSUpstream)
	if err != nil {
		return option.DNSServerOptions{}, err
	}

	remote := option.RemoteDNSServerOptions{
		DNSServerAddressOptions: option.DNSServerAddressOptions{Server: host, ServerPort: port},
	}
	if _, err := netip.ParseAddr(host); err != nil {
		remote.DialerOptions.DomainResolver = &option.DomainResolveOptions{Server: TagDNSBootstrap}
	}

	out := option.DNSServerOptions{Tag: TagDNSUpstream}
	switch s.DNSType {
	case "udp":
		out.Type, out.Options = C.DNSTypeUDP, &remote
	case "dot":
		out.Type = C.DNSTypeTLS
		out.Options = &option.RemoteTLSDNSServerOptions{RemoteDNSServerOptions: remote}
	case "doh":
		out.Type = C.DNSTypeHTTPS
		out.Options = &option.RemoteHTTPSDNSServerOptions{
			RemoteTLSDNSServerOptions: option.RemoteTLSDNSServerOptions{RemoteDNSServerOptions: remote},
		}
	default:
		return option.DNSServerOptions{}, fmt.Errorf(
			"%w: неизвестный тип DNS %q", store.ErrInvalid, s.DNSType)
	}
	return out, nil
}

// reject — DNS-правило, отвечающее отказом.
func reject(raw option.RawDefaultDNSRule) option.DNSRule {
	return option.DNSRule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: raw,
			DNSRuleAction: option.DNSRuleAction{
				Action:        C.RuleActionTypeReject,
				RejectOptions: option.RejectActionOptions{Method: C.RuleActionRejectMethodDefault},
			},
		},
	}
}
