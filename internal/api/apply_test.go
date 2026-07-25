package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// fakeApplier — подмена применения: тестам не нужны ни /etc/sing-box, ни
// рантайм, ни systemd.
type fakeApplier struct {
	res   singbox.ApplyResult
	err   error
	calls int
	snap  store.Snapshot
}

func (a *fakeApplier) Apply(_ context.Context, snap store.Snapshot) (singbox.ApplyResult, error) {
	a.calls++
	a.snap = snap
	return a.res, a.err
}

// withApplier подставляет применятель уже собранному тестовому серверу.
func withApplier(ts *testServer, a Applier) *testServer {
	ts.applier = a
	return ts
}

func decodeApply(t *testing.T, resp response) applyResponse {
	t.Helper()
	var out applyResponse
	if err := json.Unmarshal([]byte(resp.body), &out); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	return out
}

func TestApplyRequiresSession(t *testing.T) {
	ap := &fakeApplier{}
	ts := withApplier(newTestServer(t), ap)

	resp := ts.do(t, request{method: http.MethodPost, path: "/api/apply"})
	if resp.code != http.StatusUnauthorized {
		t.Fatalf("код %d, ожидался 401 (%s)", resp.code, resp.body)
	}
	if ap.calls != 0 {
		t.Fatal("конфиг применён без сессии")
	}
	if got := decodeError(t, resp).Code; got != codeUnauthorized {
		t.Fatalf("код ошибки %q, ожидался %q", got, codeUnauthorized)
	}
}

func TestApplyOK(t *testing.T) {
	ap := &fakeApplier{res: singbox.ApplyResult{
		Path: singbox.DefaultConfigPath, Changed: true, Reloaded: true,
	}}
	ts := withApplier(newTestServer(t), ap)
	cookie := ts.login(t)

	resp := ts.do(t, request{
		method: http.MethodPost, path: "/api/apply", cookies: []*http.Cookie{cookie},
	})
	if resp.code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200 (%s)", resp.code, resp.body)
	}
	got := decodeApply(t, resp)
	if !got.Changed || !got.Reloaded {
		t.Fatalf("changed=%v reloaded=%v, ожидалось true/true", got.Changed, got.Reloaded)
	}
	if got.Path != singbox.DefaultConfigPath {
		t.Fatalf("путь %q", got.Path)
	}
	if got.Detail == "" {
		t.Fatal("пустое описание результата: его показывает панель")
	}
	if ap.calls != 1 {
		t.Fatalf("Apply вызван %d раз, ожидался 1", ap.calls)
	}
}

func TestApplyUnchanged(t *testing.T) {
	ap := &fakeApplier{res: singbox.ApplyResult{Path: singbox.DefaultConfigPath}}
	ts := withApplier(newTestServer(t), ap)
	cookie := ts.login(t)

	resp := ts.do(t, request{
		method: http.MethodPost, path: "/api/apply", cookies: []*http.Cookie{cookie},
	})
	if resp.code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200 (%s)", resp.code, resp.body)
	}
	got := decodeApply(t, resp)
	if got.Changed || got.Reloaded {
		t.Fatalf("changed=%v reloaded=%v, ожидалось false/false", got.Changed, got.Reloaded)
	}
	if !strings.Contains(got.Detail, "не изменилась") {
		t.Fatalf("описание %q не говорит, что применять было нечего", got.Detail)
	}
}

func TestApplyCheckFailureIs422(t *testing.T) {
	const detail = "route rule 0: unknown outbound tag"
	ap := &fakeApplier{err: fmt.Errorf("%w: %s", singbox.ErrCheckFailed, detail)}
	ts := withApplier(newTestServer(t), ap)
	cookie := ts.login(t)

	resp := ts.do(t, request{
		method: http.MethodPost, path: "/api/apply", cookies: []*http.Cookie{cookie},
	})
	if resp.code != http.StatusUnprocessableEntity {
		t.Fatalf("код %d, ожидался 422 (%s)", resp.code, resp.body)
	}
	got := decodeError(t, resp)
	if got.Code != codeInvalidConfig {
		t.Fatalf("код ошибки %q, ожидался %q", got.Code, codeInvalidConfig)
	}
	// Текст рантайма доходит до пользователя целиком: по коду причину не понять.
	if !strings.Contains(got.Error, detail) {
		t.Fatalf("текст %q не содержит причины отказа", got.Error)
	}
	if !strings.Contains(got.Error, "не прошёл проверку") {
		t.Fatalf("текст %q не на русском", got.Error)
	}
}

func TestApplyReloadFailureIs500(t *testing.T) {
	ap := &fakeApplier{err: fmt.Errorf("%w: unit not found", singbox.ErrReloadFailed)}
	ts := withApplier(newTestServer(t), ap)
	cookie := ts.login(t)

	resp := ts.do(t, request{
		method: http.MethodPost, path: "/api/apply", cookies: []*http.Cookie{cookie},
	})
	if resp.code != http.StatusInternalServerError {
		t.Fatalf("код %d, ожидался 500 (%s)", resp.code, resp.body)
	}
	if got := decodeError(t, resp).Code; got != codeInternal {
		t.Fatalf("код ошибки %q, ожидался %q", got, codeInternal)
	}
}

func TestApplyGenerateFailureIs422(t *testing.T) {
	ap := &fakeApplier{err: errors.New("правило «YouTube» ссылается на несуществующий туннель")}
	ts := withApplier(newTestServer(t), ap)
	cookie := ts.login(t)

	resp := ts.do(t, request{
		method: http.MethodPost, path: "/api/apply", cookies: []*http.Cookie{cookie},
	})
	if resp.code != http.StatusUnprocessableEntity {
		t.Fatalf("код %d, ожидался 422 (%s)", resp.code, resp.body)
	}
	if got := decodeError(t, resp).Error; !strings.Contains(got, "несуществующий туннель") {
		t.Fatalf("текст %q не дошёл до пользователя", got)
	}
}

func TestApplyPassesCurrentState(t *testing.T) {
	ap := &fakeApplier{}
	ts := withApplier(newTestServer(t), ap)
	cookie := ts.login(t)

	if _, err := ts.st.CreateTunnel(context.Background(), store.Tunnel{
		Name: "Амстердам", Type: store.TunnelVLESS, Source: store.SourceURL,
		Raw: "vless://…", Enabled: true,
		Parsed: json.RawMessage(`{"server":"1.2.3.4","server_port":443,` +
			`"uuid":"00000000-0000-0000-0000-000000000000"}`),
	}); err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	resp := ts.do(t, request{
		method: http.MethodPost, path: "/api/apply", cookies: []*http.Cookie{cookie},
	})
	if resp.code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200 (%s)", resp.code, resp.body)
	}
	// Применяется состояние БД, а не тело запроса: у `POST /api/apply` его нет.
	if len(ap.snap.Tunnels) != 1 || ap.snap.Tunnels[0].Name != "Амстердам" {
		t.Fatalf("в применение ушёл снимок %+v", ap.snap.Tunnels)
	}
}

func TestApplyDefaultApplierIsReal(t *testing.T) {
	ts := newTestServer(t)
	if _, ok := ts.applier.(*singbox.Applier); !ok {
		t.Fatalf("по умолчанию применятель %T, ожидался настоящий", ts.applier)
	}
}
