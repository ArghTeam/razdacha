package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
)

// withClash подменяет серверу клиент Clash API подставным. Настоящего sing-box
// в тестах нет: проверяется, как панель переводит его ответы пользователю.
func withClash(t *testing.T, ts *testServer, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ts.Server.clash = clash.New(clash.Options{
		Addr:         srv.Listener.Addr().String(),
		ProbeTimeout: 100 * time.Millisecond,
		HTTPClient:   &http.Client{Timeout: 300 * time.Millisecond},
	})
}

// TestCheckTunnelRequiresSession — маршрут проверки закрыт сессией, как и весь
// остальной `/api/`.
func TestCheckTunnelRequiresSession(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do(t, request{method: http.MethodPost, path: "/api/tunnels/t1/check"})
	requireCode(t, resp, http.StatusUnauthorized)
	if code := decodeError(t, resp).Code; code != codeUnauthorized {
		t.Errorf("код ошибки = %q", code)
	}
}

// TestCheckTunnelOK — удачная проверка: тег строится как в генераторе, задержка
// доходит до ответа и оседает в производных полях списка.
func TestCheckTunnelOK(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Нидерланды")

	var gotPath string
	withClash(t, ts, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if _, err := w.Write([]byte(`{"delay":37}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/check", "")
	requireCode(t, resp, http.StatusOK)
	var out tunnelCheckResponse
	decodeJSONBody(t, resp, &out)

	if out.Status != tunnelUp {
		t.Errorf("статус = %q, ожидался %q", out.Status, tunnelUp)
	}
	if out.LatencyMS == nil || *out.LatencyMS != 37 {
		t.Errorf("задержка = %v, ожидалось 37", out.LatencyMS)
	}
	if want := "/proxies/tun-" + tun.ID + "/delay"; gotPath != want {
		t.Errorf("Clash API спросили о %q, ожидалось %q", gotPath, want)
	}

	list := listTunnels(t, ts, cookie)
	if len(list) != 1 {
		t.Fatalf("туннелей в списке %d", len(list))
	}
	got := list[0]
	if got.Status == nil || *got.Status != tunnelUp {
		t.Fatalf("статус в списке = %v", got.Status)
	}
	if got.LatencyMS == nil || *got.LatencyMS != 37 {
		t.Errorf("задержка в списке = %v", got.LatencyMS)
	}
	if got.LastCheck == nil || !got.LastCheck.Equal(ts.clock().UTC()) {
		t.Errorf("время проверки = %v", got.LastCheck)
	}
}

// TestCheckTunnelNotApplied — туннель есть в БД, но не в работающем конфиге.
// Ответ обязан объяснять это словами, а не выглядеть как обрыв сети.
func TestCheckTunnelNotApplied(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Свежий")

	withClash(t, ts, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		if _, err := w.Write([]byte(`{"message":"proxy not found"}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/check", "")
	requireCode(t, resp, http.StatusOK)
	var out tunnelCheckResponse
	decodeJSONBody(t, resp, &out)

	if out.Status != tunnelNotApplied {
		t.Errorf("статус = %q, ожидался %q", out.Status, tunnelNotApplied)
	}
	if out.LatencyMS != nil {
		t.Errorf("задержка = %v, ожидался null", *out.LatencyMS)
	}
	if out.Detail == "" {
		t.Error("«не применён» без объяснения")
	}
}

// TestCheckTunnelDown — туннель применён, но цель через него не открылась.
func TestCheckTunnelDown(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Резервный")

	withClash(t, ts, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := w.Write([]byte(`{"message":"An error occurred in the delay test"}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/check", "")
	requireCode(t, resp, http.StatusOK)
	var out tunnelCheckResponse
	decodeJSONBody(t, resp, &out)

	if out.Status != tunnelDown {
		t.Errorf("статус = %q, ожидался %q", out.Status, tunnelDown)
	}
	if out.LatencyMS != nil {
		t.Errorf("у неотвечающего туннеля задержка %v", *out.LatencyMS)
	}
}

// TestCheckTunnelSingboxSilent — sing-box не отвечает вовсе: 503 с внятным
// текстом, а список туннелей продолжает отдавать null, а не выдуманный «down».
func TestCheckTunnelSingboxSilent(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Нидерланды")

	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.Listener.Addr().String()
	srv.Close()
	ts.Server.clash = clash.New(clash.Options{Addr: addr, ProbeTimeout: 100 * time.Millisecond})

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/check", "")
	requireCode(t, resp, http.StatusServiceUnavailable)
	e := decodeError(t, resp)
	if e.Code != codeNotReady {
		t.Errorf("код ошибки = %q, ожидался %q", e.Code, codeNotReady)
	}
	if e.Error == "" {
		t.Error("пустой текст ошибки")
	}

	list := listTunnels(t, ts, cookie)
	if list[0].Status != nil || list[0].LatencyMS != nil || list[0].LastCheck != nil {
		t.Errorf("непроверенный туннель получил производные поля: %+v", list[0])
	}
}

// TestCheckTunnelTimeout — Clash API принял соединение и замолчал. Панель не
// должна висеть на этом запросе.
func TestCheckTunnelTimeout(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Медленный")

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	withClash(t, ts, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	})

	start := time.Now()
	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/check", "")
	requireCode(t, resp, http.StatusServiceUnavailable)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("проверка заняла %s — дедлайн не сработал", elapsed)
	}
}

// TestCheckTunnelDisabled — выключенный туннель в конфиг не попадает, и
// спрашивать о нём sing-box незачем.
func TestCheckTunnelDisabled(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Выключенный")

	asked := false
	withClash(t, ts, func(w http.ResponseWriter, _ *http.Request) {
		asked = true
		w.WriteHeader(http.StatusOK)
	})

	resp := ts.auth(t, cookie, http.MethodPatch, "/api/tunnels/"+tun.ID, `{"enabled":false}`)
	requireCode(t, resp, http.StatusOK)

	resp = ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/check", "")
	requireCode(t, resp, http.StatusOK)
	var out tunnelCheckResponse
	decodeJSONBody(t, resp, &out)

	if out.Status != tunnelNotApplied {
		t.Errorf("статус = %q, ожидался %q", out.Status, tunnelNotApplied)
	}
	if asked {
		t.Error("о выключенном туннеле спросили sing-box")
	}
}

// TestCheckTunnelMissing — проверка несуществующего туннеля — 404 хранилища, а
// не поход в Clash API.
func TestCheckTunnelMissing(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/нетакого/check", "")
	requireCode(t, resp, http.StatusNotFound)
}

// TestCheckForgottenAfterDelete — удалённый туннель не оставляет за собой
// результат проверки.
func TestCheckForgottenAfterDelete(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Нидерланды")

	withClash(t, ts, func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"delay":11}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/check", ""), http.StatusOK)
	requireCode(t, ts.auth(t, cookie, http.MethodDelete, "/api/tunnels/"+tun.ID, ""), http.StatusOK)

	listTunnels(t, ts, cookie)
	if _, ok := ts.Server.checks.get(tun.ID); ok {
		t.Error("результат проверки пережил удаление туннеля")
	}
}

// listTunnels читает `GET /api/tunnels`.
func listTunnels(t *testing.T, ts *testServer, cookie *http.Cookie) []tunnelResponse {
	t.Helper()
	resp := ts.auth(t, cookie, http.MethodGet, "/api/tunnels", "")
	requireCode(t, resp, http.StatusOK)
	var out []tunnelResponse
	decodeJSONBody(t, resp, &out)
	return out
}
