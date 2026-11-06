package singbox

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

func explainFixture() store.Snapshot {
	return store.Snapshot{Settings: store.DefaultSettings(), Peers: []store.Peer{{ID: "client", Name: "Client", Address: "10.8.0.2", Enabled: true}, {ID: "other", Address: "10.8.0.3", Enabled: true}}, Tunnels: []store.Tunnel{{ID: "exit", Name: "Exit", Type: store.TunnelSOCKS, Source: store.SourceURL, Enabled: true, Parsed: json.RawMessage(`{"server":"192.0.2.1","server_port":1080,"version":"5"}`)}}}
}

func explainRule(id string, action store.RuleAction) store.Rule {
	return store.Rule{ID: id, Name: id, Enabled: true, Action: action, TunnelID: "exit", Domains: []string{"example.com"}, PeerScope: store.ScopeAll}
}

func explainFor(t *testing.T, s store.Snapshot, plain PlainLists) PolicyExplanation {
	t.Helper()
	out, err := ExplainPolicy(s, plain, ExplainRequest{PeerID: "client", Transport: "wg", Domain: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExplainSuffixUsesPinnedMatcher(t *testing.T) {
	for _, tc := range []struct {
		suffix, host string
		want         bool
	}{{"example.com", "example.com", true}, {"example.com", "a.example.com", true}, {"example.com", "badexample.com", false}, {"example.com", "example.com.evil", false}, {".example.com", "example.com", false}, {".example.com", "a.example.com", true}, {"*.example.com", "a.example.com", false}, {"EXAMPLE.com", "example.com", false}} {
		t.Run(tc.suffix+"_"+tc.host, func(t *testing.T) {
			if got := suffixMatch(tc.host, tc.suffix); got != tc.want {
				t.Fatalf("match=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestExplainOrderingScopeAndCapturedDNSC1(t *testing.T) {
	s := explainFixture()
	first := explainRule("first", store.ActionDirect)
	second := explainRule("second", store.ActionBlock)
	second.Priority = 1
	s.Rules = []store.Rule{first, second}
	out := explainFor(t, s, nil)
	if out.Routing.Action != "direct" || out.Routing.RuleID != "first" || out.GatewayA.Action != "reject" || out.GatewayA.RuleID != "second" {
		t.Fatalf("C1: %+v", out)
	}
	first.PeerScope = store.ScopeSelected
	first.PeerIDs = []string{"other"}
	s.Rules[0] = first
	out = explainFor(t, s, nil)
	if out.Routing.Action != "reject" || out.Routing.RuleID != "second" {
		t.Fatalf("scope: %+v", out)
	}
	first.PeerIDs = []string{"client"}
	s.Rules[0] = first
	second.Action = store.ActionTunnel
	s.Rules[1] = second
	out = explainFor(t, s, nil)
	if out.Routing.Action != "direct" || out.GatewayA.Action != "fakeip" {
		t.Fatalf("C1 FakeIP: %+v", out)
	}
}

func TestExplainUnresolvedCandidatesAndDomainOR(t *testing.T) {
	for _, native := range []bool{false, true} {
		s := explainFixture()
		early := explainRule("early", store.ActionDirect)
		early.Domains = nil
		var plain PlainLists
		if native {
			early.RemoteLists = []string{"https://private.example/list.json"}
			plain = testPlain
		} else {
			early.Subnets = []string{"203.0.113.0/24"}
		}
		later := explainRule("later", store.ActionBlock)
		later.Priority = 1
		s.Rules = []store.Rule{early, later}
		out := explainFor(t, s, plain)
		if out.Routing.State != "unknown" || len(out.Candidates) != 2 || out.Candidates[0].RuleID != "early" || out.Candidates[1].RuleID != "later" {
			t.Fatalf("uncertainty: %+v", out)
		}
		early.Domains = []string{"example.com"}
		s.Rules[0] = early
		out = explainFor(t, s, plain)
		if out.Routing.Action != "direct" || out.Routing.State != "conditional" {
			t.Fatalf("OR: %+v", out)
		}
	}
}

func TestExplainWholeExitAndUnavailableTargets(t *testing.T) {
	s := explainFixture()
	s.Rules = []store.Rule{explainRule("block", store.ActionBlock)}
	id := "exit"
	s.Peers[0].EgressTunnelID = &id
	out := explainFor(t, s, nil)
	if out.Routing.Action != "tunnel" || out.GatewayA.Action != "same_exit_upstream" || out.Routing.RuleID != "" || out.Routing.TunnelName != "Exit" || out.GatewayA.TunnelName != "Exit" {
		t.Fatalf("whole exit: %+v", out)
	}
	s.Tunnels[0].Enabled = false
	out = explainFor(t, s, nil)
	if out.Routing.Action != "reject" || out.GatewayA.Action != "reject" {
		t.Fatalf("disabled: %+v", out)
	}
	s.Peers[0].EgressTunnelID = nil
	s.Rules[0] = explainRule("unavailable", store.ActionTunnel)
	out = explainFor(t, s, nil)
	if out.Routing.Action != "reject" || out.GatewayA.Action != "reject" {
		t.Fatalf("unavailable: %+v", out)
	}
	s.Settings.EndpointHost = "example.com"
	out, err := ExplainPolicy(s, nil, ExplainRequest{PeerID: "client", Transport: "reality", Domain: "example.com"})
	if err != nil || out.Routing.Reason != "gateway_domain_guard" {
		t.Fatalf("gateway: %+v %v", out, err)
	}
}

func TestExplainSingleFrozenReadAndConfigParity(t *testing.T) {
	s := fixture()
	before, _, err := GeneratePolicyWithDiag(s, testPlain)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	expected := map[string]int{}
	for _, r := range s.Rules {
		if !r.Enabled {
			continue
		}
		for _, key := range r.CommunityLists {
			expected["https://github.com/itdoginfo/allow-domains/releases/latest/download/"+key+".srs"]++
		}
		for _, url := range r.RemoteLists {
			expected[url]++
		}
	}
	var plan []compiledDestination
	after, _, err := generatePolicyWithPlan(s, func(url string) (PlainList, bool) { counts[url]++; return testPlain(url) }, &plan)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := Marshal(before)
	b, _ := Marshal(after)
	if !bytes.Equal(a, b) {
		t.Fatal("evidence changed generated config bytes")
	}
	for url, n := range counts {
		if n != expected[url] {
			t.Fatalf("source read %d times: %s", n, url)
		}
	}
	s = explainFixture()
	r := explainRule("plain", store.ActionBlock)
	r.Domains = nil
	r.RemoteLists = []string{"https://secret.example/source"}
	s.Rules = []store.Rule{r}
	out := explainFor(t, s, func(string) (PlainList, bool) { return PlainList{Domains: []string{"example.com"}}, true })
	if out.Routing.Action != "reject" {
		t.Fatal(out)
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "secret.example") {
		t.Fatal("source URL leaked")
	}
}

func TestExplainDomainValidation(t *testing.T) {
	for _, bad := range []string{"", "https://example.com", "example.com/path", "example.com:443", "192.0.2.1", "::1", "192.0.2.0/24", "*.example.com", " example.com", "a..example", "-a.example"} {
		if _, err := NormalizeExplainDomain(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if got, err := NormalizeExplainDomain("WWW.Example.com."); err != nil || got != "www.example.com" {
		t.Fatalf("normalize %q %v", got, err)
	}
}

func TestExplainDoesNotLogPrivateSources(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	s := explainFixture()
	r := explainRule("empty", store.ActionBlock)
	r.Domains = nil
	r.RemoteLists = []string{"https://user:private-password@example.com/source"}
	s.Rules = []store.Rule{r}
	_, err := ExplainPolicy(s, func(string) (PlainList, bool) { return PlainList{}, false }, ExplainRequest{PeerID: "client", Transport: "wg", Domain: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Fatalf("compiler emitted diagnostic log: %s", logs.String())
	}
}

func TestExplainEmptyPoolAndDisabledChainReject(t *testing.T) {
	for _, kind := range []string{"empty_pool", "disabled_chain"} {
		t.Run(kind, func(t *testing.T) {
			s := explainFixture()
			r := explainRule("selected", store.ActionTunnel)
			if kind == "empty_pool" {
				s.Tunnels[0].Source = store.SourcePool
				s.Tunnels[0].Type = store.TunnelVLESS
				s.Tunnels[0].Parsed = nil
			} else {
				r.ViaTunnelID = "warp"
				s.Tunnels = append(s.Tunnels, store.Tunnel{ID: "warp", Type: store.TunnelWireGuard, Source: store.SourceWARP, Enabled: false})
			}
			s.Rules = []store.Rule{r}
			out := explainFor(t, s, nil)
			if out.Routing.Action != "reject" || out.GatewayA.Action != "reject" {
				t.Fatal(out)
			}
		})
	}
}

func TestExplainGeneratedDNSGuardsPrecedeWholeExit(t *testing.T) {
	s := explainFixture()
	id := "exit"
	s.Peers[0].EgressTunnelID = &id
	out, err := ExplainPolicy(s, nil, ExplainRequest{PeerID: "client", Transport: "wg", Domain: "use-application-dns.net"})
	if err != nil || out.Routing.Action != "tunnel" || out.GatewayA.Action != "reject" {
		t.Fatalf("generated DNS guard %+v %v", out, err)
	}
}

func TestExplainNativeDNSRemainsUnknown(t *testing.T) {
	s := explainFixture()
	first := explainRule("native", store.ActionBlock)
	first.Domains = nil
	first.CommunityLists = []string{"youtube"}
	later := explainRule("known", store.ActionBlock)
	later.Priority = 1
	s.Rules = []store.Rule{first, later}
	out := explainFor(t, s, testPlain)
	if out.Routing.State != "unknown" || out.GatewayA.State != "unknown" {
		t.Fatal(out)
	}
}

func TestExplainAvailableChainUsesCommittedNames(t *testing.T) {
	s := explainFixture()
	warp := chainFixture().Tunnels
	target := warp[len(warp)-1]
	target.Name = "Committed WARP"
	s.Tunnels = append(s.Tunnels, target)
	r := explainRule("chain", store.ActionTunnel)
	r.ViaTunnelID = target.ID
	s.Rules = []store.Rule{r}
	out := explainFor(t, s, nil)
	if out.Routing.Action != "tunnel" || out.Routing.TunnelID != "exit" || out.Routing.TunnelName != "Exit" || out.Routing.ViaTunnelID != target.ID || out.Routing.ViaTunnelName != "Committed WARP" {
		t.Fatal(out)
	}
}
