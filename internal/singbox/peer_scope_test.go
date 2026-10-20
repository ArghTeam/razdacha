package singbox

import (
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

func TestOrphanSelectedRulesCannotBecomeGlobalActions(t *testing.T) {
	for _, action := range []store.RuleAction{store.ActionBlock, store.ActionTunnel, store.ActionDirect} {
		for _, ids := range [][]string{nil, {}, {"missing"}} {
			t.Run(string(action)+"/"+string(rune(len(ids)+'0')), func(t *testing.T) {
				snap := store.Snapshot{Settings: store.DefaultSettings(), Rules: []store.Rule{{ID: "orphan", Name: "Orphan", Enabled: true, Action: action, TunnelID: "missing-tunnel", ViaTunnelID: "missing-chain", PeerScope: store.ScopeSelected, PeerIDs: ids, Domains: []string{"private.example"}, RemoteLists: []string{"https://example.invalid/unsupported.srs"}}}}
				calls := 0
				opts, diag, err := GenerateWithDiag(snap, func(string) (PlainList, bool) {
					calls++
					t.Error("orphan source was loaded")
					return PlainList{}, false
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(opts.Route.Rules) != 1 || len(opts.Route.RuleSet) != 0 || calls != 0 {
					t.Fatal("orphan generated routing or list artifacts")
				}
				if len(diag) != 1 || !diag[0].Skipped || diag[0].SkipReason == "" {
					t.Fatalf("missing orphan diagnostic: %+v", diag)
				}
			})
		}
	}
}

func TestExistingDisabledSelectedSourcesRetainScopedReject(t *testing.T) {
	snap := store.Snapshot{Settings: store.DefaultSettings(), Peers: []store.Peer{{ID: "disabled", Address: "10.8.0.2", Enabled: false}}, Rules: []store.Rule{{ID: "private", Name: "Private", Enabled: true, Action: store.ActionDirect, PeerScope: store.ScopeSelected, PeerIDs: []string{"dangling", "disabled"}, Domains: []string{"private.example"}, Subnets: []string{"203.0.113.10/32"}}}}
	opts, diag, err := GenerateWithDiag(snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diag) != 1 || diag[0].Skipped || diag[0].Reason == "" {
		t.Fatalf("disabled source lost protection: %+v", diag)
	}
	rule := opts.Route.Rules[1].DefaultOptions
	if rule.Action != "reject" || len(rule.SourceIPCIDR) != 1 || rule.SourceIPCIDR[0] != "10.8.0.2/32" {
		t.Fatalf("reject broadened: %+v", rule)
	}
	for _, r := range opts.DNS.Rules {
		if len(r.DefaultOptions.SourceIPCIDR) > 0 && r.DefaultOptions.SourceIPCIDR[0] != "10.8.0.2/32" {
			t.Fatal("DNS source broadened")
		}
	}
}
