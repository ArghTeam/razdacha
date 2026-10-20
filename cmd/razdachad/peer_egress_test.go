package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestPeerEgressPendingRefreshRecoveryAndReservation(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	target, err := st.CreateTunnel(ctx, store.Tunnel{Name: "Synthetic exit", Type: store.TunnelSOCKS, Source: store.SourceURL, Raw: "synthetic", Parsed: []byte(`{"server":"127.0.0.1","server_port":19080,"version":"5"}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := api.CreatePeer(ctx, st, "Client A", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.CreatePeer(ctx, st, "Client B", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f := newPublicationFixture(t, nil)
	checkDirty := func(want bool) {
		t.Helper()
		got, err := f.g.Pending(ctx, st)
		if err != nil || got != want {
			t.Fatalf("pending=%v want=%v err=%v", got, want, err)
		}
	}
	checkDirty(true)
	if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	checkDirty(false)
	original := f.g.base
	peer.EgressTunnelID = &target.ID
	if err = st.UpdatePeer(ctx, peer); err != nil {
		t.Fatal(err)
	}
	checkDirty(true)
	fresh, err := st.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.g.refreshLists(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.g.applyPools(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	f.guarded = true
	f.g.network.uncertain = true
	if err = f.g.recover(ctx, filepath.Join(filepath.Dir(f.g.network.path), "absent-seed")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.g.base.Peers, original.Peers) {
		t.Fatal("pending peer target escaped through refresh/recovery")
	}
	checkDirty(true)
	// Reverting to committed policy clears dirty without a publication.
	peer.EgressTunnelID = nil
	if err = st.UpdatePeer(ctx, peer); err != nil {
		t.Fatal(err)
	}
	checkDirty(false)
	peer.EgressTunnelID = &target.ID
	if err = st.UpdatePeer(ctx, peer); err != nil {
		t.Fatal(err)
	}
	if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	checkDirty(false)
	if !hasPeerEgress(f.published.Peers, peer.ID) {
		t.Fatal("nft publication did not receive override snapshot")
	}
	f.g.network.uncertain = true
	checkDirty(true)
	f.g.network.uncertain = false
	if err = st.DeletePeer(ctx, peer.ID); err != nil {
		t.Fatal(err)
	}
	checkDirty(true)
	if _, err = f.g.refreshLists(ctx); err != nil {
		t.Fatal(err)
	}
	if !hasPeerEgress(f.g.base.Peers, peer.ID) {
		t.Fatal("deleted committed source protection lost before Apply")
	}
	reserved, err := f.g.CreatePeer(ctx, st, "Client C", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if reserved.Address == peer.Address {
		t.Fatal("committed override address reused")
	}
	f.guarded = true
	f.g.network.uncertain = true
	if err = f.g.recover(ctx, filepath.Join(filepath.Dir(f.g.network.path), "absent-seed")); err != nil {
		t.Fatal(err)
	}
	live, err := st.Peers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range live {
		if p.ID == peer.ID {
			t.Fatal("recovery restored deleted live identity")
		}
	}
	if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	checkDirty(false)
	reused, err := f.g.CreatePeer(ctx, st, "Client D", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if reused.Address != peer.Address || reused.EgressTunnelID != nil {
		t.Fatal("applied deleted source inherited override or remained reserved")
	}
}

func hasPeerEgress(peers []store.Peer, id string) bool {
	for _, p := range peers {
		if p.ID == id {
			return p.EgressTunnelID != nil
		}
	}
	return false
}
