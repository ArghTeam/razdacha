package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/restorestate"
	"github.com/ArghTeam/razdacha/internal/store"
)

func restoreControllerFixture(t *testing.T, dir string, held bool) *restorestate.Controller {
	t.Helper()
	if e := os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	state := restorestate.State{Version: restorestate.Protocol, Operation: "fixture", Recovery: filepath.Join(dir, "recovery"), Phase: "complete", Held: held, RuntimeInhibited: true}
	if e := restorestate.WritePrivate(filepath.Join(dir, restorestate.Runtime), []byte("1\n")); e != nil {
		t.Fatal(e)
	}
	if e := restorestate.Save(dir, state); e != nil {
		t.Fatal(e)
	}
	return &restorestate.Controller{Dir: dir}
}

func TestRestoreResumeStartsActualManagerFromCurrentSavedSources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	st, e := store.Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = st.Close() }()
	var oldHits, newHits atomic.Int32
	newObserved := make(chan struct{}, 1)
	oldSink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { oldHits.Add(1); _, _ = w.Write([]byte("fixture.test\n")) }))
	defer oldSink.Close()
	newSink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newHits.Add(1)
		_, _ = w.Write([]byte("fixture.test\n"))
		select {
		case newObserved <- struct{}{}:
		default:
		}
	}))
	defer newSink.Close()
	rule, e := st.CreateRule(ctx, store.Rule{Name: "Fixture saved source", Action: store.ActionDirect, PeerScope: store.ScopeAll, RemoteLists: []string{oldSink.URL + "/old.txt"}, Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	ctl := restoreControllerFixture(t, dir, true)
	mgr := startLists(ctx, st, path, quietSlog(), ctl)
	if mgr == nil {
		t.Fatal("fixture manager unavailable")
	}
	defer func() { cancel(); mgr.Wait() }()
	g := newApplyGate(nil, &networkPublication{}, quietSlog())
	if e = registerRestoreActivity(ctx, st, mgr, nil, g, ctl); e != nil {
		t.Fatal(e)
	}
	// Audit/replace the imported URL while held, after manager construction.
	rule.RemoteLists = []string{newSink.URL + "/new.txt"}
	if e = st.UpdateRule(ctx, rule); e != nil {
		t.Fatal(e)
	}
	if oldHits.Load() != 0 || newHits.Load() != 0 {
		t.Fatal("held manager produced network requests")
	}
	if e = ctl.Resume(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-newObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("positive current-source observer saw no resumed request")
	}
	if oldHits.Load() != 0 {
		t.Fatal("resume fetched the stale imported source before syncing saved state")
	}
	if _, e = g.refreshLists(ctx); e == nil {
		t.Fatal("resumed source event published without an explicit generation")
	}
	if _, e = g.applyPools(ctx, store.Snapshot{}); e == nil {
		t.Fatal("resumed pool event published without an explicit generation")
	}
}

func TestRestoreAbortAfterAuthorityBeforeFinishCannotRecoverOnStartup(t *testing.T) {
	f := newPublicationFixture(t, nil)
	dir := filepath.Dir(f.g.network.path)
	ctl := restoreControllerFixture(t, dir, false)
	f.g.restore = ctl
	// This is a coherent candidate left by Abort after authority save but before Finish.
	gen := networkstate.Generation{Version: 2, Kind: networkstate.CommittedKind, Policy: policy("192.0.2.0/24"), Lists: map[string]lists.List{}}
	if e := networkstate.Write(f.g.network.path, gen); e != nil {
		t.Fatal(e)
	}
	if e := f.g.recoverStartup(context.Background(), filepath.Join(dir, networkstate.BootstrapFile)); e != nil {
		t.Fatal(e)
	}
	if f.reloadCount() != 0 || f.g.network.initialized || !f.isGuarded() {
		t.Fatal("inhibited startup replayed the aborted committed candidate")
	}
	s, e := ctl.Status()
	if e != nil || !s.RuntimeInhibited {
		t.Fatal("authority presence removed restore inhibition")
	}
}

func TestGlobalResetCannotShadowCanonicalRestoreState(t *testing.T) {
	dir := t.TempDir()
	ctl := restoreControllerFixture(t, dir, true)
	canonical := filepath.Join(dir, "state.db")
	alternate := filepath.Join(t.TempDir(), "shadow.db")
	calls := 0
	reset := func() error { calls++; return nil }
	if e := resetCanonicalNetwork(alternate, canonical, reset); e == nil {
		t.Fatal("alternate -db bypassed global restore protection")
	}
	if e := resetCanonicalNetwork(canonical, canonical, reset); e == nil {
		t.Fatal("held canonical reset admitted")
	}
	if calls != 0 {
		t.Fatal("global reset observer called while protected")
	}
	if e := restorestate.WritePrivate(filepath.Join(dir, restorestate.File), []byte("corrupt")); e != nil {
		t.Fatal(e)
	}
	if e := resetCanonicalNetwork(canonical, canonical, reset); e == nil || calls != 0 {
		t.Fatal("corrupt canonical state permitted global reset")
	}
	if e := os.Remove(filepath.Join(dir, restorestate.File)); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(dir, restorestate.Runtime)); e != nil {
		t.Fatal(e)
	}
	lock, e := restorestate.Acquire(ctl.Dir, -1)
	if e != nil {
		t.Fatal(e)
	}
	if e = resetCanonicalNetwork(canonical, canonical, reset); e == nil {
		t.Fatal("global reset bypassed lifecycle serialization")
	}
	_ = lock.Close()
	if e = resetCanonicalNetwork(canonical, canonical, reset); e != nil || calls != 1 {
		t.Fatal("positive unprotected canonical reset control failed")
	}
}

