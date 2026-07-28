package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
)

// withRuntimeVersion подставляет рантайм sing-box: настоящего в тестах нет,
// проверяется поведение панели вокруг него.
func withRuntimeVersion(t *testing.T, ts *testServer, body string, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	ts.clash = clash.New(clash.Options{
		Addr:       srv.Listener.Addr().String(),
		HTTPClient: &http.Client{Timeout: time.Second},
	})
}

// noRuntime уводит клиента Clash API на закрытый порт: sing-box не запущен.
func noRuntime(ts *testServer) {
	ts.clash = clash.New(clash.Options{
		Addr:       "127.0.0.1:1",
		HTTPClient: &http.Client{Timeout: 300 * time.Millisecond},
	})
}

func getVersion(t *testing.T, ts *testServer, cookie *http.Cookie) versionResponse {
	t.Helper()
	resp := ts.auth(t, cookie, http.MethodGet, "/api/version", "")
	if resp.code != http.StatusOK {
		t.Fatalf("GET /api/version = %d, ожидался 200 (%s)", resp.code, resp.body)
	}
	var out versionResponse
	if err := json.Unmarshal([]byte(resp.body), &out); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	return out
}

// TestVersionRequiresSession — ручка живёт за сессией, как соседние: забытый
// эндпоинт отдаёт 401, а не рассказывает случайному запросу, что тут стоит.
func TestVersionRequiresSession(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do(t, request{method: http.MethodGet, path: "/api/version"})
	if resp.code != http.StatusUnauthorized {
		t.Fatalf("код = %d, ожидался 401", resp.code)
	}
}

// TestVersionAll — всё известно: версия от линкера, установка из БД, рантайм
// ответил.
func TestVersionAll(t *testing.T) {
	ts := newTestServer(t)
	ts.build = Build{Version: "0.2.6", Commit: "fa5ea733ab20", SingboxLibrary: "1.12.25"}
	withRuntimeVersion(t, ts, `{"meta":true,"version":"1.12.25"}`, http.StatusOK)
	if err := ts.st.SetInstalledVersion(context.Background(), "0.2.6"); err != nil {
		t.Fatalf("SetInstalledVersion: %v", err)
	}
	cookie := ts.login(t)

	out := getVersion(t, ts, cookie)
	if out.Version != "0.2.6" {
		t.Errorf("версия = %q", out.Version)
	}
	if out.Commit == nil || *out.Commit != "fa5ea733ab20" {
		t.Errorf("коммит = %v", out.Commit)
	}
	if out.InstalledVersion == nil || *out.InstalledVersion != "0.2.6" {
		t.Errorf("версия установки = %v", out.InstalledVersion)
	}
	if out.VersionMismatch {
		t.Error("совпадающие версии показаны расхождением")
	}
	if out.SchemaVersion <= 0 {
		t.Errorf("версия схемы = %d, ожидалась ненулевая", out.SchemaVersion)
	}
	if out.Singbox.Library == nil || *out.Singbox.Library != "1.12.25" {
		t.Errorf("версия библиотеки = %v", out.Singbox.Library)
	}
	if out.Singbox.Runtime == nil || *out.Singbox.Runtime != "1.12.25" {
		t.Errorf("версия рантайма = %v", out.Singbox.Runtime)
	}
	if out.Singbox.RuntimeDetail != nil {
		t.Errorf("причина при живом рантайме = %v", *out.Singbox.RuntimeDetail)
	}
}

// TestVersionMismatch — ровно тот случай, ради которого ручка заводилась:
// установщик записал одну версию, а отвечает процесс с другой.
func TestVersionMismatch(t *testing.T) {
	ts := newTestServer(t)
	ts.build = Build{Version: "dev"}
	noRuntime(ts)
	if err := ts.st.SetInstalledVersion(context.Background(), "0.2.6"); err != nil {
		t.Fatalf("SetInstalledVersion: %v", err)
	}
	cookie := ts.login(t)

	out := getVersion(t, ts, cookie)
	if !out.VersionMismatch {
		t.Error("расхождение dev против 0.2.6 не показано")
	}
}

// TestVersionMismatchIgnoresVPrefix — `git describe` даёт версию с ведущей «v»,
// установщик пишет её из тега без неё. Это одна и та же версия, а не расхождение.
func TestVersionMismatchIgnoresVPrefix(t *testing.T) {
	ts := newTestServer(t)
	ts.build = Build{Version: "v0.2.6"}
	noRuntime(ts)
	if err := ts.st.SetInstalledVersion(context.Background(), "0.2.6"); err != nil {
		t.Fatalf("SetInstalledVersion: %v", err)
	}
	cookie := ts.login(t)

	if getVersion(t, ts, cookie).VersionMismatch {
		t.Error("v0.2.6 и 0.2.6 показаны расхождением")
	}
}

