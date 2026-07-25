package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// syncBuffer — лог теста: пишут в него горутины сервера, читает тест.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// testServer — сервер на временной БД с управляемым временем: тестам не нужны
// ни сеть, ни root, ни ожидание реальных задержек.
type testServer struct {
	*Server
	st   *store.Store
	logs *syncBuffer

	mu  sync.Mutex
	now time.Time
}

func newTestServer(t *testing.T) *testServer {
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
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Реальную задержку после неудачи тесты не ждут — проверяется её расчёт,
	// см. TestThrottleDelayCapped.
	srv.sleep = func(context.Context, time.Duration) {}
	ts.Server = srv
	return ts
}

func (ts *testServer) clock() time.Time {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.now
}

func (ts *testServer) advance(d time.Duration) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.now = ts.now.Add(d)
}

// request — один запрос к панели. remote по умолчанию — nginx на loopback.
type request struct {
	method  string
	path    string
	body    string
	remote  string
	headers map[string]string
	cookies []*http.Cookie
}

// response — прочитанный ответ: тело читается сразу, чтобы тесты не таскали
// за собой его закрытие.
type response struct {
	code    int
	header  http.Header
	cookies []*http.Cookie
	body    string
}

func (ts *testServer) do(t *testing.T, req request) response {
	t.Helper()

	r := httptest.NewRequest(req.method, req.path, strings.NewReader(req.body))
	r.RemoteAddr = "127.0.0.1:52344"
	if req.remote != "" {
		r.RemoteAddr = req.remote
	}
	for k, v := range req.headers {
		r.Header.Set(k, v)
	}
	for _, c := range req.cookies {
		r.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, r)
	res := rec.Result()
	defer res.Body.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(res.Body); err != nil {
		t.Fatalf("чтение ответа: %v", err)
	}
	return response{code: res.StatusCode, header: res.Header, cookies: res.Cookies(), body: buf.String()}
}

// login выполняет вход и возвращает cookie сессии.
func (ts *testServer) login(t *testing.T) *http.Cookie {
	t.Helper()
	resp := ts.do(t, request{method: http.MethodPost, path: "/api/login", body: loginBody(testPassword)})
	if resp.code != http.StatusOK {
		t.Fatalf("вход = %d, ожидался 200 (%s)", resp.code, resp.body)
	}
	c := resp.sessionCookie()
	if c == nil {
		t.Fatal("вход не поставил cookie сессии")
	}
	return c
}

