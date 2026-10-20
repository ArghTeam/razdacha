package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/restorestate"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// applyGate owns all manual, list and pool publication (ADR 0023).
// The editable database is never an automatic source of applied routing policy.
type applyGate struct {
	inner   *singbox.Applier
	log     *slog.Logger
	mu      sync.Mutex
	base    store.Snapshot
	restore *restorestate.Controller
	network *networkPublication
}

func newApplyGate(inner *singbox.Applier, network *networkPublication, log *slog.Logger) *applyGate {
	if log == nil {
		log = slog.Default()
	}
	return &applyGate{inner: inner, network: network, log: log}
}

func (g *applyGate) Apply(ctx context.Context, snap store.Snapshot) (singbox.ApplyResult, error) {
	lock, e := g.restore.PublicationLock()
	if e != nil {
		return singbox.ApplyResult{}, e
	}
	defer func() { _ = lock.Close() }()
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.restoreApplyAllowed(snap); err != nil {
		return singbox.ApplyResult{}, err
	}
	return g.publish(ctx, snap, false, true)
}

func (g *applyGate) applyPools(ctx context.Context, fresh store.Snapshot) (singbox.ApplyResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.network.initialized {
		return singbox.ApplyResult{}, errNoAppliedGeneration
	}
	return g.publish(ctx, withPoolServers(g.base, fresh), true, false)
}

// withPoolServers переносит состав пулов из свежего снимка в применённый.
//
// Переносится только состав и время обхода, и только у пулов, которые в
// применённом состоянии уже есть: заведённый, но не применённый пул — такая же
// правка пользователя, как правило, и выкатывать её за него нечего.
func withPoolServers(base, fresh store.Snapshot) store.Snapshot {
	pools := make(map[string]store.Tunnel, len(fresh.Tunnels))
	for _, t := range fresh.Tunnels {
		if t.Source == store.SourcePool {
			pools[t.ID] = t
		}
	}

	out := base
	out.Tunnels = append([]store.Tunnel(nil), base.Tunnels...)
	for i, t := range out.Tunnels {
		if t.Source != store.SourcePool {
			continue
		}
		f, ok := pools[t.ID]
		if !ok {
			continue
		}
		out.Tunnels[i].Pool = f.Pool
		out.Tunnels[i].PoolUpdatedAt = f.PoolUpdatedAt
	}
	return out
}

// startApply recovers known policy before subscribing background updates.
func startApply(ctx context.Context, st *store.Store, pools *lists.PoolManager,
	mgr *lists.Manager, nf netfilter, dbPath string, log *slog.Logger, restore ...*restorestate.Controller,
) (*applyGate, error) {
	g := newApplyGate(singbox.NewApplier(), &networkPublication{
		manager: mgr, guard: nf.guard, route: nf.ensureRoute, nft: nf.publish,
		path:      filepath.Join(filepath.Dir(dbPath), networkstate.CommittedFile),
		uncertain: true,
	}, log)
	if len(restore) > 0 {
		g.restore = restore[0]
	}
	g.inner.BeforeReload = func(context.Context) error { return g.restore.BeginPublishing(g.inner.ConfigPath) }
	if g.restore != nil {
		g.inner.AfterReload = func(ctx context.Context) error {
			return (packaging.RestoreServices{Query: packaging.SystemdQuery}).RuntimeReady(ctx)
		}
	}
	g.network.validateWG = func(ctx context.Context, settings store.Settings) error {
		peers, err := st.Peers(ctx)
		if err != nil {
			return err
		}
		return validateWGPolicy(settings, peers)
	}
	g.network.wg = func(ctx context.Context, settings store.Settings) error {
		wg, err := startWG(ctx, st, settings)
		if err != nil {
			return err
		}
		defer func() { _ = wg.Close() }()
		peers, err := st.Peers(ctx)
		if err != nil {
			return err
		}
		_, err = wg.SyncPeers(ctx, peers)
		return err
	}
	if err := g.recoverStartup(ctx, filepath.Join(filepath.Dir(dbPath), networkstate.BootstrapFile)); err != nil {
		return nil, err
	}

	if mgr != nil {
		go watchAppliedLists(ctx, mgr, g, log)
		if len(restore) == 0 {
			go syncListSources(ctx, st, mgr, nil, g.appliedSources, log)
		}
	}
	if pools != nil {
		go watchPools(ctx, st, pools, g, log)
	}
	return g, nil
}

// watchPools changes only committed pool membership; nft and lists remain based
// on the committed generation even when the database has pending policy edits.
func watchPools(ctx context.Context, st *store.Store, m *lists.PoolManager,
	g *applyGate, log *slog.Logger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.Updates():
		}

		snap, err := st.Snapshot(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error("чтение состояния после обновления пула", "ошибка", err)
			continue
		}

		res, err := g.applyPools(ctx, snap)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// Прежний конфиг остаётся в силе, а пул — со старыми ключами до
			// следующего обхода. Ронять демон не за что: остальные туннели
			// работают.
			log.Error("конфиг не применён после обновления пула", "ошибка", err)
			continue
		}
		if !res.Changed {
			log.Debug("состав пула обновлён, конфиг тот же", "путь", res.Path)
			continue
		}
		log.Info("конфиг применён после обновления состава пула",
			"путь", res.Path, "перезагружен", res.Reloaded)
	}
}

// recover runs under the startup ingress guard and never reads editable policy.
func (g *applyGate) recover(ctx context.Context, seedPath string) error {
	g.network.bootstrapPath = seedPath
	committed, err := readGeneration(g.network.path)
	if errors.Is(err, os.ErrNotExist) {
		committed, err = networkstate.Read(seedPath, networkstate.BootstrapKind)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if committed.Version == 1 {
			for _, src := range lists.Sources(committed.Policy) {
				if src.Format != lists.FormatPlain {
					g.log.Error("Legacy native routing authority is unsupported; clients remain guarded until sources are validated and explicit Apply succeeds")
					return nil
				}
			}
		}
		g.base = committed.Policy
		g.network.contents = committed.Lists
		g.network.initialized = true
		if _, err := g.publish(ctx, committed.Policy, true, false); err != nil {
			return fmt.Errorf("recover applied network generation: %w", err)
		}
	} else {
		g.log.Warn("No committed network generation; clients remain guarded until explicit Apply")
	}
	return nil
}
