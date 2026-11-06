package singbox

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

func TestRealityDenialPlanSingleReadAndUnchangedDNS(t *testing.T) {
	snap, live := realitySniffFixture()
	snap.Rules[0].Action = store.ActionBlock
	snap.Rules[0].RemoteLists = []string{"https://fixture.example/plain"}
	reads := 0
	plain := func(string) (PlainList, bool) {
		reads++
		return PlainList{Domains: []string{"plain.fixture.example"}}, true
	}
	got, err := GenerateWithReality(snap, plain, live)
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("source reads=%d, want one", reads)
	}
	// The existing REALITY DNS conversion must remain byte-equivalent with/without
	// preflight. Remove only its private plan to exercise that same conversion.
	base, _, err := GeneratePolicyWithDiag(snap, plain)
	if err != nil {
		t.Fatal(err)
	}
	var expected []option.DNSRule
	expected = append(expected, reject(option.RawDefaultDNSRule{Inbound: []string{TagRealityIn}, QueryType: []option.DNSQueryType{28}}))
	for _, r := range base.DNS.Rules {
		raw := r.DefaultOptions.RawDefaultDNSRule
		if len(raw.SourceIPCIDR) == 0 && len(raw.RuleSet) == 0 {
			expected = append(expected, r)
			continue
		}
		original := r
		original.DefaultOptions.Inbound = []string{TagDNSIn, TagTProxyIn}
		expected = append(expected, original)
		alternative := r
		alternative.DefaultOptions.SourceIPCIDR = nil
		alternative.DefaultOptions.Inbound = []string{TagRealityIn}
		alternative.DefaultOptions.AuthUser = []string{store.RealityUserName("a")}
		expected = append(expected, alternative)
	}
	base.DNS.Rules = expected
	if !reflect.DeepEqual(got.DNS, base.DNS) {
		t.Fatal("ordinary DNS options changed")
	}
	pre, resolve := -1, -1
	for i, r := range got.Route.Rules {
		if r.Type == C.RuleTypeLogical && r.LogicalOptions.Action == C.RuleActionTypeReject && len(r.LogicalOptions.Rules) >= 4 {
			pre = i
		}
		if r.DefaultOptions.Action == C.RuleActionTypeResolve {
			resolve = i
			break
		}
	}
	if pre < 6 || pre >= resolve {
		t.Fatalf("preflight=%d resolve=%d", pre, resolve)
	}
	children := got.Route.Rules[pre].LogicalOptions.Rules
	if strings.Join(children[1].DefaultOptions.AuthUser, ",") != store.RealityUserName("a") {
		t.Fatal("scope changed")
	}
	if strings.Join(children[3].DefaultOptions.RuleSet, ",") != ruleSetTag("selected")+"-dns,"+plainListTag("selected", 0)+"-dns" {
		t.Fatal("projection mismatch")
	}
}

func TestRealityDenialNativeCapability(t *testing.T) {
	r := store.Rule{ID: "native", RemoteLists: []string{"https://fixture.example/a.json"}}
	for _, tc := range []struct {
		name                string
		complete            bool
		ordinary, transport []string
		want                bool
	}{
		{name: "unknown", want: true},
		{name: "complete-domain-only", complete: true},
		{name: "ordinary", complete: true, ordinary: []string{"203.0.113.0/24"}, want: true},
		{name: "transport", complete: true, transport: []string{"203.0.113.0/24"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := buildRuleSets(r, store.Settings{}, func(string) (PlainList, bool) {
				return PlainList{Native: &lists.NativeArtifact{DNSPath: "dns.json", Path: "original.json"}, Subnets: tc.ordinary, TransportSubnets: tc.transport, NativeSummaryComplete: tc.complete}, true
			})
			if b.AddressCapable != tc.want {
				t.Fatalf("capability=%v want=%v", b.AddressCapable, tc.want)
			}
		})
	}
}

