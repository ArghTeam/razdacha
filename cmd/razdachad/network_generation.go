package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"go4.org/netipx"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

var errNoAppliedGeneration = errors.New("clients are guarded: explicit Apply is required to initialize network policy")

// networkPublication is accessed only while the gate mutex is held.
type networkPublication struct {
	manager       *lists.Manager
	guard         func(bool) error
	route         func(context.Context) error
	wg            func(context.Context, store.Settings) error
	validateWG    func(context.Context, store.Settings) error
	nft           func(context.Context, store.Snapshot, map[string]lists.List) (int, error)
	path          string
	bootstrapPath string
	contents      map[string]lists.List
	initialized   bool
	uncertain     bool
}

type networkGeneration = networkstate.Generation

func readGeneration(path string) (networkGeneration, error) {
	return networkstate.Read(path, networkstate.CommittedKind)
}
func writeGeneration(path string, gen networkGeneration) error {
	gen.Kind = networkstate.CommittedKind
	return networkstate.Write(path, gen)
}

// publish validates before mutation, then closes client ingress across every
// publication boundary. The separate kernel guard survives daemon failure.
func (g *applyGate) publish(ctx context.Context, snap store.Snapshot, recovery, repair bool) (singbox.ApplyResult, error) {
	n := g.network
	// Clone policy, including nested slices/maps, before retaining it as applied.
	data, err := json.Marshal(snap)
	if err != nil {
		return singbox.ApplyResult{}, fmt.Errorf("copy candidate policy: %w", err)
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return singbox.ApplyResult{}, fmt.Errorf("copy candidate policy: %w", err)
	}
	if err := validateWGPolicy(snap.Settings, snap.Peers); err != nil {
		return singbox.ApplyResult{}, fmt.Errorf("%w: %w", singbox.ErrGenerateFailed, err)
	}
	if n.validateWG != nil {
		if err := n.validateWG(ctx, snap.Settings); err != nil {
			return singbox.ApplyResult{}, fmt.Errorf("%w: %w", singbox.ErrGenerateFailed, err)
		}
	}
	contents := n.contents
	if !recovery {
		contents = nil
		if n.manager != nil {
			contents = n.manager.Snapshot()
		}
	}
	selected := make(map[string]lists.List)
	for _, src := range lists.Sources(snap) {
		if l, ok := contents[src.URL]; ok {
			selected[src.URL] = l
		} else if src.Format == lists.FormatPlain {
			return singbox.ApplyResult{}, fmt.Errorf("%w: required plain list is not loaded", singbox.ErrGenerateFailed)
		}
	}
	a := *g.inner
	a.PlainLists = frozenPlainLists(selected)
	var prepared *singbox.Prepared
	if n.uncertain {
		prepared, err = a.PrepareRecovery(ctx, snap)
	} else {
		prepared, err = a.Prepare(ctx, snap)
	}
	if err != nil {
		return singbox.ApplyResult{}, err
	}
	defer prepared.Close()
	changedGeneration := !reflect.DeepEqual(g.base, snap) || !reflect.DeepEqual(n.contents, selected)
	needRestart := n.uncertain || prepared.Changed() || changedGeneration
	if !repair && !needRestart {
		res, err := prepared.Commit(ctx, false)
		count := len(generationSubnets(snap, selected))
		res.NftSubnets = &count
		return res, err
	}
	if err := ctx.Err(); err != nil {
		return singbox.ApplyResult{}, err
	}
	if err := n.guard(true); err != nil {
		return singbox.ApplyResult{}, fmt.Errorf("close client ingress: %w", err)
	}
	n.uncertain = true
	if n.wg != nil {
		if err := n.wg(ctx, snap.Settings); err != nil {
			return singbox.ApplyResult{}, fmt.Errorf("clients remain guarded: configure WireGuard: %w", err)
		}
	}
	if n.route != nil {
		if err := n.route(ctx); err != nil {
			return singbox.ApplyResult{}, fmt.Errorf("clients remain guarded: publish policy routing: %w", err)
		}
	}
	res, err := prepared.Commit(ctx, needRestart)
	if err != nil {
		return res, fmt.Errorf("clients remain guarded: %w", err)
	}
	count, err := n.nft(ctx, snap, selected)
	if err != nil {
		return res, fmt.Errorf("clients remain guarded: publish nft policy: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("clients remain guarded: %w", err)
	}
	if err := writeGeneration(n.path, networkGeneration{Version: 1, Policy: snap, Lists: selected}); err != nil {
		return res, fmt.Errorf("clients remain guarded: %w", err)
	}
	seedPath := n.bootstrapPath
	if seedPath == "" {
		seedPath = filepath.Join(filepath.Dir(n.path), networkstate.BootstrapFile)
	}
	if err := networkstate.DiscardBootstrap(seedPath); err != nil {
		return res, fmt.Errorf("clients remain guarded: %w", err)
	}
	g.base = snap
	n.contents = selected
	n.initialized = true
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("clients remain guarded: %w", err)
	}
	if err := n.guard(false); err != nil {
		return res, fmt.Errorf("clients remain guarded: release ingress guard: %w", err)
	}
	n.uncertain = false
	res.NftSubnets = &count
	return res, nil
}

