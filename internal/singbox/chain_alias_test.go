package singbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	"github.com/ArghTeam/razdacha/internal/store"
)

func hostnameChainFixture(t *testing.T) store.Snapshot {
	t.Helper()
	snap := chainFixture()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(snap.Tunnels[len(snap.Tunnels)-1].Parsed, &fields); err != nil {
		t.Fatal(err)
	}
	fields["peers"] = json.RawMessage(`[{"address":"relay.example","port":51820,"public_key":"cHVibGlj","allowed_ips":["0.0.0.0/0"]}]`)
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	snap.Tunnels[len(snap.Tunnels)-1].Parsed = body
	snap.Rules[0].ViaTunnelID = "wwww"
	return snap
}

func selectorAliases(opts option.Options) []option.Outbound {
	var result []option.Outbound
	for _, outbound := range opts.Outbounds {
		if outbound.Type == C.TypeSelector {
			result = append(result, outbound)
		}
	}
	return result
}

func TestHostnameChainUsesFixedParentWithoutPolicyChanges(t *testing.T) {
	snap := hostnameChainFixture(t)
	opts := mustGenerate(t, snap)
	aliases := selectorAliases(opts)
	if len(aliases) != 1 {
		t.Fatalf("got %d aliases", len(aliases))
	}
	alias := aliases[0]
	selector := alias.Options.(*option.SelectorOutboundOptions)
	parent := TunnelTag("aaaa")
	if !reflect.DeepEqual(selector.Outbounds, []string{parent}) || selector.Default != parent || selector.InterruptExistConnections {
		t.Fatalf("selector permits a changed exit: %+v", selector)
	}
	expected, err := chainEndpoint(snap.Tunnels[len(snap.Tunnels)-1], ChainTag("wwww", "aaaa"), parent)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, endpoint := range opts.Endpoints {
		if endpoint.Tag != expected.Tag {
			continue
		}
		found = true
		var before, after map[string]json.RawMessage
		if err := json.Unmarshal(expected.Options.(json.RawMessage), &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(endpoint.Options.(json.RawMessage), &after); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(alias.Tag)
		before["detour"] = raw
		if !reflect.DeepEqual(before, after) {
			t.Fatal("endpoint changed beyond its parent detour")
		}
	}
	if !found {
		t.Fatal("chain endpoint missing")
	}
	// The alias is transport wiring only: existing route/DNS/source constraints
	// are exactly the same as the corresponding literal-address chain.
	literal := chainFixture()
	literal.Rules[0].ViaTunnelID = "wwww"
	baseline := mustGenerate(t, literal)
	if !reflect.DeepEqual(opts.Route, baseline.Route) || !reflect.DeepEqual(opts.DNS, baseline.DNS) {
		t.Fatal("hostname alias changed route or DNS policy")
	}
	if !bytes.Equal(generate(t, snap), generate(t, snap)) {
		t.Fatal("generation is nondeterministic")
	}
}

func TestHostnameChainsDeduplicateAliasesByParent(t *testing.T) {
	snap := hostnameChainFixture(t)
	other := snap.Tunnels[len(snap.Tunnels)-1]
	other.ID = "second-warp"
	other.Name = "Second exit"
	snap.Tunnels = append(snap.Tunnels, other)
	snap.Rules[1].TunnelID = "aaaa"
	snap.Rules[1].ViaTunnelID = other.ID
	if count := len(selectorAliases(mustGenerate(t, snap))); count != 1 {
		t.Fatalf("same parent produced %d aliases", count)
	}
	otherParent := snap.Tunnels[0]
	otherParent.ID = "second-parent"
	snap.Tunnels = append(snap.Tunnels, otherParent)
	snap.Rules[1].TunnelID = otherParent.ID
	if count := len(selectorAliases(mustGenerate(t, snap))); count != 2 {
		t.Fatalf("distinct parents produced %d aliases", count)
	}
}

func TestWireGuardParentAliasCollisionRejected(t *testing.T) {
	parent := option.Endpoint{Type: C.TypeWireGuard, Tag: "parent", Options: json.RawMessage(`{"peers":[{"address":"192.0.2.1"}]}`)}
	child := option.Endpoint{Type: C.TypeWireGuard, Tag: "child", Options: json.RawMessage(`{"detour":"parent","peers":[{"address":"relay.example"}]}`)}
	for _, endpointCollision := range []bool{false, true} {
		t.Run(fmt.Sprint(endpointCollision), func(t *testing.T) {
			endpoints := []option.Endpoint{parent, child}
			var outbounds []option.Outbound
			if endpointCollision {
				endpoints = append(endpoints, option.Endpoint{Type: C.TypeWireGuard, Tag: wireGuardParentTag("parent"), Options: json.RawMessage(`{}`)})
			} else {
				outbounds = append(outbounds, option.Outbound{Type: C.TypeDirect, Tag: wireGuardParentTag("parent")})
			}
			if _, _, err := stabilizeWireGuardDetours(endpoints, outbounds); err == nil {
				t.Fatal("alias collision accepted")
			}
		})
	}
}

func TestHostnameChainUnavailableOrOrphanNeverAddsAlias(t *testing.T) {
	for _, mode := range []string{"first-disabled", "second-disabled", "selected-orphan", "selected-disabled", "empty-destination"} {
		t.Run(mode, func(t *testing.T) {
			snap := hostnameChainFixture(t)
			switch mode {
			case "first-disabled":
				snap.Tunnels[0].Enabled = false
			case "second-disabled":
				snap.Tunnels[len(snap.Tunnels)-1].Enabled = false
			case "selected-orphan":
				snap.Rules[0].PeerScope = store.ScopeSelected
				snap.Rules[0].PeerIDs = []string{"absent"}
			case "selected-disabled":
				snap.Rules[0].PeerScope = store.ScopeSelected
				snap.Rules[0].PeerIDs = []string{"p1"}
				for i := range snap.Peers {
					snap.Peers[i].Enabled = false
				}
			case "empty-destination":
				snap.Rules[0].Domains = nil
				snap.Rules[0].CommunityLists = nil
			}
			opts := mustGenerate(t, snap)
			if len(selectorAliases(opts)) != 0 {
				t.Fatal("unused or unavailable chain materialized an alias")
			}
			if mode == "first-disabled" || mode == "second-disabled" || mode == "selected-disabled" {
				assertRejected(t, opts, ruleSetTag("r1"))
			}
		})
	}
	snap := hostnameChainFixture(t)
	snap.Rules[0].ViaTunnelID = "missing"
	if _, err := Generate(snap, testPlain); err == nil {
		t.Fatal("missing chain accepted")
	}
}

func TestHostnameChainOverProxyAndLiteralChainKeepDirectDetour(t *testing.T) {
	snap := hostnameChainFixture(t)
	snap.Rules[0].TunnelID = "bbbb"
	if len(selectorAliases(mustGenerate(t, snap))) != 0 {
		t.Fatal("non-WireGuard parent received an alias")
	}
	literal := chainFixture()
	literal.Rules[0].ViaTunnelID = "wwww"
	if len(selectorAliases(mustGenerate(t, literal))) != 0 {
		t.Fatal("literal peer received an alias")
	}
}

func TestHostnameAliasDoesNotChangePoolMemberPruning(t *testing.T) {
	snap := hostnameChainFixture(t)
	snap.Tunnels = append(snap.Tunnels, store.Tunnel{
		ID: "pool", Name: "Synthetic pool", Type: store.TunnelVLESS, Source: store.SourcePool, Enabled: true,
		Pool: []store.PoolServer{{URL: "vless://00000000-0000-0000-0000-000000000000@192.0.2.1:443?security=tls&sni=relay.example"}},
	})
	opts := mustGenerate(t, snap)
	found := false
	for i, outbound := range opts.Outbounds {
		member, ok := rejectedPoolMember(opts, snap, fmt.Errorf("initialize outbound[%d]", i))
		if outbound.Type == C.TypeSelector && ok {
			t.Fatal("internal alias was treated as a rejected pool member")
		}
		if ok {
			found = true
			pruned := withoutPoolServer(snap, member.tunnelID, member.url)
			if len(selectorAliases(mustGenerate(t, pruned))) != 1 {
				t.Fatal("pool pruning changed the independent hostname chain")
			}
		}
	}
	if !found {
		t.Fatal("synthetic pool member not identified")
	}
}