func TestPasswordChangeCannotAliasIncompleteCanonicalRestore(t *testing.T) {
	dir := t.TempDir()
	restoreControllerFixture(t, dir, true)
	canonical := filepath.Join(dir, "state.db")
	alias := filepath.Join(t.TempDir(), "alias.db")
	if e := os.Symlink(canonical, alias); e != nil {
		t.Fatal(e)
	}
	if lock, e := passwordLifecycle(alias, canonical); e == nil {
		_ = lock.Close()
		t.Fatal("alternate password path admitted against protected state")
	}
	state, e := restorestate.Read(dir)
	if e != nil {
		t.Fatal(e)
	}
	state.Phase = "preparing"
	if e = restorestate.WritePrivate(filepath.Join(dir, restorestate.Incomplete), []byte("1\n")); e != nil {
		t.Fatal(e)
	}
	if e = restorestate.Save(dir, *state); e != nil {
		t.Fatal(e)
	}
	if lock, e := passwordLifecycle(canonical, canonical); e == nil {
		_ = lock.Close()
		t.Fatal("password path admitted incomplete canonical DB")
	}
	if _, e = os.Lstat(canonical); !os.IsNotExist(e) {
		t.Fatal("password guard created or opened missing restored DB")
	}
}

func TestRestoreResumeStartsActualPoolManagerFromCurrentSavedCatalog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	st, e := store.Open(ctx, filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = st.Close() }()
	var oldHits, newHits atomic.Int32
	observed := make(chan struct{}, 1)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/old/") {
			oldHits.Add(1)
		} else {
			newHits.Add(1)
			select {
			case observed <- struct{}{}:
			default:
			}
		}
		_, _ = w.Write([]byte("vless://11111111-1111-4111-8111-111111111111@127.0.0.1:9?security=tls#Synthetic\n"))
	}))
	defer sink.Close()
	driver, _, e := lists.PoolDriverFor(igareckCatalogURL)
	if e != nil {
		t.Fatal(e)
	}
	undo := lists.RegisterPoolDriver("127.0.0.1", driver)
	defer undo()
	tunnel, e := st.CreateTunnel(ctx, store.Tunnel{Name: "Synthetic audited pool", Type: store.TunnelVLESS, Source: store.SourcePool, Raw: sink.URL + "/old/", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	ctl := restoreControllerFixture(t, dir, true)
	// Every attempted request is constrained to this synthetic observer even if a
	// production driver tries its fallback: no real provider hostname is reachable.
	client := &http.Client{Transport: restorePoolTransport{base: http.DefaultTransport, allowed: sink.URL}}
	pools := lists.NewPoolManager(lists.PoolManagerOptions{Allow: ctl.Allowed, Catalog: &lists.PoolCatalog{Client: client, Log: quietSlog(), CheckKey: poolKeyCheck, Pause: -1}, Writer: st, Logger: quietSlog()})
	snap, e := st.Snapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	pools.SetTunnels(lists.PoolTunnels(snap))
	defer func() { cancel(); pools.Wait() }()
	g := newApplyGate(nil, &networkPublication{}, quietSlog())
	if e = registerRestoreActivity(ctx, st, nil, pools, g, ctl); e != nil {
		t.Fatal(e)
	}
	tunnel.Raw = sink.URL + "/current/"
	if e = st.UpdateTunnel(ctx, tunnel); e != nil {
		t.Fatal(e)
	}
	if oldHits.Load() != 0 || newHits.Load() != 0 {
		t.Fatal("held pool contacted a catalog")
	}
	if e = ctl.Resume(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("current saved catalog positive observer missing")
	}
	if oldHits.Load() != 0 {
		t.Fatal("resume fetched stale imported catalog")
	}
	if _, e = g.applyPools(ctx, store.Snapshot{}); e == nil {
		t.Fatal("pool acquisition published before explicit Apply")
	}
}

type restorePoolTransport struct {
	base    http.RoundTripper
	allowed string
}

func (r restorePoolTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !strings.HasPrefix(request.URL.String(), r.allowed+"/") {
		return nil, fmt.Errorf("synthetic pool fixture rejected non-observer URL")
	}
	return r.base.RoundTrip(request)
}

func TestTrustedStartupRejectsDatabaseAliasesDuringInitialSchemaAdmission(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		t.Run("canonical"+suffix, func(t *testing.T) {
			dir := t.TempDir()
			_ = restoreControllerFixture(t, dir, true)
			path := filepath.Join(dir, "state.db")
			st, e := store.Open(context.Background(), path)
			if e != nil {
				t.Fatal(e)
			}
			if e = st.Close(); e != nil {
				t.Fatal(e)
			}
			before, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			sibling := filepath.Join(t.TempDir(), "sibling.db")
			if e = os.WriteFile(sibling, before, 0o600); e != nil {
				t.Fatal(e)
			}
			if suffix == "" {
				if e = os.Remove(path); e != nil {
					t.Fatal(e)
				}
			}
			if e = os.Symlink(sibling, path+suffix); e != nil {
				t.Fatal(e)
			}
			// The schema error proves rejection before Store.Open, controller/worker setup,
			// netstack construction or service callbacks in the actual startup sequence.
			e = run(context.Background(), "127.0.0.1:0", path, false)
			if e == nil || !strings.Contains(e.Error(), "open restore schema:") {
				t.Fatalf("startup did not reject at initial schema boundary: %v", e)
			}
			after, e := os.ReadFile(sibling)
			if e != nil || string(after) != string(before) {
				t.Fatal("startup changed private sibling")
			}
			state, e := restorestate.Read(dir)
			if e != nil || state == nil || !state.Held || !state.RuntimeInhibited || state.Phase != "complete" {
				t.Fatal("startup changed trusted safety state")
			}
		})
	}
}
