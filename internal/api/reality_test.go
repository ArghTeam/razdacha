package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

func TestRealityPublicResponsesNeverDiscloseCredentials(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	ctx := context.Background()
	cfg := store.DefaultSettings()
	cfg.RealityEnabled = true
	cfg.RealityServerName = "fixture.example"
	cfg.RealityHandshakeServer = "127.0.0.1"
	if err := ts.st.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	p := createPeer(t, ts, cookie, "Client")
	enabled := true
	disabled := false
	if _, err := ts.st.PatchPeer(ctx, p.ID, store.PeerPatch{RealityEnabled: &enabled, WireGuardEnabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	snap, err := ts.st.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	live, err := ts.st.RealityRuntime(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(live.Users) != 1 || live.Identity == nil {
		t.Fatal("missing private fixture identity")
	}
	for _, path := range []string{"/api/peers", "/api/settings"} {
		resp := ts.auth(t, cookie, http.MethodGet, path, "")
		requireCode(t, resp, http.StatusOK)
		for _, private := range []string{live.Users[0].UUID, live.Identity.PrivateKey} {
			if strings.Contains(resp.body, private) || strings.Contains(ts.logs.String(), private) {
				t.Fatal("private REALITY material leaked")
			}
		}
	}
	resp := ts.auth(t, cookie, http.MethodGet, "/api/peers", "")
	if !strings.Contains(resp.body, `"wireguard_enabled":false`) || !strings.Contains(resp.body, `"reality_enabled":true`) || !strings.Contains(resp.body, `"reality_profile_status":"pending"`) {
		t.Fatal("missing additive transport contract", resp.body)
	}
	settings := ts.auth(t, cookie, http.MethodGet, "/api/settings", "")
	if !strings.Contains(settings.body, live.Identity.PublicKey) || !strings.Contains(settings.body, live.Identity.ShortID) {
		t.Fatal("public handshake parameters missing")
	}
}
func TestRealityDeliveryRequiresAuthenticationAndCommittedPublisher(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	p := createPeer(t, ts, cookie, "Client")
	for _, suffix := range []string{"reality-config", "reality-client-config"} {
		path := "/api/peers/" + p.ID + "/" + suffix
		requireCode(t, ts.do(t, request{method: http.MethodGet, path: path}), http.StatusUnauthorized)
		response := ts.auth(t, cookie, http.MethodGet, path, "")
		requireCode(t, response, http.StatusServiceUnavailable)
		if response.header.Get("Cache-Control") != "no-store" {
			t.Fatal("private delivery refusal is cacheable")
		}
	}
	path := "/api/peers/" + p.ID + "/reality/rotate"
	requireCode(t, ts.do(t, request{method: http.MethodPost, path: path, body: `{"expected_revision":1}`}), http.StatusUnauthorized)
	requireCode(t, ts.auth(t, cookie, http.MethodPost, path, `{}`), http.StatusBadRequest)
	requireCode(t, ts.auth(t, cookie, http.MethodPost, path, `{"expected_revision":0}`), http.StatusBadRequest)
	before, _ := ts.st.Peers(context.Background())
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/peers", `{"name":"Invalid","wireguard_enabled":false,"reality_enabled":false}`), http.StatusBadRequest)
	after, _ := ts.st.Peers(context.Background())
	if len(after) != len(before) {
		t.Fatal("rejected creation persisted a peer")
	}
}
