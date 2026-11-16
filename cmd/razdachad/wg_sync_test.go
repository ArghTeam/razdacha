package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/store"
)

type wgReaderFunc func(context.Context) ([]store.Peer, error)

func (f wgReaderFunc) Peers(ctx context.Context) ([]store.Peer, error) { return f(ctx) }

// Model the live admitted key table independently of retained routing policy.
// Filtering matches the production syncer's enabled WG identity contract.
type liveWGTable struct {
	mu   sync.Mutex
	keys map[string]bool
	fail error
}

func (w *liveWGTable) SyncPeers(_ context.Context, peers []store.Peer) (netstack.WGPeerChanges, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fail != nil {
		return netstack.WGPeerChanges{}, w.fail
	}
	w.keys = make(map[string]bool)
	for _, p := range peers {
		if p.Enabled && !p.WireGuardDisabled {
			w.keys[p.PublicKey] = true
		}
	}
	return netstack.WGPeerChanges{}, nil
}
func (w *liveWGTable) has(key string) bool { w.mu.Lock(); defer w.mu.Unlock(); return w.keys[key] }

func wireLiveWGTable(f *publicationFixture, reader wgPeerReader, w wgPeerSyncer) {
	configure := f.g.network.wg
	f.g.network.wg = func(ctx context.Context, settings store.Settings) error {
		if err := configure(ctx, settings); err != nil {
			return err
		}
		return syncLiveWGPeers(ctx, reader, w)
	}
}

func TestWGPublicationRevokesDeletedLiveKeyBeforeSourceAndGuardRelease(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	a, err := api.CreatePeer(ctx, st, "Revoked WG", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f := newPublicationFixture(t, nil)
	w := &liveWGTable{}
	wireLiveWGTable(f, st, w)
	if _, err := f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	if !w.has(a.PublicKey) {
		t.Fatal("initial peer was not admitted")
	}
	if _, err := f.g.DeletePeer(ctx, st, a.ID); err != nil {
		t.Fatal(err)
	}
	if !w.has(a.PublicKey) {
		t.Fatal("pending deletion unexpectedly changed kernel admission")
	}
	oldNft := f.g.network.nft
	f.g.network.nft = func(ctx context.Context, snap store.Snapshot, lists map[string]lists.List) (int, error) {
		if w.has(a.PublicKey) {
			t.Error("deleted key still admitted when source policy is replaced")
		}
		return oldNft(ctx, snap, lists)
	}
	oldGuard := f.g.network.guard
	f.g.network.guard = func(on bool) error {
		if !on && w.has(a.PublicKey) {
			t.Error("deleted key still admitted at ingress release")
		}
		return oldGuard(on)
	}
	if _, err := f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	if w.has(a.PublicKey) || f.isGuarded() {
		t.Fatal("successful publication did not revoke and reopen")
	}
	b, err := f.g.CreatePeer(ctx, st, "Replacement WG", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if b.Address != a.Address {
		t.Fatal("safe publication did not release old address")
	}
}

func TestWGPublicationReadAndSyncFailuresRetainGuardAndReservation(t *testing.T) {
	for _, stage := range []string{"read", "sync"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			st, _ := openStore(t)
			a, err := api.CreatePeer(ctx, st, "Revoked WG", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			f := newPublicationFixture(t, nil)
			w := &liveWGTable{}
			failed := false
			read := wgReaderFunc(func(ctx context.Context) ([]store.Peer, error) {
				if failed && stage == "read" {
					return nil, errors.New("synthetic live read failure")
				}
				return st.Peers(ctx)
			})
			wireLiveWGTable(f, read, w)
			if _, err := f.g.ApplyCurrent(ctx, st); err != nil {
				t.Fatal(err)
			}
			if _, err := f.g.DeletePeer(ctx, st, a.ID); err != nil {
				t.Fatal(err)
			}
			failed = true
			if stage == "sync" {
				w.fail = errors.New("synthetic kernel sync failure")
			}
			if _, err := f.g.ApplyCurrent(ctx, st); err == nil {
				t.Fatal("publication accepted failed authentication reconciliation")
			}
			if !f.isGuarded() || !f.g.network.uncertain {
				t.Fatal("failed reconciliation reopened ingress")
			}
			if !w.has(a.PublicKey) {
				t.Fatal("failure did not preserve synthetic stale-key case")
			}
			b, err := f.g.CreatePeer(ctx, st, "Still reserved", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if b.Address == a.Address {
				t.Fatal("failed reconciliation released committed address")
			}
			failed = false
			w.fail = nil
			if _, err := f.g.ApplyCurrent(ctx, st); err != nil {
				t.Fatal(err)
			}
			if w.has(a.PublicKey) || f.isGuarded() {
				t.Fatal("retry failed to revoke and release")
			}
		})
	}
}

func TestWGPeriodicSnapshotCannotCrossDeletionAndPublication(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	a, err := api.CreatePeer(ctx, st, "Stale periodic WG", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f := newPublicationFixture(t, nil)
	w := &liveWGTable{}
	wireLiveWGTable(f, st, w)
	if _, err := f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	stale := wgReaderFunc(func(ctx context.Context) ([]store.Peer, error) {
		peers, err := st.Peers(ctx)
		close(entered)
		<-resume
		return peers, err
	})
	periodic := make(chan error, 1)
	go func() { periodic <- f.g.syncWGPeers(ctx, stale, w) }()
	<-entered
	// The whole read/write interval owns the gate, rather than just the write.
	if f.g.mu.TryLock() {
		f.g.mu.Unlock()
		close(resume)
		t.Fatal("periodic snapshot captured outside publication gate")
	}
	published := make(chan error, 1)
	go func() {
		if _, err := f.g.DeletePeer(ctx, st, a.ID); err != nil {
			published <- err
			return
		}
		_, err := f.g.ApplyCurrent(ctx, st)
		published <- err
	}()
	select {
	case err := <-published:
		close(resume)
		t.Fatalf("publication crossed paused periodic read: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(resume)
	if err := <-periodic; err != nil {
		t.Fatal(err)
	}
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	if w.has(a.PublicKey) || f.isGuarded() {
		t.Fatal("stale periodic authentication survived publication")
	}
	if err := f.g.syncWGPeers(ctx, st, w); err != nil {
		t.Fatal(err)
	}
	if w.has(a.PublicKey) {
		t.Fatal("subsequent periodic sync restored deleted identity")
	}
}

func TestWGRecoveryNeverReplaysCommittedAuthentication(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	a, err := api.CreatePeer(ctx, st, "Committed only WG", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f := newPublicationFixture(t, nil)
	w := &liveWGTable{}
	wireLiveWGTable(f, st, w)
	if _, err := f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.DeletePeer(ctx, st, a.ID); err != nil {
		t.Fatal(err)
	}
	recovery := newPublicationFixture(t, nil)
	recovery.g.network.path = f.g.network.path
	wireLiveWGTable(recovery, st, w)
	if err := recovery.g.recover(ctx, filepath.Join(t.TempDir(), "absent-seed")); err != nil {
		t.Fatal(err)
	}
	if len(recovery.g.base.Peers) != 1 {
		t.Fatal("fixture lost committed routing identity")
	}
	if w.has(a.PublicKey) || recovery.isGuarded() {
		t.Fatal("recovery replayed committed authentication or failed release")
	}
}
