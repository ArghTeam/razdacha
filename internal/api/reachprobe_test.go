package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// Классификатор — чистая функция, поэтому и проверяется без сети: код ответа,
// кусок тела и ошибка соединения на входе, класс и вердикт на выходе.
func TestClassifyReach(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		connErr error
		class   string
	}{
		{name: "200 открывается", status: 200, class: reachClassOK},
		{name: "301 редирект — тоже открывается", status: 301, class: reachClassOK},
		{name: "403 геоблок", status: 403, class: reachClassGeoblock},
		{name: "451 геоблок", status: 451, class: reachClassGeoblock},
		{name: "Cloudflare 1020 в теле", status: 403, body: "…error code: 1020…", class: reachClassGeoblock},
		{name: "1020 при любом коде", status: 200, body: "error code: 1020", class: reachClassGeoblock},
		{name: "ошибка соединения", status: 0, connErr: errors.New("dial tcp: timeout"), class: reachClassDown},
		{name: "прочий код — дозвонились", status: 500, class: reachClassOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			class, verdict := classifyReach(c.status, []byte(c.body), c.connErr)
			if class != c.class {
				t.Errorf("класс = %q, ожидался %q", class, c.class)
			}
			if verdict == "" {
				t.Error("вердикт пустой")
			}
		})
	}
}

// Уточнение про Cloudflare попадает в вердикт: 1020 — не просто «403».
func TestClassifyReachCloudflareVerdict(t *testing.T) {
	_, verdict := classifyReach(403, []byte("error code: 1020"), nil)
	if want := "1020"; !strings.Contains(verdict, want) {
		t.Errorf("вердикт %q не упоминает Cloudflare %s", verdict, want)
	}
}

func decodeReach(t *testing.T, res response) reachResult {
	t.Helper()
	var got reachResult
	if err := json.Unmarshal([]byte(res.body), &got); err != nil {
		t.Fatalf("разбор ответа пробы: %v (%s)", err, res.body)
	}
	return got
}

func runReach(t *testing.T, ts *testServer, cookie *http.Cookie, domain string) response {
	t.Helper()
	return ts.do(t, request{
		method: http.MethodPost, path: "/api/domain/reachability",
		body:    `{"domain":"` + domain + `"}`,
		cookies: []*http.Cookie{cookie},
	})
}

// Хендлер зовёт пробу (подменённую), нормализует домен и переводит исход в
// класс с вердиктом. Настоящая сеть не участвует.
func TestReachabilityHandlerGeoblock(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	var asked string
	ts.reach = func(_ context.Context, domain string) (int, []byte, error) {
		asked = domain
		return 403, []byte("blocked"), nil
	}

	got := decodeReach(t, runReach(t, ts, cookie, "https://ChatGPT.com/path"))
	if asked != "chatgpt.com" {
		t.Errorf("пробе передан домен %q, ожидался нормализованный chatgpt.com", asked)
	}
	if got.Domain != "chatgpt.com" {
		t.Errorf("domain = %q, ожидался chatgpt.com", got.Domain)
	}
	if got.Class != reachClassGeoblock {
		t.Errorf("класс = %q, ожидался %q", got.Class, reachClassGeoblock)
	}
	if got.Status != 403 {
		t.Errorf("status = %d, ожидался 403", got.Status)
	}
	if got.Verdict == "" {
		t.Error("вердикт пустой")
	}
}

// Ошибка соединения — не 500, а честный ответ «напрямую не отвечает».
func TestReachabilityHandlerUnreachable(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	ts.reach = func(_ context.Context, _ string) (int, []byte, error) {
		return 0, nil, errors.New("dial tcp: i/o timeout")
	}

	res := runReach(t, ts, cookie, "example.com")
	if res.code != http.StatusOK {
		t.Fatalf("код ответа = %d, ожидался 200 (%s)", res.code, res.body)
	}
	got := decodeReach(t, res)
	if got.Class != reachClassDown {
		t.Errorf("класс = %q, ожидался %q", got.Class, reachClassDown)
	}
	if got.Status != 0 {
		t.Errorf("status = %d, ожидался 0", got.Status)
	}
}

// Пустой и битый домен — 400, а не поход пробы.
func TestReachabilityHandlerRejectsBadDomain(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	called := false
	ts.reach = func(_ context.Context, _ string) (int, []byte, error) {
		called = true
		return 200, nil, nil
	}

	for _, bad := range []string{"", "   ", "not a domain", "http://", "10.0.0.1"} {
		res := runReach(t, ts, cookie, bad)
		if res.code != http.StatusBadRequest {
			t.Errorf("домен %q: код = %d, ожидался 400 (%s)", bad, res.code, res.body)
		}
		if decodeError(t, res).Code != codeBadRequest {
			t.Errorf("домен %q: код ошибки не %q", bad, codeBadRequest)
		}
	}
	if called {
		t.Error("проба вызвана на негодном домене — до сети дойти не должно")
	}
}
