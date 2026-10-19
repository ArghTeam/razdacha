package singbox

import (
	"reflect"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
	C "github.com/sagernet/sing-box/constant"
)

func TestDNSProjectionSingleSourceRead(t *testing.T) {
	calls := 0
	plain := func(string) (PlainList, bool) {
		calls++
		return PlainList{Domains: []string{"selected.example"}, Subnets: []string{"192.0.2.0/24"}}, true
	}
	r := store.Rule{ID: "synthetic", RemoteLists: []string{"https://example.invalid/plain"}, Domains: []string{"manual.example"}, Subnets: []string{"203.0.113.0/24"}}
	got := buildRuleSets(r, store.DefaultSettings(), plain)
	if calls != 1 {
		t.Fatalf("source read %d times; want one immutable input", calls)
	}
	if !reflect.DeepEqual(got.RouteTags, []string{"rule-synthetic", "rule-synthetic-plain-0"}) || !reflect.DeepEqual(got.DNSTags, []string{"rule-synthetic-dns", "rule-synthetic-plain-0-dns"}) {
		t.Fatalf("unexpected projections: %+v", got)
	}
	for _, set := range got.Sets {
		if contains(got.DNSTags, set.Tag) {
			if len(set.InlineOptions.Rules) != 1 || len(set.InlineOptions.Rules[0].DefaultOptions.DomainSuffix) != 1 || len(set.InlineOptions.Rules[0].DefaultOptions.IPCIDR) != 0 {
				t.Fatalf("DNS set contains non-domain conditions: %+v", set)
			}
		} else if len(set.InlineOptions.Rules) != 2 {
			t.Fatalf("full route conditions lost: %+v", set)
		}
	}
}

func TestDNSProjectionEmptyAndMissing(t *testing.T) {
	for _, name := range []string{"cidr", "empty", "missing"} {
		t.Run(name, func(t *testing.T) {
			r := store.Rule{ID: "synthetic", RemoteLists: []string{"https://example.invalid/plain"}}
			built := buildRuleSets(r, store.DefaultSettings(), func(string) (PlainList, bool) {
				if name == "cidr" {
					return PlainList{Subnets: []string{"192.0.2.0/24"}}, true
				}
				return PlainList{}, name != "missing"
			})
			if len(built.DNSTags) != 0 {
				t.Fatalf("empty DNS projection has tags: %+v", built)
			}
			if name == "cidr" {
				if len(built.RouteTags) != 1 || len(built.Missing) != 0 {
					t.Fatalf("CIDR route disappeared: %+v", built)
				}
			} else if len(built.RouteTags) != 0 || len(built.Missing) != 1 {
				t.Fatalf("empty/missing source diagnostics lost: %+v", built)
			}
			for _, action := range []store.RuleAction{store.ActionBlock, store.ActionTunnel, store.ActionDirect} {
				r.Action = action
				if _, ok := dnsRuleFor(r, built.DNSTags, []string{"10.8.0.2/32"}, true); ok {
					t.Fatal("empty DNS tags produced unrestricted action")
				}
			}
		})
	}
}

func TestDNSProjectionRefreshAndRestart(t *testing.T) {
	r := store.Rule{ID: "synthetic", RemoteLists: []string{"https://example.invalid/plain"}}
	current := PlainList{Domains: []string{"old.example"}, Subnets: []string{"192.0.2.0/24"}}
	plain := func(string) (PlainList, bool) { return current, true }
	old := buildRuleSets(r, store.DefaultSettings(), plain)
	current = PlainList{Subnets: []string{"203.0.113.0/24"}}
	refreshed := buildRuleSets(r, store.DefaultSettings(), plain)
	restarted := buildRuleSets(r, store.DefaultSettings(), plain)
	if len(old.DNSTags) != 1 || len(refreshed.DNSTags) != 0 || len(refreshed.RouteTags) != 1 || !reflect.DeepEqual(refreshed, restarted) {
		t.Fatalf("refresh/restart projections inconsistent: old=%+v refreshed=%+v restarted=%+v", old, refreshed, restarted)
	}
	if old.Sets[1].InlineOptions.Rules[0].DefaultOptions.DomainSuffix[0] != "old.example" {
		t.Fatal("later generation mutated earlier projection")
	}
}

func TestDNSProjectionNativeSourcesAreLocal(t *testing.T) {
	r := store.Rule{ID: "synthetic", CommunityLists: []string{"synthetic"}, RemoteLists: []string{"https://example.invalid/native.srs", "https://example.invalid/native.json"}}
	built := buildRuleSets(r, store.DefaultSettings(), testPlain)
	if built.Err != nil || len(built.Sets) != 6 || len(built.DNSTags) != 3 {
		t.Fatalf("native projections: %+v", built)
	}
	for _, set := range built.Sets {
		if set.Type != C.RuleSetTypeLocal || set.RemoteOptions.URL != "" || set.LocalOptions.Path == "" {
			t.Fatalf("autonomous source: %+v", set)
		}
	}
	missing := buildRuleSets(r, store.DefaultSettings(), nil)
	if missing.Err == nil {
		t.Fatal("missing native source silently accepted")
	}
}
