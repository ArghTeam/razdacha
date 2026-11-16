package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

const explainSnapshotLimit = 4 << 20

type explainSnapshot struct {
	Policy store.Snapshot
	Lists  map[string]lists.List
}

// explainEligibility reads live metadata, never REALITY UUIDs/private server keys.
// Historical policy cannot resurrect deleted, disabled or rotated access.
func (g *applyGate) explainEligibility(ctx context.Context, st *store.Store, req singbox.ExplainRequest) (string, error) {
	live, err := st.RouteEligibility(ctx, req.PeerID)
	if err != nil {
		return "", err
	}
	p := live.Peer
	if !p.Enabled || req.Transport == "wg" && p.WireGuardDisabled || req.Transport == "reality" && (!p.RealityEnabled || !live.Settings.RealityEnabled) {
		return "transport_disabled", nil
	}
	if g.network == nil || !g.network.initialized || g.network.uncertain {
		return "authority_unavailable", nil
	}
	var committed store.Peer
	for _, c := range g.base.Peers {
		if c.ID == p.ID {
			committed = c
			break
		}
	}
	if committed.ID == "" || !committed.Enabled {
		return "client_not_applied", nil
	}
	if req.Transport == "wg" {
		if committed.WireGuardDisabled || committed.Address != p.Address || committed.PublicKey != p.PublicKey {
			return "transport_not_admitted", nil
		}
	} else {
		if g.publicGuarded() {
			return "ingress_guarded", nil
		}
		a, ok := live.Credential, live.HasCredential
		if !g.base.Settings.RealityEnabled || live.IdentityID != g.base.RealityIdentityID || !ok || a.CredentialID != p.RealityCredentialID || a.Revision != p.RealityRevision || !indexRealityPolicy(g.base).admitted(p) {
			return "transport_not_admitted", nil
		}
	}
	return "", nil
}

func (g *applyGate) ExplainRoute(ctx context.Context, st *store.Store, req singbox.ExplainRequest) (singbox.PolicyExplanation, error) {
	out := singbox.UnknownExplanation(req, "authority_unavailable")
	g.mu.Lock()
	reason, err := g.explainEligibility(ctx, st, req)
	if err != nil || reason != "" {
		g.mu.Unlock()
		if reason != "" {
			out = singbox.UnknownExplanation(req, reason)
			if reason == "transport_disabled" || reason == "transport_not_admitted" || reason == "client_not_applied" {
				out.State = "disabled"
			}
		}
		return out, err
	}
	if !explainAuthorityWithinBudget(explainSnapshot{g.base, g.network.contents}) {
		g.mu.Unlock()
		return singbox.UnknownExplanation(req, "construction_budget"), nil
	}
	// Private committed state remains internal. Copy immutable authority while
	// locked; expensive compiler work and all response serialization are outside.
	data, err := json.Marshal(explainSnapshot{g.base, g.network.contents})
	epoch := g.activityEpoch
	g.mu.Unlock()
	if err != nil {
		return out, fmt.Errorf("copy explanation authority")
	}
	if len(data) > explainSnapshotLimit {
		return singbox.UnknownExplanation(req, "construction_budget"), nil
	}
	var frozen explainSnapshot
	if err = json.Unmarshal(data, &frozen); err != nil {
		return out, fmt.Errorf("decode explanation authority: %w", err)
	}
	// Bound expansion before entering the generator, including nested list items.
	items := len(frozen.Policy.Peers) + len(frozen.Policy.Rules) + len(frozen.Policy.Tunnels)
	for _, r := range frozen.Policy.Rules {
		items += len(r.Domains) + len(r.Subnets) + len(r.RemoteLists) + len(r.CommunityLists)
	}
	for _, l := range frozen.Lists {
		items += len(l.Domains) + len(l.Subnets) + len(l.TransportSubnets)
	}
	if items > 100000 || len(frozen.Policy.Rules) > 1000 || len(frozen.Policy.Peers) > 1000 {
		return singbox.UnknownExplanation(req, "construction_budget"), nil
	}
	plain := frozenPlainLists(frozen.Lists)
	exhausted := false
	boundedPlain := func(url string) (singbox.PlainList, bool) {
		l, ok := plain(url)
		items += len(l.Domains) + len(l.Subnets) + len(l.TransportSubnets)
		if items > 100000 {
			exhausted = true
			return singbox.PlainList{}, false
		}
		return l, ok
	}
	compile := g.explainCompile
	if compile == nil {
		compile = singbox.ExplainPolicy
	}
	out, err = compile(frozen.Policy, boundedPlain, req)
	if exhausted {
		return singbox.UnknownExplanation(req, "construction_budget"), nil
	}
	if err != nil {
		return singbox.UnknownExplanation(req, "compiler_unavailable"), nil
	}
	applied, err := json.Marshal(frozen.Policy)
	if err != nil {
		return singbox.UnknownExplanation(req, "policy_encoding_failed"), nil
	}
	sum := sha256.Sum256(data)
	out.Marker = hex.EncodeToString(sum[:])
	g.mu.Lock()
	defer g.mu.Unlock()
	reason, err = g.explainEligibility(ctx, st, req)
	if err != nil {
		return singbox.UnknownExplanation(req, "eligibility_changed"), err
	}
	if reason != "" || epoch != g.activityEpoch {
		return singbox.UnknownExplanation(req, "authority_changed"), nil
	}
	// Pending metadata may change independently of publication while compiling.
	current, err := st.Snapshot(ctx)
	if err != nil {
		return singbox.UnknownExplanation(req, "pending_read_failed"), nil
	}
	if !explainAuthorityWithinBudget(current) {
		return singbox.UnknownExplanation(req, "pending_construction_budget"), nil
	}
	currentBytes, err := json.Marshal(current)
	if err != nil {
		return singbox.UnknownExplanation(req, "pending_read_failed"), nil
	}
	out.Pending = !bytes.Equal(applied, currentBytes)
	out.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return out, nil
}

// Bound traversal and retained string/byte content before cloning under the gate.
// Count internal private fields without serializing them into any public result.
func explainAuthorityWithinBudget(value any) bool {
	budget := 1_000_000
	var visit func(reflect.Value) bool
	visit = func(v reflect.Value) bool {
		budget--
		if budget < 0 {
			return false
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if !v.IsNil() {
				return visit(v.Elem())
			}
		case reflect.String:
			budget -= v.Len()
		case reflect.Slice, reflect.Array:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				budget -= v.Len()
				break
			}
			for i := 0; i < v.Len(); i++ {
				if !visit(v.Index(i)) {
					return false
				}
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if !visit(v.Field(i)) {
					return false
				}
			}
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				if !visit(iter.Key()) || !visit(iter.Value()) {
					return false
				}
			}
		}
		return budget >= 0
	}
	return visit(reflect.ValueOf(value))
}
