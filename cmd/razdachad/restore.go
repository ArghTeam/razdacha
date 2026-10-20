package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/restorestate"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

func (g *applyGate) restoreApplyAllowed(snap store.Snapshot) error {
	if e := g.restore.ApplyAllowed(); e != nil {
		return e
	}
	s, e := g.restore.Status()
	if e != nil {
		return e
	}
	if s == nil || !s.RuntimeInhibited || s.RecoveryActivated {
		return nil
	}
	ids, e := singbox.RestoreWireGuardIDs(snap.Tunnels)
	if e != nil {
		return e
	}
	wg := make(map[string]bool)
	for _, id := range ids {
		wg[id] = true
	}
	for _, t := range snap.Tunnels {
		if t.Enabled && wg[t.ID] {
			return errors.New("first restore Apply must keep WireGuard paused; apply paused policy before enabling chosen credentials")
		}
	}
	return nil
}

// Restored peers reach wg0 only after a successful explicit coherent publication.
func syncRestoredPeers(ctx context.Context, st *store.Store, g *applyGate) {
	ticker := time.NewTicker(wgSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		g.mu.Lock()
		if g.network.initialized {
			wg, e := startWG(ctx, st, g.base.Settings)
			if e == nil {
				peers, e := st.Peers(ctx)
				if e == nil {
					_, _ = wg.SyncPeers(ctx, peers)
				}
				_ = wg.Close()
			}
		}
		g.mu.Unlock()
	}
}

// Resume rereads one current saved snapshot before starting any worker. Managers
// constructed while held must never immediately fetch their stale imported sources.
func registerRestoreActivity(ctx context.Context, st *store.Store, mgr *lists.Manager, pools *lists.PoolManager, g *applyGate, ctl *restorestate.Controller) error {
	return ctl.StartWhenAllowed(func() error {
		snap, e := st.Snapshot(ctx)
		if e != nil {
			return e
		}
		sources := lists.Sources(snap)
		seen := make(map[string]bool)
		for _, src := range sources {
			seen[src.URL] = true
		}
		for _, src := range g.appliedSources() {
			if !seen[src.URL] {
				sources = append(sources, src)
				seen[src.URL] = true
			}
		}
		tunnels := lists.PoolTunnels(snap)
		if mgr != nil {
			mgr.SetSources(sources)
			mgr.SetInterval(snap.Settings.ListUpdateInterval)
		}
		if pools != nil {
			pools.SetTunnels(tunnels)
			pools.SetInterval(snap.Settings.PoolUpdateInterval)
			pools.SetFilter(store.PoolFilterFrom(snap.Settings))
		}
		if mgr != nil {
			mgr.Start(ctx)
			go syncListSources(ctx, st, mgr, sources, g.appliedSources, slog.Default())
		}
		if pools != nil {
			pools.Start(ctx)
			go syncPoolTunnels(ctx, st, pools, tunnels, poolTunnelsInterval, slog.Default())
		}
		return nil
	})
}

// An inhibited restore never gains publication authority merely because an aborted
// candidate left a committed file behind. Only another explicit Apply may proceed.
func (g *applyGate) recoverStartup(ctx context.Context, seed string) error {
	state, e := g.restore.Status()
	if e != nil {
		return e
	}
	if state != nil && state.RuntimeInhibited {
		g.network.bootstrapPath = seed
		return nil
	}
	return g.recover(ctx, seed)
}

// Global network reset always belongs to the canonical installation, regardless of
// caller-supplied -db. Serialize before checking state so restore cannot race reset.
func resetCanonicalNetwork(dbPath, canonicalDB string, reset func() error) error {
	db, e := filepath.Abs(dbPath)
	if e != nil {
		return e
	}
	canonical, e := filepath.Abs(canonicalDB)
	if e != nil {
		return e
	}
	if db != canonical {
		return errors.New("global network reset refuses alternate database paths")
	}
	lock, e := restorestate.Acquire(filepath.Dir(canonical), -1)
	if e != nil {
		return e
	}
	defer func() { _ = lock.Close() }()
	state, e := restorestate.Read(filepath.Dir(canonical))
	if e != nil {
		return e
	}
	if state.Blocked() {
		return errors.New("network reset refused while restore protection is active")
	}
	return reset()
}

// Password changes may never alias a protected canonical installation through -db.
func passwordLifecycle(dbPath, canonicalDB string) (*restorestate.Lock, error) {
	canonical, e := filepath.Abs(canonicalDB)
	if e != nil {
		return nil, e
	}
	db, e := filepath.Abs(dbPath)
	if e != nil {
		return nil, e
	}
	lock, e := restorestate.Acquire(filepath.Dir(canonical), -1)
	if e != nil {
		return nil, e
	}
	fail := func(e error) (*restorestate.Lock, error) { _ = lock.Close(); return nil, e }
	state, e := restorestate.Read(filepath.Dir(canonical))
	if e != nil {
		return fail(e)
	}
	if state != nil {
		if db != canonical {
			return fail(errors.New("password changes refuse alternate database paths while trusted restore state exists"))
		}
		if state.Phase != "complete" {
			return fail(errors.New("password changes refuse incomplete restore state"))
		}
		info, e := os.Lstat(canonical)
		if e != nil {
			return fail(e)
		}
		if !info.Mode().IsRegular() {
			return fail(errors.New("password changes refuse symlink or nonregular restored databases"))
		}
	}
	return lock, nil
}
