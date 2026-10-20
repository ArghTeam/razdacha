//go:build linux && !security220_baseline

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/store"
)

func nativeWireExercise(t *testing.T) {
	n := newNativeWire(t)
	h := n.h
	_, a, snap := n.runtime(t)
	pub := &networkPublication{manager: h.mgr, path: filepath.Join(n.dir, "generation.json"), uncertain: true, guard: netstack.SetClientGuard, wg: func(_ context.Context, s store.Settings) error { return wireConfigureWG(s) }, route: func(context.Context) error { return netstack.NewRoute().Apply() }, nft: n.nft}
	gate := newApplyGate(a, pub, quietSlog())
	_, err := gate.Apply(h.ctx, snap)
	wireMust(t, err)
	n.preconditions(t)
	h.probe(t, "dns", "203.0.113.20:53", "unrelated.example.", true)
	n.phase.Store(1)
	wireMust(t, h.mgr.Refresh(h.ctx))
	_, err = gate.refreshLists(h.ctx)
	wireMust(t, err)
	if n.skew == "runtime-new-daemon-old" {
		// An unseen upstream version has no authority: local old matching stays old.
		h.probe(t, "tcp4", "203.0.113.10:12080", "native-unaccepted-version-control", true)
		h.probe(t, "dns", "203.0.113.20:53", "blocked.example.", true)
		n.phase.Store(2)
		wireMust(t, h.mgr.Refresh(h.ctx))
		_, err = gate.refreshLists(h.ctx)
		wireMust(t, err)
	}
	for _, proto := range []string{"tcp4", "udp4"} {
		h.probe(t, proto, "203.0.113.10:12080", "blocked-final-native-new-"+proto, false)
		h.probe(t, proto, "203.0.113.30:12080", "blocked-final-native-old-final-"+proto, false)
		h.probe(t, proto, "203.0.113.20:12080", "native-final-control-"+proto, true)
	}
	h.probe(t, "dns", "203.0.113.20:53", "unrelated.example.", true)
	// The expected DNS refusal must be a valid response, never a transport error.
	cmd := wireExec(t, h.client, "native-dns-reject", "blocked.example.")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native selected DNS rejection: %v %s", err, out)
	}
	if n.runtimeRequests.Load() != 0 {
		t.Fatal("local native runtime fetched an autonomous source")
	}
	// Restart from the committed native authority and recheck its selected address.
	wireMust(t, netstack.SetClientGuard(true))
	pub.uncertain = true
	wireMust(t, gate.recover(h.ctx, filepath.Join(n.dir, "absent-seed")))
	h.probe(t, "tcp4", "203.0.113.10:12080", "blocked-final-native-recovered", false)
	h.assertNoForbidden(t)
	t.Logf("220-FIXED-NATIVE-WIRE-PASS format=%s case=%s autonomous_requests=0", n.format, n.skew)
}
