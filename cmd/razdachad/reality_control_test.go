package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/ingressguard"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestRealityInvalidRevocationsDoNotGuardOrChangeAuthentication(t *testing.T) {
	for _, kind := range []string{"last-method", "empty-name", "missing-exit", "settings-mtu"} {
		t.Run(kind, func(t *testing.T) {
			f := newRealityGateFixture(t)
			ctx := context.Background()
			no := false
			if kind == "last-method" {
				var err error
				f.p, err = f.g.PatchPeer(ctx, f.st, f.p.ID, store.PeerPatch{WireGuardEnabled: &no})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
				t.Fatal(err)
			}
			before, err := f.st.RealityDelivery(ctx, f.p.ID)
			if err != nil {
				t.Fatal(err)
			}
			epoch, reloads := f.g.activityEpoch, f.reloads
			var changeErr error
			switch kind {
			case "settings-mtu":
				cfg := before.Settings
				cfg.RealityEnabled = false
				cfg.ClientMTU = store.MinClientMTU - 1
				changeErr = f.g.SaveSettings(ctx, f.st, cfg)
			default:
				patch := store.PeerPatch{RealityEnabled: &no}
				if kind == "empty-name" {
					empty := " "
					patch.Name = &empty
				}
				if kind == "missing-exit" {
					missing := "missing"
					patch.SetEgressTunnel = true
					patch.EgressTunnelID = &missing
				}
				_, changeErr = f.g.PatchPeer(ctx, f.st, f.p.ID, patch)
			}
			if !errors.Is(changeErr, store.ErrInvalid) {
				t.Fatal("expected validation rejection", changeErr)
			}
			after, err := f.st.RealityDelivery(ctx, f.p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) || f.closed || f.wgClosed || f.g.network.uncertain || f.g.activityEpoch != epoch || f.reloads != reloads {
				t.Fatal("invalid request affected admission or ingress")
			}
			manifest, err := ingressguard.Read(f.g.manifestPath())
			if err != nil || manifest.Guarded {
				t.Fatal("invalid request persisted guard intent", err)
			}
		})
	}
}

func TestRealityStorageFailureAfterGuardRemainsClosed(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(filepath.Dir(f.g.network.path), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TRIGGER fail_settings BEFORE INSERT ON settings BEGIN SELECT RAISE(ABORT, 'synthetic write failure'); END`); err != nil {
		t.Fatal(err)
	}
	cfg, _ := f.st.Settings(ctx)
	cfg.RealityEnabled = false
	if err := f.g.SaveSettings(ctx, f.st, cfg); err == nil {
		t.Fatal("write failure accepted")
	}
	if !f.closed || !f.wgClosed || !f.g.network.uncertain {
		t.Fatal("potentially durable write failure reopened ingress")
	}
	if _, err := f.g.RealityProfile(ctx, f.st, f.p.ID); !errors.Is(err, api.ErrNetworkGuarded) {
		t.Fatal("guarded profile exported", err)
	}
}

func TestRealitySettingsPatchMergesAfterConcurrentDisable(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	old, err := f.st.RealityDelivery(ctx, f.p.ID)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, started := make(chan struct{}), make(chan struct{}), make(chan struct{})
	disabled, unrelated := make(chan error, 1), make(chan error, 1)
	go func() {
		_, _, err := f.g.PatchSettings(ctx, f.st, func(v store.Settings) store.Settings { close(entered); <-release; v.RealityEnabled = false; return v })
		disabled <- err
	}()
	<-entered
	var response store.Settings
	go func() {
		close(started)
		_, saved, err := f.g.PatchSettings(ctx, f.st, func(v store.Settings) store.Settings {
			if v.RealityEnabled {
				t.Error("partial merge received stale enabled state")
			}
			v.LogLevel = "error"
			return v
		})
		response = saved
		unrelated <- err
	}()
	<-started
	close(release)
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	if err := <-unrelated; err != nil {
		t.Fatal(err)
	}
	saved, err := f.st.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if saved.RealityEnabled || saved.LogLevel != "error" || !reflect.DeepEqual(response, saved) {
		t.Fatal("partial save lost disabled intent or returned stale state")
	}
	enabled := saved
	enabled.RealityEnabled = true
	if err := f.g.SaveSettings(ctx, f.st, enabled); err != nil {
		t.Fatal(err)
	}
	fresh, err := f.st.RealityDelivery(ctx, f.p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.UUID == old.UUID || fresh.Peer.RealityRevision <= old.Peer.RealityRevision {
		t.Fatal("concurrent patch resurrected a credential")
	}
	runtime, err := f.st.RealityRuntime(ctx, f.g.base)
	if err != nil || len(runtime.Users) != 0 {
		t.Fatal("revoked admission resurrected", err)
	}
}

func TestRealityInvalidDisableHTTPReturns400WithoutGuard(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	no := false
	var err error
	f.p, err = f.g.PatchPeer(ctx, f.st, f.p.ID, store.PeerPatch{WireGuardEnabled: &no})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := api.CreatePeer(ctx, f.st, "Unaffected client", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	sibling, err = f.g.PatchPeer(ctx, f.st, sibling.ID, store.PeerPatch{RealityEnabled: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	password := make([]byte, 32)
	if _, err := rand.Read(password); err != nil {
		t.Fatal(err)
	}
	secret := hex.EncodeToString(password)
	if err := api.SetPassword(ctx, f.st, secret); err != nil {
		t.Fatal(err)
	}
	server, err := api.New(ctx, api.Config{Store: f.st, Applier: f.g, Logger: quietSlog()})
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	payload, err := json.Marshal(map[string]string{"password": secret})
	if err != nil {
		t.Fatal(err)
	}
	server.Handler().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(payload)))
	if login.Code != http.StatusOK {
		t.Fatal("synthetic login failed", login.Code)
	}
	result := login.Result()
	cookies := result.Cookies()
	_ = result.Body.Close()
	before, err := f.st.RealityDelivery(ctx, sibling.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []struct{ path, body string }{
		{"/api/peers/" + f.p.ID, `{"reality_enabled":false}`},
		{"/api/settings", `{"reality_enabled":false,"client_mtu":1279}`},
	} {
		r := httptest.NewRequest(http.MethodPatch, req.path, strings.NewReader(req.body))
		for _, c := range cookies {
			r.AddCookie(c)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, r)
		if response.Code != http.StatusBadRequest {
			t.Fatal("invalid disable did not return 400", response.Code)
		}
		after, err := f.st.RealityDelivery(ctx, sibling.ID)
		if err != nil {
			t.Fatal(err)
		}
		if f.closed || f.wgClosed || !reflect.DeepEqual(before, after) {
			t.Fatal("invalid HTTP request affected another client")
		}
	}
}
