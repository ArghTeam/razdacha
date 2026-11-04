package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/ingressguard"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

type realityGateFixture struct {
	g                *applyGate
	st               *store.Store
	p                store.Peer
	closed, wgClosed bool
	reloads          int
	failReload       bool
	loaded           []byte
}

func newRealityGateFixture(t *testing.T) *realityGateFixture {
	t.Helper()
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f := &realityGateFixture{closed: true, wgClosed: true}
	f.st, err = store.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.st.Close() })
	cfg := store.DefaultSettings()
	cfg.EndpointHost = "gateway.fixture.example"
	cfg.RealityEnabled = true
	cfg.RealityServerName = "fixture.example"
	cfg.RealityHandshakeServer = "127.0.0.1"
	if err := f.st.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	f.p, err = api.CreatePeer(ctx, f.st, "Client", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	f.p, err = f.st.PatchPeer(ctx, f.p.ID, store.PeerPatch{RealityEnabled: &yes})
	if err != nil {
		t.Fatal(err)
	}
	a := &singbox.Applier{ConfigPath: filepath.Join(dir, "config.json"), Checker: checkerFunc(func(context.Context, string) error { return nil }), Log: quietSlog()}
	n := &networkPublication{path: filepath.Join(dir, networkstate.CommittedFile), uncertain: true}
	n.guard = func(v bool) error { f.wgClosed = v; return nil }
	n.publicGuard = func(_ []ingressguard.Endpoint, v bool) error { f.closed = v; return nil }
	n.checkPublicPort = func(ingressguard.Endpoint) error { return nil }
	n.nft = func(context.Context, store.Snapshot, map[string]lists.List) (int, error) {
		if !f.closed || !f.wgClosed {
			t.Error("policy changed with open client ingress")
		}
		return 0, nil
	}
	f.g = newApplyGate(a, n, quietSlog())
	f.g.store = f.st
	a.Reloader = reloaderFunc(func(context.Context) error {
		f.reloads++
		if !f.closed || !f.wgClosed {
			t.Error("reload occurred with open client ingress")
		}
		// Exercise the actual independent helper while the publication mutex is held.
		if err := ingressguard.Startup(a.ConfigPath, f.g.manifestPath(), func(_ []ingressguard.Endpoint) error { f.closed = true; return nil }); err != nil {
			return err
		}
		if f.failReload {
			return errors.New("synthetic restart failure")
		}
		f.loaded, err = os.ReadFile(a.ConfigPath)
		return err
	})
	return f
}

func TestRealityPublicationRotationAndRecovery(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	if f.closed || f.wgClosed {
		t.Fatal("successful publication retained a guard")
	}
	uri, err := f.g.RealityConfig(ctx, f.st, f.p.ID)
	if err != nil || !strings.HasPrefix(uri, "vless://") {
		t.Fatal("applied profile unavailable", err)
	}
	parsed, parseErr := singbox.Parse(uri)
	if parseErr != nil || parsed.Outbound == nil {
		t.Fatal("delivered URI is not importable by the real parser", parseErr)
	}
	runtime, err := f.st.RealityRuntime(ctx, f.g.base)
	if err != nil {
		t.Fatal(err)
	}
	old := runtime.Users[0].UUID
	f.failReload = true
	if _, err := f.g.RotateReality(ctx, f.st, f.p.ID, f.p.RealityRevision); !errors.Is(err, api.ErrNetworkGuarded) {
		t.Fatal("failed restart claimed successful revocation", err)
	}
	if !f.closed || !f.wgClosed {
		t.Fatal("failed restart opened an ingress")
	}
	if _, err := f.g.RealityConfig(ctx, f.st, f.p.ID); !errors.Is(err, api.ErrNetworkGuarded) {
		t.Fatal("guarded profile was delivered")
	}
	runtime, err = f.st.RealityRuntime(ctx, f.g.base)
	if err != nil || len(runtime.Users) != 0 {
		t.Fatal("history restored revoked identity", err)
	}
	f.failReload = false
	if err := f.g.recover(ctx, filepath.Join(filepath.Dir(f.g.network.path), networkstate.BootstrapFile)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(f.loaded), old) {
		t.Fatal("recovery restored the revoked bearer")
	}
	current, _ := f.st.Peer(ctx, f.p.ID)
	if status := f.g.RealityStatus(ctx, f.st, current); status != "pending" {
		t.Fatal("replacement admitted without Apply", status)
	}
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	if status := f.g.RealityStatus(ctx, f.st, current); status != "available" {
		t.Fatal(status)
	}
	if _, err := f.g.RotateReality(ctx, f.st, f.p.ID, f.p.RealityRevision); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale revision accepted")
	}
}

func TestRealityForeignListenerRejectedBeforeGuardMutation(t *testing.T) {
	f := newRealityGateFixture(t)
	f.closed = false
	f.wgClosed = false
	f.g.network.uncertain = false
	f.g.network.checkPublicPort = func(ingressguard.Endpoint) error { return errors.New("occupied") }
	if _, err := f.g.ApplyCurrent(context.Background(), f.st); !errors.Is(err, errPublicPortBusy) {
		t.Fatal("occupied port was not rejected", err)
	}
	if f.closed || f.wgClosed || f.reloads != 0 {
		t.Fatal("port preflight changed the running network")
	}
	if _, err := os.Stat(f.g.manifestPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("port preflight wrote safety authority")
	}
}

