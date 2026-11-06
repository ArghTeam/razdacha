package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestRealityInventoryMatchesDeliveryForHundredClients(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	yes := true
	for i := 1; i < 100; i++ {
		p, err := api.CreatePeer(ctx, f.st, fmt.Sprintf("Client %d", i), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.g.PatchPeer(ctx, f.st, p.ID, store.PeerPatch{RealityEnabled: &yes}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"available", "pending", "invalid-endpoint", "uncertain"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "pending" {
				if _, err := f.g.RotateReality(ctx, f.st, f.p.ID, f.p.RealityRevision); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "invalid-endpoint" {
				cfg, _ := f.st.Settings(ctx)
				cfg.EndpointHost = ""
				if err := f.g.SaveSettings(ctx, f.st, cfg); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "uncertain" {
				f.g.network.uncertain = true
			}
			peers, err := f.st.Peers(ctx)
			if err != nil {
				t.Fatal(err)
			}
			inventory := f.g.RealityInventory(ctx, f.st, peers)
			if len(inventory) != 100 {
				t.Fatal("incomplete inventory")
			}
			for _, p := range peers {
				profile, err := f.g.RealityProfile(ctx, f.st, p.ID)
				entry := inventory[p.ID]
				if entry.Downloadable != (err == nil) {
					t.Fatal("eligibility differs from delivery", phase, p.ID, err)
				}
				if err == nil && (entry.Status != profile.State || entry.Revision != profile.Revision) {
					t.Fatal("delivery metadata differs from inventory")
				}
			}
		})
	}
}

func TestRealityInventoryRefusesStaleRevisionAndGuardedDelivery(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	stale := f.p
	if _, err := f.g.RotateReality(ctx, f.st, f.p.ID, f.p.RealityRevision); err != nil {
		t.Fatal(err)
	}
	e := f.g.RealityInventory(ctx, f.st, []store.Peer{stale})[stale.ID]
	if e.Status == "available" || e.Downloadable {
		t.Fatal("stale inventory revision retained authority")
	}
	f.g.network.uncertain = true
	p, _ := f.st.Peer(ctx, stale.ID)
	e = f.g.RealityInventory(ctx, f.st, []store.Peer{p})[p.ID]
	if e.Status != "guarded" || e.Downloadable {
		t.Fatal("uncertain inventory permitted delivery")
	}
}
