package dnsruntime

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"testing"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	runtimeDNS "github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	runtimeRule "github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
)

type setRouter struct {
	adapter.Router
	sets map[string]adapter.RuleSet
}

func (r setRouter) RuleSet(tag string) (adapter.RuleSet, bool) { s, ok := r.sets[tag]; return s, ok }

type transports struct {
	adapter.DNSTransportManager
	upstream, fake adapter.DNSTransport
}

func (m transports) Transport(tag string) (adapter.DNSTransport, bool) {
	if tag == singbox.TagDNSFakeIP {
		return m.fake, true
	}
	return m.upstream, true
}
func (m transports) Default() adapter.DNSTransport { return m.upstream }

type transport struct{ tag, addr string }

func (t transport) Type() string {
	if t.tag == singbox.TagDNSFakeIP {
		return C.DNSTypeFakeIP
	}
	return C.DNSTypeUDP
}
func (t transport) Tag() string                    { return t.tag }
func (t transport) Dependencies() []string         { return nil }
func (t transport) Start(adapter.StartStage) error { return nil }
func (t transport) Close() error                   { return nil }
func (t transport) Exchange(ctx context.Context, q *dns.Msg) (*dns.Msg, error) {
	if t.tag == singbox.TagDNSFakeIP {
		r := new(dns.Msg)
		r.SetReply(q)
		r.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("198.18.0.1")}}
		return r, nil
	}
	r, _, err := (&dns.Client{}).ExchangeContext(ctx, q, t.addr)
	return r, err
}

// Exercise generated rules through the pinned DNS router and loopback UDP.
// Synthetic FakeIP transport avoids persistent state and external services.
func TestGeneratedDNSRuntime(t *testing.T) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{PacketConn: conn, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		r := new(dns.Msg)
		r.SetReply(q)
		if q.Question[0].Qtype == dns.TypeA {
			r.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("203.0.113.8")}}
		}
		_ = w.WriteMsg(r)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })
	for _, source := range []string{"manual", "plain"} {
		for _, shape := range []string{"cidr", "domain", "mixed", "empty", "missing"} {
			for _, mode := range []string{"block", "block-real", "unavailable", "unavailable-real", "fakeip", "real", "direct"} {
				t.Run(source+"/"+shape+"/"+mode, func(t *testing.T) {
					policy := store.Rule{ID: "synthetic", Name: "Synthetic policy", Enabled: true, Action: store.ActionBlock, PeerScope: store.ScopeSelected, PeerIDs: []string{"selected"}}
					content := singbox.PlainList{}
					if shape == "domain" || shape == "mixed" {
						content.Domains = []string{"selected.example"}
					}
					if shape == "cidr" || shape == "mixed" {
						content.Subnets = []string{"192.0.2.0/24"}
					}
					var plain singbox.PlainLists
					if source == "manual" {
						policy.Domains = content.Domains
						policy.Subnets = content.Subnets
					} else {
						policy.RemoteLists = []string{"https://example.invalid/list"}
						plain = func(string) (singbox.PlainList, bool) { return content, shape != "missing" }
					}
					tunnel := store.Tunnel{ID: "tunnel", Name: "Synthetic tunnel", Type: store.TunnelTrojan, Source: store.SourceURL, Enabled: true, Parsed: json.RawMessage(`{"server":"192.0.2.9","server_port":443,"password":"synthetic"}`)}
					switch mode {
					case "block-real":
						policy.ResolveRealIP = true
					case "direct":
						policy.Action = store.ActionDirect
					case "unavailable", "unavailable-real", "fakeip", "real":
						policy.Action = store.ActionTunnel
						policy.TunnelID = tunnel.ID
						tunnel.Enabled = mode == "fakeip" || mode == "real"
						policy.ResolveRealIP = mode == "real" || mode == "unavailable-real"
					}
					opts, err := singbox.Generate(store.Snapshot{Settings: store.DefaultSettings(), Rules: []store.Rule{policy}, Tunnels: []store.Tunnel{tunnel}, Peers: []store.Peer{{ID: "selected", Address: "10.8.0.2", Enabled: true}}}, plain)
					if err != nil {
						t.Fatal(err)
					}
					ctx := context.Background()
					factory, err := log.New(log.Options{Context: ctx, Options: option.LogOptions{Level: "error"}, DefaultWriter: io.Discard})
					if err != nil {
						t.Fatal(err)
					}
					sets := map[string]adapter.RuleSet{}
					for _, o := range opts.Route.RuleSet {
						set, err := runtimeRule.NewLocalRuleSet(ctx, factory.NewLogger("sets"), o)
						if err != nil {
							t.Fatal(err)
						}
						sets[o.Tag] = set
						t.Cleanup(func() { _ = set.Close() })
					}
					ctx = service.ContextWith[adapter.Router](ctx, setRouter{sets: sets})
					ctx = service.ContextWith[adapter.DNSTransportManager](ctx, transports{upstream: transport{tag: "upstream", addr: conn.LocalAddr().String()}, fake: transport{tag: singbox.TagDNSFakeIP}})
					router := runtimeDNS.NewRouter(ctx, factory, *opts.DNS)
					if err = router.Initialize(opts.DNS.Rules); err != nil {
						t.Fatal(err)
					}
					if err = router.Start(adapter.StartStateStart); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = router.Close() })
					for _, peer := range []string{"10.8.0.2", "10.8.0.3"} {
						for _, name := range []string{"selected.example", "unrelated.example"} {
							q := new(dns.Msg)
							q.SetQuestion(name+".", dns.TypeA)
							queryctx := adapter.WithContext(ctx, &adapter.InboundContext{Source: M.Socksaddr{Addr: netip.MustParseAddr(peer)}})
							answer, err := router.Exchange(queryctx, q, adapter.DNSQueryOptions{DisableCache: true})
							if err != nil {
								t.Fatal(err)
							}
							selected := len(content.Domains) > 0 && peer == "10.8.0.2" && name == "selected.example"
							reject := selected && (mode == "block" || mode == "block-real" || mode == "unavailable" || mode == "unavailable-real")
							if reject {
								if answer.Rcode != dns.RcodeRefused {
									t.Fatalf("selected name was not rejected: %s", answer)
								}
								continue
							}
							if answer.Rcode != dns.RcodeSuccess || len(answer.Answer) != 1 {
								t.Fatalf("permitted DNS must resolve: peer=%s name=%s rcode=%d answers=%d", peer, name, answer.Rcode, len(answer.Answer))
							}
							got := answer.Answer[0].(*dns.A).A.String()
							want := "203.0.113.8"
							if selected && mode == "fakeip" {
								want = "198.18.0.1"
							}
							if got != want {
								t.Fatalf("answer=%s want=%s", got, want)
							}
						}
					}
				})
			}
		}
	}
}