func TestRealityDraftDoesNotActivateOnBackgroundRefresh(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	yes := true
	p, err := api.CreatePeer(ctx, f.st, "Pending", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p, err = f.g.PatchPeer(ctx, f.st, p.ID, store.PeerPatch{RealityEnabled: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.refreshLists(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(f.loaded), store.RealityUserName(p.ID)) {
		t.Fatal("background refresh admitted an unapplied client")
	}
	if status := f.g.RealityStatus(ctx, f.st, p); status != "pending" {
		t.Fatal(status)
	}
}

func TestConcurrentRealityRotationAndApplyNeverRestoreOldBearer(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	live, err := f.st.RealityRuntime(ctx, f.g.base)
	if err != nil {
		t.Fatal(err)
	}
	old := live.Users[0].UUID
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		go func() { <-start; _, err := f.g.RotateReality(ctx, f.st, f.p.ID, f.p.RealityRevision); results <- err }()
	}
	applied := make(chan error, 1)
	go func() { <-start; _, err := f.g.ApplyCurrent(ctx, f.st); applied <- err }()
	close(start)
	success, conflict := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			success++
		case errors.Is(err, store.ErrConflict):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if err := <-applied; err != nil {
		t.Fatal(err)
	}
	if success != 1 || conflict != 1 {
		t.Fatal("revision race was not serialized", success, conflict)
	}
	if strings.Contains(string(f.loaded), old) {
		t.Fatal("concurrent Apply restored old bearer")
	}
}

func TestIndependentRuntimeGuardRequiresCheckedRepair(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	before := f.reloads
	if err := ingressguard.Startup(f.g.configPath(), f.g.manifestPath(), func([]ingressguard.Endpoint) error { f.closed = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if pending, err := f.g.Pending(ctx, f.st); err != nil || !pending {
		t.Fatal("independent runtime guard was hidden from Apply status", err)
	}
	if err := f.g.networkStatus(); !errors.Is(err, api.ErrNetworkGuarded) {
		t.Fatal("independent runtime guard was hidden from diagnostics")
	}
	result, err := f.g.ApplyCurrent(ctx, f.st)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reloaded || f.reloads != before+1 || f.closed || f.wgClosed {
		t.Fatal("byte-identical safety repair skipped checked restart")
	}
}

func TestRealityDeliveryUsesCommittedEndpointDespiteSavedDraft(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	next, err := f.st.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next.EndpointHost = "draft.fixture.example"
	next.RealityListenPort = 9443
	next.RealityServerName = "draft-sni.fixture.example"
	if err := f.g.SaveSettings(ctx, f.st, next); err != nil {
		t.Fatal(err)
	}
	uri, err := f.g.RealityConfig(ctx, f.st, f.p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uri, "draft") || !strings.Contains(uri, "gateway.fixture.example:8443") {
		t.Fatal("draft endpoint escaped profile delivery")
	}
	profile, err := f.g.RealityClientConfig(ctx, f.st, f.p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(profile), "draft") {
		t.Fatal("draft endpoint escaped full profile delivery")
	}
}

func TestRealityReleaseRefusesOldApprovedConfigRestoredByPreStartHook(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	old, err := ingressguard.ReadConfig(f.g.configPath())
	if err != nil {
		t.Fatal(err)
	}
	oldManifest, err := ingressguard.Read(f.g.manifestPath())
	if err != nil {
		t.Fatal(err)
	}
	live, err := f.st.RealityRuntime(ctx, f.g.base)
	if err != nil {
		t.Fatal(err)
	}
	oldUUID := live.Users[0].UUID
	f.g.inner.Reloader = reloaderFunc(func(context.Context) error {
		if !f.closed || !f.wgClosed {
			t.Fatal("restart began without both barriers")
		}
		if err := os.WriteFile(f.g.configPath(), old, 0o600); err != nil {
			return err
		}
		if err := ingressguard.Startup(f.g.configPath(), f.g.manifestPath(), func([]ingressguard.Endpoint) error { f.closed = true; return nil }); err != nil {
			return err
		}
		f.loaded = old
		return nil
	})
	if _, err := f.g.RotateReality(ctx, f.st, f.p.ID, f.p.RealityRevision); !errors.Is(err, api.ErrNetworkGuarded) {
		t.Fatal("stale approved runtime config released", err)
	}
	if !f.closed || !f.wgClosed {
		t.Fatal("stale configuration opened an ingress")
	}
	manifest, err := ingressguard.Read(f.g.manifestPath())
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Guarded || len(manifest.ConfigHashes) != 2 || len(manifest.Endpoints) != len(oldManifest.Endpoints) {
		t.Fatal("failed release narrowed union safety authority")
	}
	if _, err := f.g.RealityConfig(ctx, f.st, f.p.ID); !errors.Is(err, api.ErrNetworkGuarded) {
		t.Fatal("stale runtime profile became available")
	}
	if !strings.Contains(string(f.loaded), oldUUID) {
		t.Fatal("fixture did not load the old authorized config")
	}
}

func TestRealityPartialGuardReleaseFailureReclosesBothPaths(t *testing.T) {
	for _, at := range []string{"wireguard", "public"} {
		t.Run(at, func(t *testing.T) {
			f := newRealityGateFixture(t)
			ctx := context.Background()
			if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
				t.Fatal(err)
			}
			f.g.network.guard = func(on bool) error {
				f.wgClosed = on
				if !on && at == "wireguard" {
					return errors.New("synthetic partial WG release")
				}
				return nil
			}
			f.g.network.publicGuard = func(_ []ingressguard.Endpoint, on bool) error {
				f.closed = on
				if !on && at == "public" {
					return errors.New("synthetic partial public release")
				}
				return nil
			}
			if _, err := f.g.ApplyCurrent(ctx, f.st); err == nil {
				t.Fatal("partial release claimed success")
			}
			if !f.closed || !f.wgClosed {
				t.Fatal("partial release left a path open")
			}
			if status := f.g.RealityStatus(ctx, f.st, f.p); status != "guarded" {
				t.Fatal("partial release uncertainty hidden", status)
			}
		})
	}
}
