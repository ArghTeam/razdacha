package singbox

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
	C "github.com/sagernet/sing-box/constant"
)

func peerExitSnapshot() store.Snapshot {
	id := "exit"
	return store.Snapshot{Settings: store.DefaultSettings(), Tunnels: []store.Tunnel{{ID: id, Name: "Synthetic exit", Type: store.TunnelSOCKS, Source: store.SourceURL, Raw: "synthetic", Parsed: json.RawMessage(`{"server":"127.0.0.1","server_port":19080,"version":"5"}`), Enabled: true}}, Peers: []store.Peer{{ID: "a", Address: "10.8.0.2", Enabled: true, EgressTunnelID: &id}, {ID: "b", Address: "10.8.0.3", Enabled: true}}, Rules: []store.Rule{{ID: "ordinary", Name: "Shared block", Enabled: true, Action: store.ActionBlock, PeerScope: store.ScopeAll, Domains: []string{"blocked.example"}}}}
}

func TestPeerEgressPrecedesSharedRoutesAndDNS(t *testing.T) {
	snap := peerExitSnapshot()
	opts, err := Generate(snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Route.Rules[0].DefaultOptions.Action != C.RuleActionTypeHijackDNS {
		t.Fatal("DNS hijack displaced")
	}
	resolve, route, ordinary := opts.Route.Rules[1].DefaultOptions, opts.Route.Rules[3].DefaultOptions, opts.Route.Rules[4].DefaultOptions
	if resolve.Action != C.RuleActionTypeResolve || resolve.ResolveOptions.Server != "peer-dns-exit" || resolve.ResolveOptions.Strategy.String() != "ipv4_only" {
		t.Fatal("stale FakeIP resolution is not explicit same-exit IPv4")
	}
	if (len(resolve.SourceIPCIDR) != 1 || resolve.SourceIPCIDR[0] != "10.8.0.2/32") || !reflect.DeepEqual(route.SourceIPCIDR, resolve.SourceIPCIDR) || route.RouteOptions.Outbound != "tun-exit" || ordinary.Action != C.RuleActionTypeReject || len(ordinary.SourceIPCIDR) != 0 {
		t.Fatal("peer override changed source or ordinary policy")
	}
	if opts.DNS.Rules[0].DefaultOptions.Action != C.RuleActionTypeReject || opts.DNS.Rules[1].DefaultOptions.Action != C.RuleActionTypeReject {
		t.Fatal("DNS safety guards displaced")
	}
	peer := opts.DNS.Rules[2].DefaultOptions
	if peer.RouteOptions.Server != "peer-dns-exit" || !reflect.DeepEqual(peer.SourceIPCIDR, resolve.SourceIPCIDR) {
		t.Fatal("DNS is not source-scoped before ordinary policy")
	}
	data, err := Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"detour": "tun-exit"`) {
		t.Fatal("peer DNS lacks detour")
	}
	snap.Peers[0].EgressTunnelID = nil
	normal, err := Generate(snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(normal.Route.Rules) != 2 || len(normal.DNS.Rules) != 3 || len(normal.DNS.Servers) != 3 {
		t.Fatal("switching to shared rules retained peer override")
	}
}

func TestPeerEgressUnavailableRetainsSourceRejection(t *testing.T) {
	for _, state := range []string{"disabled", "missing", "empty-pool", "disabled-peer"} {
		t.Run(state, func(t *testing.T) {
			snap := peerExitSnapshot()
			switch state {
			case "disabled":
				snap.Tunnels[0].Enabled = false
			case "missing":
				snap.Tunnels = nil
			case "empty-pool":
				snap.Tunnels[0].Source = store.SourcePool
				snap.Tunnels[0].Parsed = nil
				snap.Tunnels[0].Pool = nil
			case "disabled-peer":
				snap.Peers[0].Enabled = false
			}
			opts, err := Generate(snap, nil)
			if err != nil {
				t.Fatal(err)
			}
			route, dns := opts.Route.Rules[1].DefaultOptions, opts.DNS.Rules[2].DefaultOptions
			if route.Action != C.RuleActionTypeReject || dns.Action != C.RuleActionTypeReject || len(route.SourceIPCIDR) != 1 || route.SourceIPCIDR[0] != "10.8.0.2/32" || len(dns.SourceIPCIDR) != 1 {
				t.Fatal("unavailable target broadened or disappeared")
			}
			if len(opts.DNS.Servers) != 3 {
				t.Fatal("unavailable exit created DNS transport")
			}
		})
	}
}

func TestPeerEgressHostnameBootstrapAndRuntimeCheck(t *testing.T) {
	for _, transport := range []string{"udp", "dot", "doh"} {
		t.Run(transport, func(t *testing.T) {
			snap := peerExitSnapshot()
			snap.Settings.DNSType = transport
			snap.Settings.DNSUpstream = "resolver.example"
			opts, err := Generate(snap, nil)
			if err != nil {
				t.Fatal(err)
			}
			data, err := Marshal(opts)
			if err != nil {
				t.Fatal(err)
			}
			var raw struct {
				DNS struct {
					Servers []map[string]any `json:"servers"`
				} `json:"dns"`
			}
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			found := map[string]map[string]any{}
			for _, s := range raw.DNS.Servers {
				tag, _ := s["tag"].(string)
				found[tag] = s
			}
			upstream, bootstrap := found["peer-dns-exit"], found["peer-dns-exit-bootstrap"]
			if upstream["detour"] != "tun-exit" || bootstrap["detour"] != "tun-exit" || bootstrap["server"] != "1.1.1.1" {
				t.Fatal("hostname upstream bootstrap can escape selected exit")
			}
			resolver, ok := upstream["domain_resolver"].(map[string]any)
			if !ok || resolver["server"] != "peer-dns-exit-bootstrap" {
				t.Fatal("upstream uses global bootstrap")
			}
			bin := os.Getenv("RAZDACHA_TEST_SINGBOX")
			if bin == "" {
				t.Log("Pinned runtime check not requested; set RAZDACHA_TEST_SINGBOX")
				return
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "check", "-c", path)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("pinned runtime rejected peer DNS/resolve config: %v %s", err, out)
			}
		})
	}
}
