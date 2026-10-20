//go:build linux && security215_baseline

package main

import (
	"testing"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// This exercise runs over the unmodified archived base, with only shared
// fixture files overlaid. A generic probe failure is never regression proof.
func peerWireExercise(t *testing.T) {
	p := newPeerWire(t)
	p.initial(t)
	wireMust(t, p.st.DeletePeer(p.h.ctx, p.a.ID))
	fresh, err := p.st.Snapshot(p.h.ctx)
	wireMust(t, err)
	if len(fresh.Rules) != 1 || fresh.Rules[0].PeerScope != store.ScopeAll || len(fresh.Rules[0].PeerIDs) != 0 || !fresh.Rules[0].Enabled {
		t.Fatal("baseline broadening prerequisite absent")
	}
	opts, err := singbox.Generate(fresh, plainLists(p.h.mgr))
	wireMust(t, err)
	if len(opts.Route.Rules) != 2 || len(opts.Route.Rules[1].DefaultOptions.SourceIPCIDR) != 0 {
		t.Fatal("baseline generated source was not broadened")
	}
	expectedAction := "route"
	if p.mode == "reject" {
		expectedAction = "reject"
	}
	if opts.Route.Rules[1].DefaultOptions.Action != expectedAction {
		t.Fatal("baseline route action does not match proof")
	}
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	want := "private"
	if p.mode == "reject" {
		want = "deny"
	}
	p.target(t, p.b.Address, want, "baseline-broadened-B")
	p.controls(t, "baseline-broadened-B")
	p.h.assertNoForbidden(t)
	t.Fatalf("215-BASELINE-PEER-BROADENING-OBSERVED case=%s: unrelated client inherited selected policy", p.mode)
}
