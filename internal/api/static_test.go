package api

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/ArghTeam/razdacha/ui"
)

// fakeUI — минимальная статика: тестам нужен факт отдачи, а не настоящая панель.
func fakeUI() fs.FS {
	return fstest.MapFS{
		"index.html": {Data: []byte("<!DOCTYPE html><title>razdacha</title>")},
		"app.css":    {Data: []byte(":root{}")},
		"js/app.js":  {Data: []byte("export {};")},
	}
}

// newUIServer — сервер с подставленной статикой.
func newUIServer(t *testing.T, files fs.FS) *testServer {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "razdacha.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err := SetPassword(ctx, st, testPassword); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	ts := &testServer{st: st, logs: &syncBuffer{}, now: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)}
	srv, err := New(ctx, Config{
		Store:  st,
		Logger: slog.New(slog.NewTextHandler(ts.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:    ts.clock,
		UI:     files,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.sleep = func(context.Context, time.Duration) {}
	ts.Server = srv
	return ts
}

func TestStaticServesIndex(t *testing.T) {
	ts := newUIServer(t, fakeUI())

	resp := ts.do(t, request{method: http.MethodGet, path: "/"})
	if resp.code != http.StatusOK {
		t.Fatalf("код = %d, ожидался 200", resp.code)
	}
	if ct := resp.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, ожидался text/html", ct)
	}
	if !strings.Contains(resp.body, "<title>razdacha</title>") {
		t.Errorf("корень отдал не index.html: %q", resp.body)
	}
}

// Адреса экранов живут в хеше, но открыть можно что угодно: неизвестный путь
// обязан отдать саму панель, а не 404.
func TestStaticUnknownPathFallsBackToIndex(t *testing.T) {
	ts := newUIServer(t, fakeUI())

	for _, p := range []string{"/чего-то-нет", "/peers/42", "/deep/nested/route"} {
		resp := ts.do(t, request{method: http.MethodGet, path: p})
		if resp.code != http.StatusOK {
			t.Errorf("%s: код = %d, ожидался 200", p, resp.code)
			continue
		}
		if !strings.Contains(resp.body, "<title>razdacha</title>") {
			t.Errorf("%s: отдан не index.html", p)
		}
	}
}

func TestStaticServesAssets(t *testing.T) {
	ts := newUIServer(t, fakeUI())

	cases := map[string]string{
		"/app.css":   "text/css",
		"/js/app.js": "text/javascript",
	}
	for path, wantType := range cases {
		resp := ts.do(t, request{method: http.MethodGet, path: path})
		if resp.code != http.StatusOK {
			t.Errorf("%s: код = %d, ожидался 200", path, resp.code)
			continue
		}
		if ct := resp.header.Get("Content-Type"); !strings.HasPrefix(ct, wantType) {
			t.Errorf("%s: Content-Type = %q, ожидался %s", path, ct, wantType)
		}
	}
}

// Экран входа — это и есть статика: требовать на неё сессию значило бы не
// показать пользователю форму, в которую он должен ввести пароль.
func TestStaticDoesNotRequireSession(t *testing.T) {
	ts := newUIServer(t, fakeUI())

	for _, p := range []string{"/", "/app.css", "/js/app.js", "/что-угодно"} {
		resp := ts.do(t, request{method: http.MethodGet, path: p})
		if resp.code != http.StatusOK {
			t.Errorf("%s без сессии: код = %d, ожидался 200", p, resp.code)
		}
	}
}

// Обратная сторона того же правила: API сессию требует, и опечатка в пути REST
// не должна отдавать HTML со статусом 200 — иначе клиент разберёт страницу
// входа как JSON.
func TestAPIDoesNotFallThroughToSPA(t *testing.T) {
	ts := newUIServer(t, fakeUI())
	cookie := ts.login(t)

	for _, p := range []string{"/api/", "/api/peers", "/api/нет-такого"} {
		resp := ts.do(t, request{method: http.MethodGet, path: p, cookies: []*http.Cookie{cookie}})
		if resp.code == http.StatusOK {
			t.Errorf("%s: отдал 200, интерфейс подменил ответ API", p)
			continue
		}
		if strings.Contains(resp.body, "<title>") {
			t.Errorf("%s: в ответе HTML, ожидался JSON: %q", p, resp.body)
		}
		if ct := resp.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: Content-Type = %q, ожидался application/json", p, ct)
		}
	}
}

func TestAPIRequiresSession(t *testing.T) {
	ts := newUIServer(t, fakeUI())

	for _, p := range []string{"/api/peers", "/api/session", "/api/diag"} {
		resp := ts.do(t, request{method: http.MethodGet, path: p})
		if resp.code != http.StatusUnauthorized {
			t.Errorf("%s без сессии: код = %d, ожидался 401", p, resp.code)
		}
		if got := decodeError(t, resp).Code; got != codeUnauthorized {
			t.Errorf("%s: код ошибки = %q, ожидался %q", p, got, codeUnauthorized)
		}
	}
}

// Каталог сборки может быть пуст — бинарник обязан собираться и запускаться и
// в этом случае, но сказать об этом вслух.
func TestStaticMissingBuildIsExplicit(t *testing.T) {
	ts := newUIServer(t, fstest.MapFS{})

	resp := ts.do(t, request{method: http.MethodGet, path: "/"})
	if resp.code != http.StatusServiceUnavailable {
		t.Fatalf("код = %d, ожидался 503", resp.code)
	}
	if !strings.Contains(resp.body, "ui/dist") {
		t.Errorf("ответ не объясняет причину: %q", resp.body)
	}
}

func TestStaticRejectsWrite(t *testing.T) {
	ts := newUIServer(t, fakeUI())

	resp := ts.do(t, request{method: http.MethodPost, path: "/"})
	if resp.code != http.StatusMethodNotAllowed {
		t.Errorf("код = %d, ожидался 405", resp.code)
	}
}

// Встроенная сборка — не заглушка: в бинарнике лежит настоящая панель.
func TestEmbeddedUIHasIndex(t *testing.T) {
	f, err := ui.Files().Open("index.html")
	if err != nil {
		t.Fatalf("index.html не встроен: %v", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() < 512 {
		t.Errorf("index.html подозрительно мал: %d байт", info.Size())
	}
}