// TestVersionUnknownInstalled — БД от версии до 0.2.1 ключа не знала. Сравнить
// не с чем, и расхождение здесь было бы выдумкой.
func TestVersionUnknownInstalled(t *testing.T) {
	ts := newTestServer(t)
	ts.build = Build{Version: "0.2.6"}
	noRuntime(ts)
	cookie := ts.login(t)

	out := getVersion(t, ts, cookie)
	if out.InstalledVersion != nil {
		t.Errorf("версия установки = %v, ожидался null", *out.InstalledVersion)
	}
	if out.VersionMismatch {
		t.Error("неизвестная версия установки показана расхождением")
	}
}

// TestVersionRuntimeDown — sing-box не запущен. Ручка всё равно отдаёт 200:
// версии демона и схемы от рантайма не зависят, а причина уходит текстом.
//
// Причина — короткая фраза по-русски, а не ошибка Go: она показывается
// значением строки в сводке версий. Сырая ошибка с адресом Clash API уходит в
// лог демона — разбираться по ней всё равно не владельцу панели.
func TestVersionRuntimeDown(t *testing.T) {
	ts := newTestServer(t)
	ts.build = Build{Version: "0.2.6", SingboxLibrary: "1.12.25"}
	noRuntime(ts)
	cookie := ts.login(t)

	out := getVersion(t, ts, cookie)
	if out.Singbox.Runtime != nil {
		t.Errorf("версия рантайма = %v, ожидался null", *out.Singbox.Runtime)
	}
	if out.Singbox.RuntimeDetail == nil || *out.Singbox.RuntimeDetail != "sing-box не запущен" {
		t.Errorf("причина = %v, ожидалось «sing-box не запущен»", out.Singbox.RuntimeDetail)
	}
	if out.Singbox.Library == nil {
		t.Error("версия библиотеки пропала вместе с рантаймом")
	}

	if logs := ts.logs.String(); !strings.Contains(logs, "версия рантайма sing-box не получена") ||
		!strings.Contains(logs, "connection refused") {
		t.Error("сырая ошибка не попала в лог демона")
	}
}

// TestVersionRuntimeDetailStaysShort — причина не тащит в панель ни адрес Clash
// API, ни англоязычный хвост от net/http: строка такой длины разносит вёрстку
// сводки, а прочитать её пользователю всё равно нечем.
func TestVersionRuntimeDetailStaysShort(t *testing.T) {
	ts := newTestServer(t)
	noRuntime(ts)
	cookie := ts.login(t)

	got := getVersion(t, ts, cookie).Singbox.RuntimeDetail
	if got == nil {
		t.Fatal("причина не названа")
	}
	for _, bad := range []string{"127.0.0.1", "http://", "Get", "dial tcp", "refused"} {
		if strings.Contains(*got, bad) {
			t.Errorf("причина %q содержит %q", *got, bad)
		}
	}
}

// TestVersionRuntimeBadResponse — рантайм ответил, но не тем. Это не «не
// запущен»: юнит жив, и лечится это иначе.
func TestVersionRuntimeBadResponse(t *testing.T) {
	ts := newTestServer(t)
	withRuntimeVersion(t, ts, `{"meta":true}`, http.StatusOK)
	cookie := ts.login(t)

	out := getVersion(t, ts, cookie)
	if out.Singbox.Runtime != nil {
		t.Errorf("версия рантайма = %v, ожидался null", *out.Singbox.Runtime)
	}
	if out.Singbox.RuntimeDetail == nil || *out.Singbox.RuntimeDetail != "sing-box ответил неожиданно" {
		t.Errorf("причина = %v, ожидалось «sing-box ответил неожиданно»", out.Singbox.RuntimeDetail)
	}
}

// TestVersionDefaultsToDev — сборка мимо `Makefile` не оставляет версию пустой:
// пустая строка в шапке выглядит как сломанная панель.
func TestVersionDefaultsToDev(t *testing.T) {
	ts := newTestServer(t)
	noRuntime(ts)
	cookie := ts.login(t)

	out := getVersion(t, ts, cookie)
	if out.Version != "dev" {
		t.Errorf("версия = %q, ожидалась dev", out.Version)
	}
	if out.Commit != nil {
		t.Errorf("коммит = %v, ожидался null", *out.Commit)
	}
	if out.Singbox.Library != nil {
		t.Errorf("версия библиотеки = %v, ожидался null", *out.Singbox.Library)
	}
}
