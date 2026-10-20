//go:build linux && security220_baseline

package main

import (
	"net/netip"
	"os"
	"testing"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/singbox"
)

// Compiled only over the archived d5b8ad3 source. It uses that source's original
// remote generator and flattened daemon list snapshots. No fixed path is imported.
func nativeWireExercise(t *testing.T) {
	n := newNativeWire(t)
	h := n.h
	r, _, snap := n.runtime(t)
	load := func() {
		opts, err := singbox.Generate(snap, plainLists(h.mgr))
		wireMust(t, err)
		opts.Experimental.CacheFile = nil
		body, err := singbox.Marshal(opts)
		wireMust(t, err)
		wireMust(t, os.WriteFile(r.path, body, 0o600))
		wireMust(t, (singbox.BinaryChecker{Binary: r.bin}).Check(h.ctx, r.path))
		wireMust(t, r.Reload(h.ctx))
	}
	wireMust(t, netstack.SetClientGuard(true))
	wireMust(t, netstack.NewRoute().Apply())
	load()
	_, err := n.nft(h.ctx, snap, h.mgr.Snapshot())
	wireMust(t, err)
	wireMust(t, netstack.SetClientGuard(false))
	n.preconditions(t)
	t.Logf("220-BASELINE-PRECONDITIONS-PASS format=%s case=%s", n.format, n.skew)
	beforeRuntimeOld, beforeDaemonOld := n.runtimeOld.Load(), n.daemonOld.Load()
	n.phase.Store(1)
	wireMust(t, netstack.SetClientGuard(true))
	wireMust(t, h.mgr.Refresh(h.ctx))
	load()
	_, err = n.nft(h.ctx, snap, h.mgr.Snapshot())
	wireMust(t, err)
	wireMust(t, netstack.SetClientGuard(false))
	if n.runtimeRequests.Load() < 2 {
		t.Fatal("baseline runtime did not fetch both controlled native versions")
	}
	state, err := netstack.DiagNft(h.ctx)
	wireMust(t, err)
	intercepted := false
	for _, r := range state.Subnets {
		intercepted = intercepted || r.Contains(netip.MustParseAddr("203.0.113.10"))
	}
	if n.skew == "daemon-new-runtime-old" {
		if !intercepted || n.daemonNew.Load() == 0 || n.runtimeOld.Load() <= beforeRuntimeOld || n.runtimeNew.Load() != 0 || n.daemonOld.Load() != beforeDaemonOld {
			t.Fatal("baseline daemon-new/runtime-old source/kernel proof failed")
		}
	} else {
		if intercepted || n.runtimeNew.Load() == 0 || n.runtimeOld.Load() != beforeRuntimeOld || n.daemonNew.Load() != 0 || n.daemonOld.Load() <= beforeDaemonOld {
			t.Fatal("baseline runtime-new/daemon-old source/kernel proof failed")
		}
	}
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.20:12080", "native-skew-control-"+proto, true)
		h.probe(t, proto, "203.0.113.30:12080", "blocked-final-native-skew-old-"+proto, false)
	}
	t.Logf("220-BASELINE-SKEW-PUBLISHED format=%s case=%s", n.format, n.skew)
	leaked := 0
	for _, proto := range []string{"tcp4", "udp4"} {
		cmd := wireExec(t, h.client, "probe", proto, "203.0.113.10:12080", "blocked-final-native-baseline-"+proto)
		if _, err := cmd.CombinedOutput(); err == nil {
			leaked++
		}
	}
	h.captureBarrier(t)
	if leaked == 2 {
		t.Fatalf("220-BASELINE-NATIVE-LEAK-OBSERVED format=%s case=%s: desired protection failed", n.format, n.skew)
	}
	if leaked != 0 {
		t.Fatalf("baseline prerequisite incomplete: only %d forbidden protocols echoed", leaked)
	}
	h.assertNoForbidden(t)
}
