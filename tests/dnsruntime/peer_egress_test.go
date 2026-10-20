package dnsruntime

import (
	"context"
	"io"
	"net/netip"
	"testing"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	runtimeRule "github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"
)

// The pinned matcher sees an FQDN and DestinationAddresses after actionResolve,
// not a replaced literal destination. Exercise its real CIDR matching semantics.
func TestPeerEgressRejectMatchesResolvedPrivateAddresses(t *testing.T) {
	target := "exit"
	snap := store.Snapshot{Settings: store.DefaultSettings(), Peers: []store.Peer{{ID: "a", Address: "10.8.0.2", Enabled: true, EgressTunnelID: &target}}, Tunnels: []store.Tunnel{{ID: target, Name: "Synthetic", Type: store.TunnelSOCKS, Source: store.SourceURL, Raw: "synthetic", Parsed: []byte(`{"server":"127.0.0.1","server_port":19080,"version":"5"}`), Enabled: true}}}
	opts, err := singbox.Generate(snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	generated := opts.Route.Rules[2]
	if generated.DefaultOptions.Action != C.RuleActionTypeReject {
		t.Fatal("missing post-resolve reject")
	}
	ctx := context.Background()
	factory, err := log.New(log.Options{Context: ctx, Options: option.LogOptions{Level: "error"}, DefaultWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := runtimeRule.NewRule(ctx, factory.NewLogger("peer-egress"), generated, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close() })
	for _, tc := range []struct {
		name, source string
		addresses    []string
		want         bool
	}{
		{"private", "10.8.0.2", []string{"10.20.0.1"}, true},
		{"loopback", "10.8.0.2", []string{"127.0.0.1"}, true},
		{"link-local", "10.8.0.2", []string{"169.254.169.254"}, true},
		{"mixed answer", "10.8.0.2", []string{"203.0.113.20", "192.168.1.1"}, true},
		{"public", "10.8.0.2", []string{"203.0.113.20"}, false},
		{"other peer", "10.8.0.3", []string{"10.20.0.1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := &adapter.InboundContext{Source: M.Socksaddr{Addr: netip.MustParseAddr(tc.source)}, Destination: M.Socksaddr{Fqdn: "cached.example", Port: 443}}
			for _, addr := range tc.addresses {
				metadata.DestinationAddresses = append(metadata.DestinationAddresses, netip.MustParseAddr(addr))
			}
			if got := compiled.Match(metadata); got != tc.want {
				t.Fatalf("pinned post-resolve matcher=%v want=%v", got, tc.want)
			}
		})
	}
}