func (g *applyGate) refreshLists(ctx context.Context) (singbox.ApplyResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.network.initialized {
		return singbox.ApplyResult{}, errNoAppliedGeneration
	}
	return g.publish(ctx, g.base, false, false)
}

func watchAppliedLists(ctx context.Context, mgr *lists.Manager, g *applyGate, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-mgr.Updates():
		}
		if _, err := g.refreshLists(ctx); err != nil && ctx.Err() == nil {
			log.Error("List generation not published", "error", err)
		}
	}
}

func generationSubnets(snap store.Snapshot, contents map[string]lists.List) []string {
	out := ruleSubnets(snap.Rules)
	for _, src := range lists.Sources(snap) {
		out = append(out, contents[src.URL].Subnets...)
	}
	return out
}

func (g *applyGate) appliedSources() []lists.Source {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.network.initialized {
		return nil
	}
	return lists.Sources(g.base)
}

func (g *applyGate) networkStatus() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.network.initialized {
		return fmt.Errorf("%w: %w", api.ErrNetworkGuarded, errNoAppliedGeneration)
	}
	if g.network.uncertain {
		return fmt.Errorf("%w: retry Apply to recover a coherent generation", api.ErrNetworkGuarded)
	}
	return nil
}

var _ api.CoherentApplier = (*applyGate)(nil)

// ApplyCurrent acquires the editable snapshot inside the publication lock so a
// concurrent peer allocation cannot precede publication of stale peer identity.
func (g *applyGate) ApplyCurrent(ctx context.Context, st *store.Store) (singbox.ApplyResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	snap, err := st.Snapshot(ctx)
	if err != nil {
		return singbox.ApplyResult{}, err
	}
	return g.publish(ctx, snap, false, true)
}

func (g *applyGate) CreatePeer(ctx context.Context, st *store.Store, name string, now time.Time) (store.Peer, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.network.initialized {
		return store.Peer{}, fmt.Errorf("%w: %w", api.ErrNetworkGuarded, errNoAppliedGeneration)
	}
	snap, err := st.Snapshot(ctx)
	if err != nil {
		return store.Peer{}, err
	}
	if snap.Settings.WGPool != g.base.Settings.WGPool || snap.Settings.WGServerAddress != g.base.Settings.WGServerAddress {
		return store.Peer{}, fmt.Errorf("%w: apply WireGuard address settings before creating peers", api.ErrNetworkGuarded)
	}
	reserved := make([]string, 0, len(g.base.Peers))
	for _, p := range g.base.Peers {
		reserved = append(reserved, p.Address)
	}
	return api.CreatePeerFromSnapshot(ctx, st, name, now, snap, reserved)
}

func validateWGPolicy(settings store.Settings, peers []store.Peer) error {
	cfg, err := netstack.WGConfigFromSettings(settings)
	if err != nil {
		return err
	}
	for _, p := range peers {
		addr, err := netip.ParseAddr(p.Address)
		if err != nil {
			return errors.New("stored peer address is invalid")
		}
		if !cfg.Address.Contains(addr) || addr == cfg.Address.Masked().Addr() || addr == netipx.RangeOfPrefix(cfg.Address).To() {
			return errors.New("candidate WireGuard pool excludes a stored peer")
		}
		if addr == cfg.Address.Addr() {
			return errors.New("candidate WireGuard server address collides with a stored peer")
		}
	}
	return nil
}

// initialWGSettings trusts a checked routing record, never editable policy when
// one exists. A genuinely legacy DB fallback is administrative access only; the
// already-installed guard remains until explicit Apply authorizes publication.
func initialWGSettings(ctx context.Context, st *store.Store, dbPath string) (store.Settings, error) {
	dir := filepath.Dir(dbPath)
	gen, err := readGeneration(filepath.Join(dir, networkstate.CommittedFile))
	if errors.Is(err, os.ErrNotExist) {
		gen, err = networkstate.Read(filepath.Join(dir, networkstate.BootstrapFile), networkstate.BootstrapKind)
	}
	if errors.Is(err, os.ErrNotExist) {
		return st.Settings(ctx)
	}
	if err != nil {
		return store.Settings{}, err
	}
	return gen.Policy.Settings, nil
}
