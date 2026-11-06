package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/store"
)

type wgPeerReader interface {
	Peers(context.Context) ([]store.Peer, error)
}

type wgPeerSyncer interface {
	SyncPeers(context.Context, []store.Peer) (netstack.WGPeerChanges, error)
}

// syncLiveWGPeers must run under the publication gate (or during exclusive
// startup recovery). Authentication always comes from live rows, never policy.
func syncLiveWGPeers(ctx context.Context, live wgPeerReader, wg wgPeerSyncer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	peers, err := live.Peers(ctx)
	if err != nil {
		return fmt.Errorf("read live WireGuard peers: %w", err)
	}
	if _, err := wg.SyncPeers(ctx, peers); err != nil {
		return fmt.Errorf("reconcile live WireGuard peers: %w", err)
	}
	return nil
}

// Lock before reading: a captured periodic snapshot must never cross a peer
// mutation/publication boundary and reinstall a revoked kernel identity.
func (g *applyGate) syncWGPeers(ctx context.Context, live wgPeerReader, wg wgPeerSyncer) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return syncLiveWGPeers(ctx, live, wg)
}

// configureLiveWG is the guarded publication callback, shared with the isolated
// encrypted admission fixture. Up alone cannot revoke an existing kernel key.
func configureLiveWG(ctx context.Context, st *store.Store, settings store.Settings) error {
	wg, err := startWG(ctx, st, settings)
	if err != nil {
		return err
	}
	return errors.Join(syncLiveWGPeers(ctx, st, wg), wg.Close())
}
