//go:build linux && !security214_baseline

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

func wireExercise(t *testing.T) {
	h := newWireHarness(t)
	dir := t.TempDir()
	r := &wireReload{bin: os.Getenv("RAZDACHA_WIRE_BIN"), path: filepath.Join(dir, "config.json"), entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(r.stop)
	a := &singbox.Applier{ConfigPath: r.path, Checker: singbox.BinaryChecker{Binary: r.bin}, Reloader: r, Log: quietSlog()}
	n := &networkPublication{manager: h.mgr, guard: netstack.SetClientGuard, wg: func(_ context.Context, settings store.Settings) error { return wireConfigureWG(settings) }, path: filepath.Join(dir, "generation.json"), uncertain: true, route: func(context.Context) error { return netstack.NewRoute().Apply() }, nft: func(ctx context.Context, snap store.Snapshot, content map[string]lists.List) (int, error) {
		nf, err := netstack.NewNft()
		if err != nil {
			return 0, err
		}
		sub := generationSubnets(snap, content)
		rs, err := nf.Apply(netstack.NftConfig{WANInterface: "wan0", ResolverAddr: snap.Settings.WGServerAddress, Subnets: sub})
		if err == nil && rs.SkippedSubnets != 0 {
			err = fmt.Errorf("invalid fixture subnets")
		}
		return len(sub), err
	}}
	gate := newApplyGate(a, n, quietSlog())
	st, err := store.Open(h.ctx, filepath.Join(dir, "state.db"))
	wireMust(t, err)
	t.Cleanup(func() { _ = st.Close() })
	wireMust(t, st.SaveSettings(h.ctx, h.settings))
	_, err = st.CreateRule(h.ctx, store.Rule{Name: "Fixture block", Enabled: true, PeerScope: store.ScopeAll, Action: store.ActionBlock, RemoteLists: []string{h.url}})
	wireMust(t, err)
	// Positive echo proves the guard includes an already intercepted tproxy path.
	_, err = st.CreateRule(h.ctx, store.Rule{Name: "Direct control", Enabled: true, Action: store.ActionDirect, PeerScope: store.ScopeAll, Subnets: []string{"203.0.113.20/32"}})
	wireMust(t, err)
	manual, err := st.CreateRule(h.ctx, store.Rule{Name: "Manual block", Enabled: true, Action: store.ActionBlock, PeerScope: store.ScopeAll, Subnets: []string{"203.0.113.30/32"}})
	wireMust(t, err)
	snap, err := st.Snapshot(h.ctx)
	wireMust(t, err)
	phase2Commits := make(chan struct{}, 2)
	n.guard = func(enabled bool) error {
		if !enabled {
			if err := wireResolverIsLocal(gate.base.Settings); err != nil {
				return err
			}
		}
		err := netstack.SetClientGuard(enabled)
		if err == nil && !enabled && h.extended.Load() {
			select {
			case phase2Commits <- struct{}{}:
			default:
			}
		}
		return err
	}
	_, err = gate.Apply(h.ctx, snap)
	wireMust(t, err)
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.10:12080", "initial-"+proto, true)
		h.probe(t, proto, "203.0.113.20:12080", "control-initial-"+proto, true)
	}
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.99:12080", "pending-control-initial-"+proto, true)
		h.probe(t, proto, "203.0.113.30:12080", "blocked-final-manual-initial-"+proto, false)
	}
	h.probe(t, "dns", "203.0.113.20:53", "unrelated.example.", true)
	h.probe(t, "dnstcp", "203.0.113.20:53", "unrelated.example.", true)
	// Explicit unchanged Apply repairs removed route/nft state without restarting
	// a known runtime. Close ingress before deliberately removing fixture state.
	wireMust(t, netstack.SetClientGuard(true))
	wireMust(t, netstack.NewRoute().Clear())
	removedNft, err := netstack.NewNft()
	wireMust(t, err)
	wireMust(t, removedNft.Clear())
	restarts := r.restarts.Load()
	repair, err := gate.Apply(h.ctx, snap)
	wireMust(t, err)
	if repair.Reloaded || r.restarts.Load() != restarts {
		t.Fatal("unchanged manual network repair restarted the runtime")
	}
	h.probe(t, "tcp4", "203.0.113.20:12080", "repaired-tproxy-control", true)
	// Reconcile a staged, uncommitted resolver address on restart. The guard-off
	// callback independently proves the committed resolver became local again.
	wireMust(t, netstack.SetClientGuard(true))
	pending := h.settings
	pending.WGServerAddress = "10.8.0.254"
	wireMust(t, wireConfigureWG(pending))
	gate.mu.Lock()
	n.uncertain = true
	err = gate.recover(h.ctx, filepath.Join(dir, "absent-seed"))
	gate.mu.Unlock()
	wireMust(t, err)
	h.probe(t, "dns", "203.0.113.20:53", "unrelated.example.", true)
	streams := make(map[string]*wireStream)
	for _, proto := range []string{"tcp4", "udp4"} {
		stream := h.stream(t, proto, "203.0.113.10:12080")
		stream.exchange(t, "established-initial-"+proto, true)
		streams[proto] = stream
	}
	ctx, cancel := context.WithCancel(h.ctx)
	done := make(chan struct{})
	go func() { defer close(done); watchAppliedLists(ctx, h.mgr, gate, quietSlog()) }()
	t.Cleanup(func() { cancel(); <-done })
	// Save a real edit without Apply. Refresh must retain the applied .30 block
	// and must not introduce the pending .99 block.
	manual.Subnets = []string{"203.0.113.99/32"}
	wireMust(t, st.UpdateRule(h.ctx, manual))
	r.pause.Store(true)
	h.next.Store(true)
	wireMust(t, h.mgr.Refresh(ctx))
	select {
	case <-r.entered:
	case <-ctx.Done():
		t.Fatal("watcher did not enter guarded publication")
	}
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.10:12081", "guarded-target-"+proto, false)
		h.probe(t, proto, "203.0.113.20:12081", "guarded-control-"+proto, false)
	}
	h.probe(t, "dns", "203.0.113.20:53", "guarded-nonce.example.", false)
	h.probe(t, "dnstcp", "203.0.113.20:53", "guarded-tcp.example.", false)
	for proto, stream := range streams {
		stream.exchange(t, "guarded-established-"+proto, false)
	}
	close(r.release)
	deadline := time.Now().Add(6 * time.Second)
	for {
		gen, err := readGeneration(n.path)
		if err == nil && len(gen.Lists[h.url].Subnets) == 1 && gen.Lists[h.url].Subnets[0] == "203.0.113.10/32" && gate.networkStatus() == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new generation did not become ready")
		}
		time.Sleep(25 * time.Millisecond)
	}
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.10:12080", "blocked-final-"+proto, false)
		h.probe(t, proto, "203.0.113.20:12080", "control-final-"+proto, true)
	}
	for proto, stream := range streams {
		stream.exchange(t, "blocked-final-established-"+proto, false)
	}
	h.probe(t, "dns", "203.0.113.20:53", "blocked.example.", false)
	h.probe(t, "dns", "203.0.113.20:53", "unrelated.example.", true)
	h.probe(t, "dnstcp", "203.0.113.20:53", "unrelated.example.", true)
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.99:12080", "pending-not-applied-"+proto, true)
		h.probe(t, proto, "203.0.113.30:12080", "blocked-final-pending-policy-"+proto, false)
	}
	// Explicitly remove the manual rule only after observing the unapplied edit.
	// This restores .30 as an independent direct control for the failure matrix.
	wireMust(t, st.DeleteRule(h.ctx, manual.ID))
	_, err = gate.ApplyCurrent(ctx, st)
	wireMust(t, err)
	snap, err = st.Snapshot(ctx)
	wireMust(t, err)
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.30:12080", "manual-removed-control-"+proto, true)
	}
	// A second plain generation preserves subnet intent while adding a domain.
	// Queue the actual ApplyCurrent entry point behind the paused watcher.
	r.entered = make(chan struct{})
	r.release = make(chan struct{})
	r.pause.Store(true)
	h.extended.Store(true)
	wireMust(t, h.mgr.Refresh(ctx))
	select {
	case <-r.entered:
	case <-ctx.Done():
		t.Fatal("second watcher publication did not pause")
	}
	manualStarted := make(chan struct{})
	manualDone := make(chan error, 1)
	go func() { close(manualStarted); _, err := gate.ApplyCurrent(ctx, st); manualDone <- err }()
	<-manualStarted
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.30:12081", "guarded-overlap-"+proto, false)
	}
	h.probe(t, "dns", "203.0.113.20:53", "guarded-overlap.example.", false)
	select {
	case err := <-manualDone:
		t.Fatalf("queued Apply completed while watcher held ingress guard: %v", err)
	default:
	}
	close(r.release)
	select {
	case err := <-manualDone:
		wireMust(t, err)
	case <-ctx.Done():
		t.Fatal("queued Apply did not complete")
	}
	for range 2 {
		select {
		case <-phase2Commits:
		case <-ctx.Done():
			t.Fatal("both serialized publications did not finish")
		}
	}
	gate.mu.Lock()
	hasExtra := slices.Contains(n.contents[h.url].Domains, "extra.example")
	gate.mu.Unlock()
	if !hasExtra || gate.networkStatus() != nil {
		t.Fatal("concurrent publications lost the new list generation")
	}
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.20:12080", "overlap-control-"+proto, true)
		h.probe(t, proto, "203.0.113.10:12080", "blocked-final-overlap-"+proto, false)
	}
	// A publication error after the real runtime restart must leave the real
	// kernel guard installed until recovery. The restored third target is direct.
	h.probe(t, "tcp4", "203.0.113.30:12080", "always-direct-before-failure", true)
	realNft := n.nft
	gate.mu.Lock()
	n.nft = func(context.Context, store.Snapshot, map[string]lists.List) (int, error) {
		return 0, errors.New("fixture publication failure")
	}
	gate.mu.Unlock()
	candidate := snap
	candidate.Rules = append([]store.Rule(nil), snap.Rules...)
	candidate.Rules[1].Action = store.ActionBlock
	if _, err := gate.Apply(h.ctx, candidate); err == nil {
		t.Fatal("injected post-restart publication failure succeeded")
	}
	if gate.networkStatus() == nil {
		t.Fatal("failed publication reported active policy")
	}
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.30:12081", "guarded-failure-"+proto, false)
	}
	h.probe(t, "dns", "203.0.113.20:53", "guarded-failure.example.", false)
	gate.mu.Lock()
	n.nft = realNft
	gate.mu.Unlock()
	_, err = gate.Apply(h.ctx, snap)
	wireMust(t, err)
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.10:12080", "blocked-final-recovery-"+proto, false)
		h.probe(t, proto, "203.0.113.20:12080", "control-recovery-"+proto, true)
	}
	h.assertNoForbidden(t)
}