func loginBody(password string) string {
	b, err := json.Marshal(loginRequest{Password: password})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// sessionCookie достаёт cookie сессии из ответа.
func (r response) sessionCookie() *http.Cookie {
	for _, c := range r.cookies {
		if c.Name == sessionCookie {
			return c
		}
	}
	return nil
}

func decodeError(t *testing.T, resp response) errorResponse {
	t.Helper()
	var out errorResponse
	if err := json.Unmarshal([]byte(resp.body), &out); err != nil {
		t.Fatalf("разбор ошибки: %v", err)
	}
	return out
}

func TestLoginSuccess(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	if cookie.Value == "" {
		t.Fatal("пустой токен сессии")
	}
	if !cookie.HttpOnly {
		t.Error("cookie сессии доступна из JS")
	}
	if !cookie.Secure {
		t.Error("cookie сессии уйдёт по HTTP без TLS")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, ожидался Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q, ожидался /", cookie.Path)
	}

	// В БД лежит хеш токена, а не сам токен.
	if _, err := ts.st.Session(context.Background(), cookie.Value, ts.clock()); !errors.Is(err, store.ErrNotFound) {
		t.Error("токен сессии хранится в БД в открытом виде")
	}
	if _, err := ts.st.Session(context.Background(), hashToken(cookie.Value), ts.clock()); err != nil {
		t.Errorf("сессия не сохранена: %v", err)
	}

	resp := ts.do(t, request{method: http.MethodGet, path: "/api/session", cookies: []*http.Cookie{cookie}})
	if resp.code != http.StatusOK {
		t.Fatalf("GET /api/session = %d, ожидался 200", resp.code)
	}
	var got sessionResponse
	if err := json.Unmarshal([]byte(resp.body), &got); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if !got.Authenticated || !got.ExpiresAt.Equal(ts.clock().Add(sessionTTL)) {
		t.Errorf("ответ сессии = %+v", got)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.do(t, request{method: http.MethodPost, path: "/api/login", body: loginBody("неверный-пароль-1")})
	if resp.code != http.StatusUnauthorized {
		t.Fatalf("код = %d, ожидался 401", resp.code)
	}
	if resp.sessionCookie() != nil {
		t.Error("неверный пароль выдал cookie")
	}
	if got := decodeError(t, resp); got.Code != codeUnauthorized || got.Error == "" {
		t.Errorf("ошибка = %+v", got)
	}

	// Пароль не должен попасть ни в лог, ни в текст ошибки.
	if strings.Contains(ts.logs.String(), "неверный-пароль-1") || strings.Contains(ts.logs.String(), testPassword) {
		t.Error("пароль попал в лог")
	}
}

func TestLoginBadBody(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.do(t, request{method: http.MethodPost, path: "/api/login", body: "{не json"})
	if resp.code != http.StatusBadRequest {
		t.Fatalf("код = %d, ожидался 400", resp.code)
	}
	if got := decodeError(t, resp); got.Code != codeBadRequest {
		t.Errorf("код ошибки = %q", got.Code)
	}
}

func TestSessionExpires(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	ts.advance(sessionTTL + time.Minute)
	resp := ts.do(t, request{method: http.MethodGet, path: "/api/session", cookies: []*http.Cookie{cookie}})
	if resp.code != http.StatusUnauthorized {
		t.Fatalf("истёкшая сессия = %d, ожидался 401", resp.code)
	}
	// Протухшая cookie гасится, чтобы браузер не слал её дальше.
	if c := resp.sessionCookie(); c == nil || c.MaxAge >= 0 {
		t.Error("протухшая cookie не погашена")
	}
}

func TestSessionRenewed(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	// До половины срока продления нет: иначе запись в БД на каждый запрос.
	resp := ts.do(t, request{method: http.MethodGet, path: "/api/session", cookies: []*http.Cookie{cookie}})
	if c := resp.sessionCookie(); c != nil {
		t.Error("сессия продлена раньше срока")
	}

	ts.advance(sessionTTL - sessionRenewAfter + time.Minute)
	resp = ts.do(t, request{method: http.MethodGet, path: "/api/session", cookies: []*http.Cookie{cookie}})
	if resp.code != http.StatusOK {
		t.Fatalf("код = %d, ожидался 200", resp.code)
	}
	c := resp.sessionCookie()
	if c == nil {
		t.Fatal("сессия не продлена")
	}
	if c.Value != cookie.Value {
		t.Error("продление сменило токен")
	}

	sess, err := ts.st.Session(context.Background(), hashToken(cookie.Value), ts.clock())
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !sess.ExpiresAt.Equal(ts.clock().Add(sessionTTL)) {
		t.Errorf("срок в БД = %v, ожидался %v", sess.ExpiresAt, ts.clock().Add(sessionTTL))
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.do(t, request{method: http.MethodPost, path: "/api/logout", cookies: []*http.Cookie{cookie}})
	if resp.code != http.StatusOK {
		t.Fatalf("выход = %d, ожидался 200", resp.code)
	}
	if c := resp.sessionCookie(); c == nil || c.MaxAge >= 0 || c.Value != "" {
		t.Error("выход не погасил cookie")
	}
	if _, err := ts.st.Session(context.Background(), hashToken(cookie.Value), ts.clock()); !errors.Is(err, store.ErrNotFound) {
		t.Error("сессия осталась в БД после выхода")
	}

	resp = ts.do(t, request{method: http.MethodGet, path: "/api/session", cookies: []*http.Cookie{cookie}})
	if resp.code != http.StatusUnauthorized {
		t.Errorf("старая cookie работает после выхода: %d", resp.code)
	}
}

// Всё под /api/, кроме входа, закрыто — включая пути, которых ещё нет.
func TestProtectedPathsRequireSession(t *testing.T) {
	ts := newTestServer(t)

	cases := []request{
		{method: http.MethodGet, path: "/api/session"},
		{method: http.MethodPost, path: "/api/logout"},
		{method: http.MethodGet, path: "/api/peers"},
		{method: http.MethodPost, path: "/api/apply"},
		{method: http.MethodGet, path: "/api/settings"},
		{method: http.MethodGet, path: "/api/session", cookies: []*http.Cookie{{Name: sessionCookie, Value: "подделанный-токен"}}},
	}
	for _, c := range cases {
		resp := ts.do(t, c)
		if resp.code != http.StatusUnauthorized {
			t.Errorf("%s %s без сессии = %d, ожидался 401", c.method, c.path, resp.code)
		}
	}
}

func TestBruteForceBlockAndReset(t *testing.T) {
	ts := newTestServer(t)

	for i := 0; i < maxLoginFails; i++ {
		resp := ts.do(t, request{method: http.MethodPost, path: "/api/login", body: loginBody("неверный-пароль-1")})
		if resp.code != http.StatusUnauthorized {
			t.Fatalf("попытка %d = %d, ожидался 401", i+1, resp.code)
		}
	}

	// Верный пароль тоже отклоняется: блокируется адрес, а не пароль.
	resp := ts.do(t, request{method: http.MethodPost, path: "/api/login", body: loginBody(testPassword)})
	if resp.code != http.StatusTooManyRequests {
		t.Fatalf("после серии неудач = %d, ожидался 429", resp.code)
	}
	if resp.header.Get("Retry-After") == "" {
		t.Error("нет заголовка Retry-After")
	}
	if got := decodeError(t, resp); got.Code != codeTooMany {
		t.Errorf("код ошибки = %q", got.Code)
	}

	ts.advance(loginBlock + time.Minute)
	ts.login(t) // блокировка истекла, вход работает

	// Успешный вход сбросил счётчик: до новой блокировки снова целая серия.
	for i := 0; i < maxLoginFails-1; i++ {
		ts.do(t, request{method: http.MethodPost, path: "/api/login", body: loginBody("неверный-пароль-1")})
	}
	resp = ts.do(t, request{method: http.MethodPost, path: "/api/login", body: loginBody(testPassword)})
	if resp.code != http.StatusOK {
		t.Errorf("счётчик не сброшен успешным входом: %d", resp.code)
	}
}

// Главная ловушка этого слоя: клиент, пришедший не с loopback, подделывает
// X-Forwarded-For и X-Real-IP и рассчитывает получать новую корзину попыток на
// каждый запрос. Адрес соединения при этом один — блокировка обязана сработать.
func TestSpoofedForwardedForDoesNotEvadeBlock(t *testing.T) {
	ts := newTestServer(t)

	spoof := func(i int) request {
		return request{
			method: http.MethodPost,
			path:   "/api/login",
			body:   loginBody("неверный-пароль-1"),
			remote: "203.0.113.7:41233",
			headers: map[string]string{
				"X-Forwarded-For": "10.0.0." + string(rune('1'+i)),
				"X-Real-IP":       "10.0.1." + string(rune('1'+i)),
			},
		}
	}

	for i := 0; i < maxLoginFails; i++ {
		if resp := ts.do(t, spoof(i)); resp.code != http.StatusUnauthorized {
			t.Fatalf("попытка %d = %d, ожидался 401", i+1, resp.code)
		}
	}

	req := spoof(maxLoginFails)
	req.body = loginBody(testPassword)
	resp := ts.do(t, req)
	if resp.code != http.StatusTooManyRequests {
		t.Fatalf("подделка заголовков обошла блокировку: %d", resp.code)
	}
}

func TestUnknownPath(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.do(t, request{method: http.MethodGet, path: "/чего-то-нет"})
	if resp.code != http.StatusNotFound {
		t.Errorf("код = %d, ожидался 404", resp.code)
	}
}

// Паника в обработчике не роняет демон, но обязана попасть в лог со стеком.
func TestPanicRecovered(t *testing.T) {
	ts := newTestServer(t)

	h := ts.logging(ts.recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("сломалось")
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("код = %d, ожидался 500", rec.Code)
	}
	logs := ts.logs.String()
	if !strings.Contains(logs, "сломалось") || !strings.Contains(logs, "TestPanicRecovered") {
		t.Errorf("паника не попала в лог со стеком: %s", logs)
	}
}

// Публичная панель без пароля — не режим работы, а отказ стартовать (ADR 0009).
func TestNewRequiresPassword(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "razdacha.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	if _, err := New(ctx, Config{Store: st}); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("ожидалась ErrNoPassword, получено: %v", err)
	}

	if err := SetPassword(ctx, st, testPassword); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if _, err := New(ctx, Config{Store: st}); err != nil {
		t.Errorf("с паролем сервер не собрался: %v", err)
	}
}

func TestNewRejectsPublicAddr(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "razdacha.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	if err := SetPassword(ctx, st, testPassword); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	if _, err := New(ctx, Config{Store: st, Addr: "0.0.0.0:8080"}); !errors.Is(err, ErrPublicListen) {
		t.Errorf("ожидалась ErrPublicListen, получено: %v", err)
	}
	if _, err := New(ctx, Config{Store: st, Addr: "не адрес"}); !errors.Is(err, ErrBadConfig) {
		t.Errorf("ожидалась ErrBadConfig, получено: %v", err)
	}
	if _, err := New(ctx, Config{Addr: DefaultAddr}); !errors.Is(err, ErrBadConfig) {
		t.Errorf("сервер без хранилища собрался: %v", err)
	}
}

// Отмена контекста гасит сервер и возвращает управление.
func TestRunShutsDown(t *testing.T) {
	ts := newTestServer(t)
	ts.addr = "127.0.0.1:0"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ts.Run(ctx) }()

	// Сервер поднялся, если лог о прослушивании появился.
	deadline := time.After(5 * time.Second)
	for !strings.Contains(ts.logs.String(), "панель слушает") {
		select {
		case err := <-done:
			t.Fatalf("Run завершился до отмены: %v", err)
		case <-deadline:
			t.Fatal("сервер не поднялся")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run не завершился по отмене контекста")
	}
}
