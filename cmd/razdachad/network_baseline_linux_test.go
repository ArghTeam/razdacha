//go:build linux && security214_baseline

package main

import (
	"bytes"
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/vishvananda/netlink"
)

// This adapter is compiled only with the archived unfixed source. It deliberately
// invokes its original startNetfilter/watchLists and database-reading callback.
func wireExercise(t *testing.T) {
	h := newWireHarness(t)
	mode := os.Getenv("RAZDACHA_WIRE_BASELINE_CASE")
	if mode == "" {
		mode = "list-tcp"
	}
	switch mode {
	case "list-tcp", "list-udp", "list-dns", "pending":
	default:
		t.Fatalf("unknown baseline case %q", mode)
	}
	handle, err := netlink.NewHandle()
	wireMust(t, err)
	defer handle.Close()
	wireRoute(t, handle, "", "192.0.2.2", "wan0")
	st, err := store.Open(h.ctx, filepath.Join(t.TempDir(), "state.db"))
	wireMust(t, err)
	t.Cleanup(func() { _ = st.Close() })
	wireMust(t, st.SaveSettings(h.ctx, h.settings))
	_, err = st.CreateRule(h.ctx, store.Rule{Name: "Fixture list", Enabled: true, PeerScope: store.ScopeAll, Action: store.ActionBlock, RemoteLists: []string{h.url}})
	wireMust(t, err)
	manual, err := st.CreateRule(h.ctx, store.Rule{Name: "Fixture saved policy", Enabled: true, PeerScope: store.ScopeAll, Action: store.ActionBlock, Subnets: []string{"203.0.113.30/32"}})
	wireMust(t, err)
	dir := t.TempDir()
	r := &wireReload{bin: os.Getenv("RAZDACHA_WIRE_BIN"), path: filepath.Join(dir, "config.json"), entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(r.stop)
	a := &singbox.Applier{ConfigPath: r.path, Checker: singbox.BinaryChecker{Binary: r.bin}, Reloader: r, PlainLists: plainLists(h.mgr), Log: quietSlog()}
	applied, err := st.Snapshot(h.ctx)
	wireMust(t, err)
	_, err = a.Apply(h.ctx, applied)
	wireMust(t, err)
	before, err := os.ReadFile(r.path)
	wireMust(t, err)
	ctx, cancel := context.WithCancel(h.ctx)
	t.Cleanup(cancel)
	nf, err := startNetfilter(ctx, st, h.mgr, quietSlog())
	wireMust(t, err)
	t.Cleanup(nf.stop)
	h.probe(t, "tcp4", "203.0.113.10:12080", "baseline-initial-tcp", true)
	h.probe(t, "udp4", "203.0.113.10:12080", "baseline-initial-udp", true)
	h.probe(t, "tcp4", "203.0.113.20:12080", "baseline-control", true)
	h.probe(t, "dns", "203.0.113.20:53", "unrelated.example.", true)
	h.probe(t, "tcp4", "203.0.113.30:12080", "baseline-manual-initial", false)
	if mode == "pending" {
		manual.Subnets = []string{"203.0.113.99/32"}
		wireMust(t, st.UpdateRule(h.ctx, manual))
	}
	h.next.Store(true)
	wireMust(t, h.mgr.Refresh(ctx))
	deadline := time.Now().Add(5 * time.Second)
	contains := func(state netstack.DiagNftState, addr string) bool {
		ip := netip.MustParseAddr(addr)
		for _, r := range state.Subnets {
			if r.Contains(ip) {
				return true
			}
		}
		return false
	}
	for {
		state, err := netstack.DiagNft(ctx)
		if err == nil && contains(state, "203.0.113.10") && (mode != "pending" || !contains(state, "203.0.113.30")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("original watcher did not publish expected kernel generation: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	after, err := os.ReadFile(r.path)
	wireMust(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("baseline premise invalid: runtime config changed")
	}
	t.Logf("original watcher refreshed nft while runtime config stayed stale: case=%s", mode)
	switch mode {
	case "list-tcp":
		h.probe(t, "tcp4", "203.0.113.10:12080", "blocked-final-baseline-tcp", false)
	case "list-udp":
		h.probe(t, "udp4", "203.0.113.10:12080", "blocked-final-baseline-udp", false)
	case "list-dns":
		h.probe(t, "dns", "203.0.113.20:53", "blocked.example.", false)
	case "pending":
		h.probe(t, "tcp4", "203.0.113.30:12080", "blocked-final-baseline-unapplied", false)
	}
	h.assertNoForbidden(t)
}
