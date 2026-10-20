//go:build linux && !security215_baseline

package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/store"
)

func peerWireExercise(t *testing.T) {
	p := newPeerWire(t)
	p.initial(t)
	wireMust(t, p.st.DeletePeer(p.h.ctx, p.a.ID))
	orphan, err := p.st.Rule(p.h.ctx, p.rule.ID)
	wireMust(t, err)
	if orphan.Enabled || orphan.PeerScope != store.ScopeSelected || len(orphan.PeerIDs) != 0 {
		t.Fatal("deleted selection broadened")
	}
	check := func(stage string, committed bool) {
		p.target(t, p.b.Address, "direct", stage)
		p.controls(t, stage)
		if committed {
			if len(p.gate.base.Rules) != 1 || p.gate.base.Rules[0].PeerScope != store.ScopeSelected || len(p.gate.base.Rules[0].PeerIDs) != 1 || p.gate.base.Rules[0].PeerIDs[0] != p.a.ID {
				t.Fatal("automatic publication spliced pending orphan into authority")
			}
		}
		p.h.assertNoForbidden(t)
		t.Logf("215-FIXED-PEER-STAGE-PASS case=%s stage=%s", p.mode, stage)
	}
	check("deleted", true)
	fresh, err := p.st.Snapshot(p.h.ctx)
	wireMust(t, err)
	_, err = p.gate.refreshLists(p.h.ctx)
	wireMust(t, err)
	check("list-refresh", true)
	_, err = p.gate.applyPools(p.h.ctx, fresh)
	wireMust(t, err)
	check("pool-refresh", true)
	recover := func() {
		wireMust(t, netstack.SetClientGuard(true))
		p.gate.network.uncertain = true
		wireMust(t, p.gate.recover(p.h.ctx, filepath.Join(p.dir, "absent-seed")))
	}
	recover()
	check("committed-recovery", true)
	reserved, err := p.gate.CreatePeer(p.h.ctx, p.st, "ClientC", time.Now())
	wireMust(t, err)
	if reserved.Address == p.a.Address {
		t.Fatal("committed private address reused before Apply")
	}
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	check("explicit-apply", false)
	recover()
	check("applied-recovery", false)
	reused, err := p.gate.CreatePeer(p.h.ctx, p.st, "ClientD", time.Now())
	wireMust(t, err)
	if reused.Address != p.a.Address {
		t.Fatal("old address not released after explicit Apply")
	}
	// The source adapter still has A's former address: use it as the newly
	// allocated D identity and prove it inherits no orphaned policy.
	p.target(t, reused.Address, "direct", "reused-address")
	live, err := p.st.Peers(p.h.ctx)
	wireMust(t, err)
	for _, peer := range live {
		if peer.ID == p.a.ID {
			t.Fatal("recovery revived deleted identity")
		}
	}
	orphan.PeerIDs = []string{p.b.ID}
	wireMust(t, p.st.UpdateRule(p.h.ctx, orphan))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	check("reassigned-disabled", false)
	orphan.Enabled = true
	wireMust(t, p.st.UpdateRule(p.h.ctx, orphan))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	want := "private"
	if p.mode == "reject" {
		want = "deny"
	}
	p.target(t, p.b.Address, want, "explicit-activation")
	p.controls(t, "explicit-activation")
	p.h.assertNoForbidden(t)
	t.Logf("215-FIXED-PEER-WIRE-PASS case=%s", p.mode)
}
