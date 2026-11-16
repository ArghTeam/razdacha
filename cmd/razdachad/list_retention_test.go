package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestPublicationRetainsCommittedListsUntilAcceptedReplacement(t *testing.T) {
	ctx := context.Background()
	mgr, _, url := fixtureLists(t)
	if err := mgr.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	snap := store.Snapshot{Settings: store.DefaultSettings(), Rules: []store.Rule{{ID: "retained", Name: "Retained", Enabled: true, Action: store.ActionBlock, PeerScope: store.ScopeAll, RemoteLists: []string{url}}}}
	f := newPublicationFixture(t, mgr)
	if _, err := f.g.Apply(ctx, snap); err != nil {
		t.Fatal(err)
	}
	mgr.SetSources(nil)
	if _, ok := mgr.List(url); !ok {
		t.Fatal("pending removal evicted committed contents")
	}
	replacement := snap
	replacement.Rules = nil
	f.fail = "check"
	if _, err := f.g.Apply(ctx, replacement); err == nil {
		t.Fatal("fixture rejection expected")
	}
	if _, ok := mgr.List(url); !ok {
		t.Fatal("failed Apply released committed owner")
	}
	if _, ok := mgr.Snapshot()[url]; !ok {
		t.Fatal("failed Apply lost committed snapshot")
	}
	f.fail = ""
	if _, err := f.g.Apply(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if len(mgr.Snapshot()) != 0 || len(mgr.States()) != 0 {
		t.Fatal("accepted replacement retained retired plain source")
	}
	// Recovery pins durable sources even before a scheduler reconciliation.
	mgr.SetSources(lists.Sources(snap))
	if err := mgr.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.Apply(ctx, snap); err != nil {
		t.Fatal(err)
	}
	mgr.SetCommittedSources(nil)
	recovered := newPublicationFixture(t, mgr)
	recovered.g.network.path = f.g.network.path
	if err := recovered.g.recover(ctx, f.g.network.path+".absent-seed"); err != nil {
		t.Fatal(err)
	}
	mgr.SetSources(nil)
	if _, ok := mgr.List(url); !ok {
		t.Fatal("recovery did not pin committed source")
	}
}

func TestPublicationListOwnerFailureBoundaries(t *testing.T) {
	for _, at := range []string{"check", "guard-on", "wg", "route", "reload", "nft", "manifest", "bootstrap", "guard-off"} {
		t.Run(at, func(t *testing.T) {
			ctx := context.Background()
			mgr, _, url := fixtureLists(t)
			if err := mgr.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			snap := policy("192.0.2.0/24")
			snap.Rules[0].RemoteLists = []string{url}
			f := newPublicationFixture(t, mgr)
			if _, err := f.g.Apply(ctx, snap); err != nil {
				t.Fatal(err)
			}
			mgr.SetSources(nil)
			candidate := snap
			candidate.Rules = nil
			switch at {
			case "manifest":
				f.g.network.path = t.TempDir()
			case "bootstrap":
				f.g.network.bootstrapPath = t.TempDir()
				if err := os.WriteFile(filepath.Join(f.g.network.bootstrapPath, "keep"), []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				f.setFailure(at)
			}
			if _, err := f.g.Apply(ctx, candidate); err == nil {
				t.Fatal("injected failure succeeded")
			}
			_, retained := mgr.List(url)
			// Release failure follows durable authority assignment: retain that
			// newly accepted owner, never the obsolete policy being replaced.
			if retained != (at != "guard-off") {
				t.Fatalf("wrong committed owner after %s failure", at)
			}
		})
	}
}
