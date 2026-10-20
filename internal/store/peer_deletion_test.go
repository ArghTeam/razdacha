package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestPeerDeletionPreservesPolicyAndRejectsStaleSelection(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			ctx := context.Background()
			s := open(t)
			a, err := s.CreatePeer(ctx, samplePeer("ClientA", "10.8.0.2"))
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.CreatePeer(ctx, samplePeer("ClientB", "10.8.0.3"))
			if err != nil {
				t.Fatal(err)
			}
			original, err := s.CreateRule(ctx, Rule{Name: "Private policy", Action: ActionBlock, Enabled: enabled, PeerScope: ScopeSelected, PeerIDs: []string{a.ID, a.ID}, Domains: []string{"private.example"}, Subnets: []string{"203.0.113.10/32"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(original.PeerIDs) != 1 {
				t.Fatal("duplicate selection not normalized")
			}
			partial, err := s.CreateRule(ctx, Rule{Name: "Shared selection", Action: ActionDirect, Enabled: true, PeerScope: ScopeSelected, PeerIDs: []string{a.ID, b.ID}, Domains: []string{"shared.example"}})
			if err != nil {
				t.Fatal(err)
			}
			unrelated, err := s.CreateRule(ctx, Rule{Name: "Public control", Action: ActionDirect, Enabled: true, PeerScope: ScopeAll, Domains: []string{"control.example"}})
			if err != nil {
				t.Fatal(err)
			}
			original, _ = s.Rule(ctx, original.ID)
			unrelated, _ = s.Rule(ctx, unrelated.ID)
			effects, err := s.DeletePeerWithEffects(ctx, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(effects.AffectedRuleIDs, []string{original.ID, partial.ID}) || !reflect.DeepEqual(effects.InactivatedRuleIDs, []string{original.ID}) {
				t.Fatalf("effects: %+v", effects)
			}
			got, err := s.Rule(ctx, original.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := original
			expected.Enabled = false
			expected.PeerIDs = []string{}
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("policy changed beyond selection/activation: %+v", got)
			}
			shared, err := s.Rule(ctx, partial.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !shared.Enabled || shared.PeerScope != ScopeSelected || !reflect.DeepEqual(shared.PeerIDs, []string{b.ID}) {
				t.Fatalf("surviving selection: %+v", shared)
			}
			control, _ := s.Rule(ctx, unrelated.ID)
			if !reflect.DeepEqual(control, unrelated) {
				t.Fatal("unrelated rule changed")
			}
			if err := s.UpdateRule(ctx, original); !errors.Is(err, ErrInvalid) {
				t.Fatalf("stale write accepted: %v", err)
			}
			unchanged, err := s.Rule(ctx, got.ID)
			if err != nil || !reflect.DeepEqual(unchanged, got) {
				t.Fatal("rejected stale update changed stored policy")
			}
			before, err := s.Rules(ctx)
			if err != nil {
				t.Fatal(err)
			}
			original.ID = ""
			if _, err := s.CreateRule(ctx, original); !errors.Is(err, ErrInvalid) {
				t.Fatalf("missing peer create accepted: %v", err)
			}
			after, err := s.Rules(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected create changed policy or priorities")
			}
			if err := s.UpdateRule(ctx, got); err != nil {
				t.Fatal(err)
			}
			got.Enabled = true
			if err := s.UpdateRule(ctx, got); !errors.Is(err, ErrInvalid) {
				t.Fatalf("empty enabled selection accepted: %v", err)
			}
			got.Enabled = false
			got.PeerIDs = []string{b.ID}
			if err := s.UpdateRule(ctx, got); err != nil {
				t.Fatal(err)
			}
			assigned, _ := s.Rule(ctx, got.ID)
			if assigned.Enabled {
				t.Fatal("reassignment implicitly activated rule")
			}
			assigned.Enabled = true
			if err := s.UpdateRule(ctx, assigned); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPeerDeletionRollsBackPeerAndAllRuleChanges(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p, err := s.CreatePeer(ctx, samplePeer("ClientA", "10.8.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRule(ctx, Rule{Name: "Retained policy", Action: ActionBlock, Enabled: true, PeerScope: ScopeSelected, PeerIDs: []string{p.ID}, Domains: []string{"private.example"}})
	if err != nil {
		t.Fatal(err)
	}
	p, _ = s.Peer(ctx, p.ID)
	r, _ = s.Rule(ctx, r.ID)
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_rule_change BEFORE UPDATE ON rules BEGIN SELECT RAISE(ABORT, 'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	effects, err := s.DeletePeerWithEffects(ctx, p.ID)
	if err == nil {
		t.Fatal("expected transaction failure")
	}
	if len(effects.AffectedRuleIDs) != 0 || len(effects.InactivatedRuleIDs) != 0 {
		t.Fatal("uncommitted effects escaped")
	}
	current, err := s.Peer(ctx, p.ID)
	if err != nil || !reflect.DeepEqual(current, p) {
		t.Fatal("peer deletion was not rolled back")
	}
	policy, err := s.Rule(ctx, r.ID)
	if err != nil || !reflect.DeepEqual(policy, r) {
		t.Fatal("rule deletion was not rolled back")
	}
}

func TestDeletionNormalizesLegacySelectionsWithoutBroadening(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	a, err := s.CreatePeer(ctx, samplePeer("ClientA", "10.8.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreatePeer(ctx, samplePeer("ClientB", "10.8.0.3"))
	if err != nil {
		t.Fatal(err)
	}
	b.Enabled = false
	if err := s.UpdatePeer(ctx, b); err != nil {
		t.Fatal(err)
	}
	tunnel, err := s.CreateTunnel(ctx, sampleTunnel("Private first hop"))
	if err != nil {
		t.Fatal(err)
	}
	via, err := s.CreateTunnel(ctx, sampleTunnel("Private second hop"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRule(ctx, Rule{Name: "Legacy selection", Action: ActionTunnel, TunnelID: tunnel.ID, ViaTunnelID: via.ID, Enabled: true, PeerScope: ScopeSelected, PeerIDs: []string{a.ID}, Domains: []string{"private.example"}, CommunityLists: []string{"synthetic"}, RemoteLists: []string{"https://example.invalid/fixture.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := s.CreateRule(ctx, Rule{Name: "Dangling legacy rule", Action: ActionBlock, Enabled: true, PeerScope: ScopeSelected, PeerIDs: []string{a.ID}, Domains: []string{"orphan.example"}})
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := jsonList([]string{a.ID, "missing", b.ID, b.ID})
	if _, err := s.db.ExecContext(ctx, `UPDATE rules SET peer_ids=? WHERE id=?`, ids, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE rules SET peer_ids='["missing"]' WHERE id=?`, orphan.ID); err != nil {
		t.Fatal(err)
	}
	effects, err := s.DeletePeerWithEffects(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Rule(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || !reflect.DeepEqual(got.PeerIDs, []string{b.ID}) || got.PeerScope != ScopeSelected || got.TunnelID != tunnel.ID || got.ViaTunnelID != via.ID || !reflect.DeepEqual(got.RemoteLists, r.RemoteLists) || !reflect.DeepEqual(got.CommunityLists, r.CommunityLists) {
		t.Fatalf("legacy survivor changed: %+v", got)
	}
	gotOrphan, _ := s.Rule(ctx, orphan.ID)
	if gotOrphan.Enabled || gotOrphan.PeerScope != ScopeSelected || len(gotOrphan.PeerIDs) != 0 {
		t.Fatal("all dangling selection was not safely normalized")
	}
	if !reflect.DeepEqual(effects.InactivatedRuleIDs, []string{orphan.ID}) {
		t.Fatalf("missing legacy effect: %+v", effects)
	}
	// Orphan the remaining disabled client: references and destination policy survive.
	if err := s.DeletePeer(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	final, _ := s.Rule(ctx, r.ID)
	got.Enabled = false
	got.PeerIDs = []string{}
	if !reflect.DeepEqual(final, got) {
		t.Fatal("orphaning changed tunnel/chain/list references")
	}
}
