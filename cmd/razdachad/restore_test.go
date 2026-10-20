package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
