package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/store"
)

func nativePublication(t *testing.T) (*publicationFixture, *lists.Manager, *atomic.Int32, string) {
	t.Helper()
	f := newPublicationFixture(t, nil)
	phase := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch phase.Load() {
		case 0:
			fmt.Fprint(w, `{"version":3,"rules":[{"domain":["old.example"],"ip_cidr":["192.0.2.0/24"]}]}`)
		case 1:
			fmt.Fprint(w, `{"version":3,"rules":[{"domain":["new.example"],"ip_cidr":["203.0.113.0/24"]}]}`)
		default:
			fmt.Fprint(w, `{"version":3,"rules":[{"domain":["bad.example"],"network":["tcp"]}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	fetcher, err := lists.NewFetcher(lists.Options{Dir: filepath.Join(filepath.Dir(f.g.network.path), "lists"), Client: srv.Client(), Logger: quietSlog()})
	if err != nil {
		t.Fatal(err)
	}
	mgr := lists.NewManager(lists.ManagerOptions{Fetcher: fetcher, Logger: quietSlog()})
	url := srv.URL + "/native.json"
	mgr.SetSources([]lists.Source{{URL: url, Format: lists.FormatJSON}})
	if err = mgr.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.g.network.manager = mgr
	return f, mgr, phase, url
}

func TestNativePublicationPinsBothProjectionsAndRecovery(t *testing.T) {
	ctx := context.Background()
	f, mgr, phase, url := nativePublication(t)
	snap := policy()
	snap.Rules[0].RemoteLists = []string{url}
	if _, err := f.g.Apply(ctx, snap); err != nil {
		t.Fatal(err)
	}
	// Existing v3 envelopes with projection-v1 authority retain their legacy
	// verification contract and recover without acquisition or editable replay.
	legacy, err := readGeneration(f.g.network.path)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Version = 3
	legacy.Lists[url].Native.Version = 1
	if err = writeGeneration(f.g.network.path, legacy); err != nil {
		t.Fatal(err)
	}
	f.g.network.uncertain = true
	if err = f.g.recover(ctx, filepath.Join(filepath.Dir(f.g.network.path), "absent-legacy-seed")); err != nil {
		t.Fatal(err)
	}
	if f.isGuarded() {
		t.Fatal("legacy supported authority stayed guarded")
	}
	old := f.g.network.contents[url]
	oldBody, err := os.ReadFile(old.Native.Path)
	if err != nil {
		t.Fatal(err)
	}
	phase.Store(1)
	f.reloadHook = func() { phase.Store(2); _ = mgr.Refresh(ctx) }
	if err := mgr.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.refreshLists(ctx); err != nil {
		t.Fatal(err)
	}
	current := f.g.network.contents[url]
	if current.Native.Hash == old.Native.Hash || !reflect.DeepEqual(current.Subnets, []string{"203.0.113.0/24"}) {
		t.Fatal("native publication did not pin accepted new generation")
	}
	oldAfter, err := os.ReadFile(old.Native.Path)
	if err != nil || !bytes.Equal(oldAfter, oldBody) {
		t.Fatal("prepared publication overwrote recovery blob")
	}
	var runtime map[string]any
	if err = json.Unmarshal(f.runtime, &runtime); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(f.runtime, []byte(`"type": "remote"`)) || bytes.Contains(f.runtime, []byte(url)) {
		t.Fatal("autonomous native source remains in runtime")
	}
	gen, err := readGeneration(f.g.network.path)
	if err != nil || gen.Version != 4 || gen.Lists[url].Native.Hash != current.Native.Hash {
		t.Fatalf("authority not pinned: %v", err)
	}
	// Refreshed malformed candidate did not advance accepted/cache authority.
	if state := mgr.States()[url]; state.Err == "" || !state.Cached {
		t.Fatal("unsupported candidate error hidden")
	}
	f.reloadHook = nil
	f.guarded = true
	f.g.network.uncertain = true
	if err = f.g.recover(ctx, filepath.Join(filepath.Dir(f.g.network.path), "absent-seed")); err != nil {
		t.Fatal(err)
	}
	if f.isGuarded() || f.g.network.contents[url].Native.Hash != current.Native.Hash {
		t.Fatal("recovery used fetched/pending content")
	}
	// A hash/permission failure during publication cannot release ingress.
	f.reloadHook = func() { _ = os.Chmod(current.Native.DNSPath, 0o644) }
	f.g.network.uncertain = true
	if _, err = f.g.Apply(ctx, snap); err == nil || !f.isGuarded() {
		t.Fatal("changed DNS artifact escaped publication guard")
	}
}

func TestNativeLegacyAuthorityKeepsAdminAndRequiresApply(t *testing.T) {
	ctx := context.Background()
	f, mgr, _, url := nativePublication(t)
	snap := policy()
	snap.Rules[0].RemoteLists = []string{url}
	snap.Settings.WGServerAddress = "10.8.0.10"
	if err := writeGeneration(f.g.network.path, networkGeneration{Version: 1, Policy: snap, Lists: map[string]lists.List{}}); err != nil {
		t.Fatal(err)
	}
	if err := f.g.recover(ctx, filepath.Join(filepath.Dir(f.g.network.path), "absent-seed")); err != nil {
		t.Fatal(err)
	}
	if !f.isGuarded() || f.g.network.initialized {
		t.Fatal("legacy native authority released or silently restored")
	}
	st, db := openStore(t)
	pending := snap.Settings
	pending.WGServerAddress = "10.8.0.254"
	if err := st.SaveSettings(ctx, pending); err != nil {
		t.Fatal(err)
	}
	// initialWGSettings reads the committed authority, rather than editable DB.
	ownDB := filepath.Join(filepath.Dir(f.g.network.path), filepath.Base(db))
	settings, err := initialWGSettings(ctx, st, ownDB)
	if err != nil || settings.WGServerAddress != snap.Settings.WGServerAddress {
		t.Fatalf("legacy administrative WG changed: %+v %v", settings, err)
	}
	f.g.network.manager = mgr
	if _, err := f.g.Apply(ctx, snap); err != nil {
		t.Fatal(err)
	}
	if f.isGuarded() || !f.g.network.initialized {
		t.Fatal("explicit native Apply failed to initialize")
	}
	gen, err := networkstate.Read(f.g.network.path, networkstate.CommittedKind)
	if err != nil || gen.Version != 4 {
		t.Fatal("explicit Apply did not upgrade native authority")
	}
}

func TestNativeNeverLoadedOrMissingRecoveryArtifactStaysGuarded(t *testing.T) {
	ctx := context.Background()
	f := newPublicationFixture(t, nil)
	snap := policy()
	snap.Rules[0].RemoteLists = []string{"https://example.invalid/native.srs"}
	if _, err := f.g.Apply(ctx, snap); err == nil || !f.isGuarded() || len(f.events) != 0 {
		t.Fatal("missing native mutated network")
	}
	f, mgr, _, url := nativePublication(t)
	snap = policy()
	snap.Rules[0].RemoteLists = []string{url}
	if _, err := f.g.Apply(ctx, snap); err != nil {
		t.Fatal(err)
	}
	current := f.g.network.contents[url]
	if err := os.Remove(current.Native.Path); err != nil {
		t.Fatal(err)
	}
	f.guarded = true
	f.g.network.uncertain = true
	f.g.network.manager = mgr
	if err := f.g.recover(ctx, filepath.Join(filepath.Dir(f.g.network.path), "absent-seed")); err == nil || !f.isGuarded() {
		t.Fatal("missing immutable recovery blob released ingress")
	}
}

func TestActiveNativeCorruptionClosesGuardBeforeCandidateValidation(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprintf("remove=%v", remove), func(t *testing.T) {
			ctx := context.Background()
			f, mgr, phase, url := nativePublication(t)
			snap := policy()
			snap.Rules[0].RemoteLists = []string{url}
			if _, err := f.g.Apply(ctx, snap); err != nil {
				t.Fatal(err)
			}
			phase.Store(2)
			if err := mgr.Refresh(ctx); err == nil {
				t.Fatal("invalid native candidate unexpectedly accepted")
			}
			if _, err := f.g.refreshLists(ctx); err != nil || f.isGuarded() {
				t.Fatal("unsupported new candidate guarded healthy last-good")
			}
			active := f.g.network.contents[url].Native
			if err := os.Chmod(active.Path, 0o644); err != nil {
				t.Fatal(err)
			}
			if remove {
				snap.Rules[0].RemoteLists = nil
			}
			f.setFailure("check")
			if _, err := f.g.Apply(ctx, snap); err == nil || !f.isGuarded() || !f.g.network.uncertain {
				t.Fatal("active corruption stayed open")
			}
			// A new accepted immutable generation (or explicit removal) can recover;
			// the damaged old watched path is never repaired in place.
			f.setFailure("")
			phase.Store(1)
			if err := mgr.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := f.g.Apply(ctx, snap); err != nil || f.isGuarded() {
				t.Fatalf("validated replacement did not recover: %v", err)
			}
		})
	}
}

func TestNativeRelativeDatabaseApplyAndFreshRecovery(t *testing.T) {
	ctx := context.Background()
	f, mgr, _, url := nativePublication(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	absoluteManifest := f.g.network.path
	relativeManifest, err := filepath.Rel(cwd, absoluteManifest)
	if err != nil {
		t.Fatal(err)
	}
	relativeDB := filepath.Join(filepath.Dir(relativeManifest), "relative-state.db")
	st, err := store.Open(ctx, relativeDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err = st.SaveSettings(ctx, policy().Settings); err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateRule(ctx, store.Rule{Name: "Relative native policy", Enabled: true, PeerScope: store.ScopeAll, Action: store.ActionBlock, RemoteLists: []string{url}}); err != nil {
		t.Fatal(err)
	}
	f.g.network.path = relativeManifest
	if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatalf("relative-db native Apply: %v", err)
	}
	gen, err := readGeneration(absoluteManifest)
	if err != nil || gen.Lists[url].Native == nil {
		t.Fatalf("relative-db authority missing: %v", err)
	}
	// A fresh gate recovers from the relative authority path without editable DB
	// fallback or a changed working directory; the original cache paths stay absolute.
	fresh := newPublicationFixture(t, mgr)
	fresh.g.network.path = relativeManifest
	if err = fresh.g.recover(ctx, filepath.Join(filepath.Dir(relativeDB), networkstate.BootstrapFile)); err != nil {
		t.Fatalf("relative-db fresh recovery: %v", err)
	}
	if fresh.isGuarded() || fresh.g.network.contents[url].Native.Hash != gen.Lists[url].Native.Hash {
		t.Fatal("relative-db recovery did not restore exact authority")
	}
	if _, err = fresh.g.refreshLists(ctx); err != nil {
		t.Fatalf("relative-db healthy no-op: %v", err)
	}
	if err = os.Chmod(gen.Lists[url].Native.Path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.g.refreshLists(ctx); err == nil || !fresh.isGuarded() || !fresh.g.network.uncertain {
		t.Fatal("relative-db active artifact failure did not close ingress")
	}
}

func TestNativeTransportProjectionTamperingGuardsActivePolicy(t *testing.T) {
	ctx := context.Background()
	f, _, _, url := nativePublication(t)
	snap := policy()
	snap.Rules[0].RemoteLists = []string{url}
	if _, err := f.g.Apply(ctx, snap); err != nil {
		t.Fatal(err)
	}
	current := f.g.network.contents[url]
	current.TransportSubnets = []string{"203.0.113.0/24"}
	f.g.network.contents[url] = current
	if _, err := f.g.Apply(ctx, snap); err == nil || !f.isGuarded() || !f.g.network.uncertain {
		t.Fatal("altered transport projection retained open ingress")
	}
}
