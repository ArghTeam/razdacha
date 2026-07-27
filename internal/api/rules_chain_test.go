package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

// chainTunnels заводит обычный туннель и WARP: первое звено цепи и допустимое
// второе.
func chainTunnels(t *testing.T, ts *testServer) (first, warp store.Tunnel) {
	t.Helper()
	ctx := context.Background()

	var err error
	first, err = ts.st.CreateTunnel(ctx, store.Tunnel{
		Name: "Амстердам", Type: store.TunnelVLESS, Source: store.SourceURL,
		Raw: "vless://…", Parsed: json.RawMessage(`{"server":"1.2.3.4"}`), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	warp, err = ts.st.CreateTunnel(ctx, store.Tunnel{
		Name: "WARP", Type: store.TunnelWireGuard, Source: store.SourceWARP,
		Raw: "[Interface]", Parsed: json.RawMessage(`{"private_key":"x"}`), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateTunnel WARP: %v", err)
	}
	return first, warp
}

func createRule(t *testing.T, ts *testServer, cookie *http.Cookie, body string) response {
	t.Helper()
	return ts.do(t, request{
		method: http.MethodPost, path: "/api/rules", body: body,
		cookies: []*http.Cookie{cookie},
	})
}

// Второе звено — WARP: правило создаётся и поле возвращается как есть.
func TestRuleChainAccepted(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	first, warp := chainTunnels(t, ts)

	res := createRule(t, ts, cookie, `{"name":"YouTube","action":"tunnel","tunnel_id":"`+
		first.ID+`","via_tunnel_id":"`+warp.ID+`","domains":["youtube.com"],"peer_scope":"all"}`)
	if res.code != http.StatusCreated {
		t.Fatalf("код %d, тело %s", res.code, res.body)
	}
	var got store.Rule
	if err := json.Unmarshal([]byte(res.body), &got); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if got.ViaTunnelID != warp.ID {
		t.Errorf("via_tunnel_id = %q, ожидался %q", got.ViaTunnelID, warp.ID)
	}
}

// Второе звено без source = warp отклоняется на русском — до генерации конфига
// и `sing-box check` (ADR 0012).
func TestRuleChainRejectsNonWARP(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	first, _ := chainTunnels(t, ts)

	res := createRule(t, ts, cookie, `{"name":"YouTube","action":"tunnel","tunnel_id":"`+
		first.ID+`","via_tunnel_id":"`+first.ID+`","domains":["youtube.com"],"peer_scope":"all"}`)
	if res.code != http.StatusBadRequest {
		t.Fatalf("код %d, ожидался 400; тело %s", res.code, res.body)
	}
	if !strings.Contains(res.body, "WARP") || !strings.Contains(res.body, "Амстердам") {
		t.Errorf("в отказе нет причины и имени туннеля: %s", res.body)
	}
}

// Второе звено в никуда — 404, а не молчаливое сохранение ссылки.
func TestRuleChainUnknownTunnel(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	first, _ := chainTunnels(t, ts)

	res := createRule(t, ts, cookie, `{"name":"YouTube","action":"tunnel","tunnel_id":"`+
		first.ID+`","via_tunnel_id":"нет-такого","domains":["youtube.com"],"peer_scope":"all"}`)
	if res.code != http.StatusNotFound {
		t.Fatalf("код %d, ожидался 404; тело %s", res.code, res.body)
	}
}

// PATCH проверяется так же: цепь навешивается и снимается через обновление.
func TestRuleChainCheckedOnUpdate(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	first, warp := chainTunnels(t, ts)

	res := createRule(t, ts, cookie, `{"name":"YouTube","action":"tunnel","tunnel_id":"`+
		first.ID+`","domains":["youtube.com"],"peer_scope":"all"}`)
	if res.code != http.StatusCreated {
		t.Fatalf("код %d, тело %s", res.code, res.body)
	}
	var created store.Rule
	if err := json.Unmarshal([]byte(res.body), &created); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}

	bad := ts.do(t, request{
		method: http.MethodPatch, path: "/api/rules/" + created.ID,
		body:    `{"via_tunnel_id":"` + first.ID + `"}`,
		cookies: []*http.Cookie{cookie},
	})
	if bad.code != http.StatusBadRequest {
		t.Fatalf("код %d, ожидался 400; тело %s", bad.code, bad.body)
	}

	good := ts.do(t, request{
		method: http.MethodPatch, path: "/api/rules/" + created.ID,
		body:    `{"via_tunnel_id":"` + warp.ID + `"}`,
		cookies: []*http.Cookie{cookie},
	})
	if good.code != http.StatusOK {
		t.Fatalf("код %d, тело %s", good.code, good.body)
	}
	if !strings.Contains(good.body, warp.ID) {
		t.Errorf("второе звено не сохранилось: %s", good.body)
	}
}