func TestRealityDenialShadowOrderAndBudget(t *testing.T) {
	positive := compiledDestination{route: routeRule(store.Rule{Action: store.ActionDirect}, []string{"full"}, nil, "", false), dnsTags: []string{"domain"}}
	deny := compiledDestination{route: routeRule(store.Rule{Action: store.ActionBlock}, []string{"deny"}, nil, "", false), dnsTags: []string{"deny-dns"}}
	selected := func([]string) []string { return []string{"a", "b"} }
	rules, err := realityDenialPreflight([]compiledDestination{positive, deny, positive}, selected)
	if err != nil || len(rules) != 1 {
		t.Fatalf("%v: %+v", err, rules)
	}
	shadow := rules[0].LogicalOptions.Rules[4].LogicalOptions
	if !shadow.Invert || len(shadow.Rules) != 1 || shadow.Rules[0].Type != C.RuleTypeLogical || len(shadow.Rules[0].LogicalOptions.Rules[1].DefaultOptions.RuleSet) != 1 {
		t.Fatal("earlier domain shadow changed")
	}
	more := positive
	more.dnsTags = []string{"second-domain"}
	grouped, groupErr := realityDenialPreflight([]compiledDestination{positive, more, deny}, selected)
	if groupErr != nil {
		t.Fatal(groupErr)
	}
	groupedShadow := grouped[0].LogicalOptions.Rules[4].LogicalOptions.Rules
	if len(groupedShadow) != 1 || len(groupedShadow[0].LogicalOptions.Rules[1].DefaultOptions.RuleSet) != 2 {
		t.Fatal("identical auth scopes were not grouped")
	}
	positive.addressCapable = true
	rules, err = realityDenialPreflight([]compiledDestination{positive, deny}, selected)
	if err != nil || len(rules[0].LogicalOptions.Rules[4].LogicalOptions.Rules[0].DefaultOptions.AuthUser) != 2 {
		t.Fatal("address barrier omitted")
	}
	rules, err = realityDenialPreflight([]compiledDestination{deny, positive}, selected)
	if err != nil || len(rules[0].LogicalOptions.Rules) != 4 {
		t.Fatal("later allow shadows earlier deny")
	}
	huge := make([]string, realityDenialBudget)
	_, err = realityDenialPreflight([]compiledDestination{deny}, func([]string) []string { return huge })
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatal("budget must fail generation")
	}
	snap, live := activityScaleFixture()
	for i := range snap.Rules {
		if i%2 == 1 {
			snap.Rules[i].Action = store.ActionBlock
		}
	}
	if _, err = GenerateWithReality(snap, nil, live); err != nil {
		t.Fatalf("100 clients/rules: %v", err)
	}
}

func TestRealityDenialWholeClientPrecedenceAndOrphans(t *testing.T) {
	snap, live := realitySniffFixture()
	snap.Rules[0].Action = store.ActionBlock
	exit := "exit"
	snap.Peers[0].EgressTunnelID = &exit
	opts, err := GenerateWithReality(snap, nil, live)
	if err != nil {
		t.Fatal(err)
	}
	whole, preflight := -1, -1
	for i, r := range opts.Route.Rules {
		if r.Type == C.RuleTypeLogical && len(r.LogicalOptions.Rules) >= 4 {
			preflight = i
		}
		if r.DefaultOptions.Action == C.RuleActionTypeRoute && r.DefaultOptions.RouteOptions.Outbound == TunnelTag("exit") && len(r.DefaultOptions.RuleSet) == 0 && strings.Join(r.DefaultOptions.AuthUser, ",") == store.RealityUserName("a") {
			whole = i
		}
	}
	if whole < 0 || preflight <= whole {
		t.Fatalf("whole=%d preflight=%d", whole, preflight)
	}
	snap.Rules[0].PeerIDs = []string{"deleted"}
	opts, err = GenerateWithReality(snap, nil, live)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range opts.Route.Rules {
		if r.Type == C.RuleTypeLogical && len(r.LogicalOptions.Rules) >= 4 {
			t.Fatal("orphan produced preflight")
		}
	}
}
