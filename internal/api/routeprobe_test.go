package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/store"
)

// fakeCore — рантайм sing-box для тестов пробника: отдаёт свой список правил и
// свой ответ резолвера. Наружу тесты не ходят.
type fakeCore struct {
	rules  string // тело `GET /rules`
	answer string // тело `GET /dns/query`
	dnsErr bool   // резолвер отвечает отказом
}

func (c fakeCore) start(t *testing.T, ts *testServer) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/rules":
			_, _ = w.Write([]byte(c.rules))
		case r.URL.Path == "/dns/query" && c.dnsErr:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"upstream не ответил"}`))
		case r.URL.Path == "/dns/query":
			_, _ = w.Write([]byte(c.answer))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	ts.clash = clash.New(clash.Options{Addr: srv.URL})
}

func probeRules(t *testing.T, ts *testServer) (withDomains, withList store.Rule) {
	t.Helper()
	ctx := context.Background()
	tunnel, err := ts.st.CreateTunnel(ctx, store.Tunnel{
		Name: "Амстердам", Type: store.TunnelVLESS, Source: store.SourceURL,
		Raw: "vless://…", Parsed: json.RawMessage(`{"server":"1.2.3.4"}`), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	withDomains, err = ts.st.CreateRule(ctx, store.Rule{
		Name: "Свои домены", Action: store.ActionTunnel, TunnelID: tunnel.ID,
		Enabled: true, Domains: []string{"example.com"}, PeerScope: store.ScopeAll,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	withList, err = ts.st.CreateRule(ctx, store.Rule{
		Name: "YouTube", Action: store.ActionTunnel, TunnelID: tunnel.ID,
		Enabled: true, CommunityLists: []string{"youtube"}, PeerScope: store.ScopeAll,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	return withDomains, withList
}

func runProbe(t *testing.T, ts *testServer, cookie *http.Cookie, domain string) response {
	t.Helper()
	return ts.do(t, request{
		method: http.MethodPost, path: "/api/route/test",
		body:    `{"domain":"` + domain + `"}`,
		cookies: []*http.Cookie{cookie},
	})
}

func decodeProbe(t *testing.T, res response) probeResponse {
	t.Helper()
	var got probeResponse
	if err := json.Unmarshal([]byte(res.body), &got); err != nil {
		t.Fatalf("разбор ответа пробника: %v (%s)", err, res.body)
	}
	return got
}

// Своё условие правила демон видит целиком, поэтому правило называется, а не
// перечисляется кандидатом. Outbound берётся у живого ядра как есть.
func TestRouteProbeNamesRuleByOwnDomain(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	own, _ := probeRules(t, ts)

	fakeCore{
		rules: `{"rules":[
			{"type":"default","payload":"inbound=dns-in","proxy":"hijack-dns"},
			{"type":"default","payload":"rule_set=rule-` + own.ID + `","proxy":"route(tun-x)"},
			{"type":"default","payload":"rule_set=[list-youtube]","proxy":"route(tun-x)"}]}`,
		answer: `{"Status":0,"Answer":[{"name":"example.com.","type":1,"TTL":60,"data":"198.18.0.7"}]}`,
	}.start(t, ts)

	got := decodeProbe(t, runProbe(t, ts, cookie, "cdn.example.com"))
	if got.Rule == nil {
		t.Fatalf("правило не названо: %+v", got)
	}
	if got.Rule.ID != own.ID {
		t.Errorf("правило = %q, ожидалось %q", got.Rule.Name, own.Name)
	}
	if got.Rule.Outbound != "route(tun-x)" {
		t.Errorf("outbound = %q, ожидался route(tun-x) от живого ядра", got.Rule.Outbound)
	}
	if !got.FakeIP {
		t.Error("FakeIP не опознан, хотя резолвер выдал 198.18.0.7")
	}
	if got.Diverged {
		t.Error("расхождение с базой на равном числе правил")
	}
}

// Состав community-списка ведёт сам sing-box: правило со списком называется
// кандидатом, а не победителем, — но только когда кандидатов больше одного.
// Иначе врать было бы не о чем: FakeIP подтверждает, что поймало именно оно.
func TestRouteProbeListRuleIsCandidate(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	_, withList := probeRules(t, ts)
	ctx := context.Background()
	second, err := ts.st.CreateRule(ctx, store.Rule{
		Name: "Заблокированные", Action: store.ActionTunnel, TunnelID: withList.TunnelID,
		Enabled: true, CommunityLists: []string{"block"}, PeerScope: store.ScopeAll,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	fakeCore{
		rules: `{"rules":[
			{"type":"default","payload":"rule_set=[list-youtube]","proxy":"route(tun-x)"},
			{"type":"default","payload":"rule_set=[list-block]","proxy":"reject(default)"}]}`,
		answer: `{"Status":0,"Answer":[{"name":"youtube.com.","type":1,"TTL":60,"data":"198.18.0.9"}]}`,
	}.start(t, ts)

	got := decodeProbe(t, runProbe(t, ts, cookie, "youtube.com"))
	if got.Rule != nil {
		t.Fatalf("правило названо, хотя состав списков ядро не отдаёт: %+v", got.Rule)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("кандидатов %d, ожидалось 2: %+v", len(got.Candidates), got.Candidates)
	}
	if got.Candidates[0].Name != withList.Name || got.Candidates[1].Name != second.Name {
		t.Errorf("порядок кандидатов = %q, %q — ожидался порядок ядра",
			got.Candidates[0].Name, got.Candidates[1].Name)
	}
	if !strings.Contains(got.Verdict, "одно из правил") {
		t.Errorf("вердикт не говорит о неопределённости: %q", got.Verdict)
	}
	// Расхождение с базой: правило «Свои домены» включено, а в конфиге ядра его нет.
	if !got.Diverged {
		t.Error("расхождение с базой не замечено")
	}
}

// Домена не ловит никто — это утверждение, а не пустой ответ.
func TestRouteProbeNoRule(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	own, _ := probeRules(t, ts)

	fakeCore{
		rules:  `{"rules":[{"type":"default","payload":"rule_set=rule-` + own.ID + `","proxy":"route(tun-x)"}]}`,
		answer: `{"Status":0,"Answer":[{"name":"bank.ru.","type":1,"TTL":60,"data":"203.0.113.7"}]}`,
	}.start(t, ts)

	got := decodeProbe(t, runProbe(t, ts, cookie, "bank.ru"))
	if got.Rule != nil || len(got.Candidates) != 0 {
		t.Fatalf("домен без правила получил правило: %+v", got)
	}
	if got.FakeIP {
		t.Error("настоящий адрес принят за FakeIP")
	}
	if !strings.Contains(got.Verdict, "напрямую") {
		t.Errorf("вердикт = %q, ожидалось про прямой выход", got.Verdict)
	}
}

// Недоступное ядро — 503 с объяснением, а не пустой результат: пустой читался
// бы как «ни одно правило не сработает».
func TestRouteProbeCoreUnavailable(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	probeRules(t, ts)
	// Порт, на котором заведомо никто не слушает: httptest поднят и сразу закрыт.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close()
	ts.clash = clash.New(clash.Options{Addr: addr})

	res := runProbe(t, ts, cookie, "example.com")
	if res.code != http.StatusServiceUnavailable {
		t.Fatalf("код %d, ожидался 503 (%s)", res.code, res.body)
	}
	e := decodeError(t, res)
	if e.Code != codeNotReady {
		t.Errorf("код ошибки = %q, ожидался %q", e.Code, codeNotReady)
	}
	if !strings.Contains(e.Error, "sing-box") {
		t.Errorf("текст ошибки не называет причину: %q", e.Error)
	}
}

// Резолвер не ответил — разбор правил остаётся, а причина называется словами.
func TestRouteProbeResolverFailed(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	own, _ := probeRules(t, ts)

	fakeCore{
		rules:  `{"rules":[{"type":"default","payload":"rule_set=rule-` + own.ID + `","proxy":"route(tun-x)"}]}`,
		dnsErr: true,
	}.start(t, ts)

	got := decodeProbe(t, runProbe(t, ts, cookie, "example.com"))
	if got.ResolveError == "" {
		t.Fatal("отказ резолвера не отражён в ответе")
	}
	if got.Rule == nil || got.Rule.ID != own.ID {
		t.Errorf("правило не названо, хотя условия видны: %+v", got.Rule)
	}
}

// Не домен — 400 с объяснением, а не запрос к ядру наугад.
func TestRouteProbeRejectsNonDomain(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	for _, bad := range []string{"", "  ", "203.0.113.7", "https://", "не домен"} {
		res := ts.do(t, request{
			method: http.MethodPost, path: "/api/route/test",
			body:    `{"domain":"` + bad + `"}`,
			cookies: []*http.Cookie{cookie},
		})
		if res.code != http.StatusBadRequest {
			t.Errorf("%q = %d, ожидался 400 (%s)", bad, res.code, res.body)
		}
	}
}

// Пробник за сессией, как и всё остальное под /api/.
func TestRouteProbeRequiresSession(t *testing.T) {
	ts := newTestServer(t)
	res := ts.do(t, request{method: http.MethodPost, path: "/api/route/test", body: `{"domain":"example.com"}`})
	if res.code != http.StatusUnauthorized {
		t.Fatalf("без сессии = %d, ожидался 401 (%s)", res.code, res.body)
	}
}

// Ссылку с путём и хвостом пробник приводит к домену сам: пользователь копирует
// адрес из строки браузера, и отказывать ему из-за https:// незачем.
func TestNormalizeProbeDomain(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=1": "www.youtube.com",
		"  Example.COM.  ":                  "example.com",
		"*.example.com":                     "example.com",
		"http://cdn.example.com/a/b":        "cdn.example.com",
		"example":                           "",
		"203.0.113.7":                       "",
		"http://":                           "",
	}
	for in, want := range cases {
		if got := normalizeProbeDomain(in); got != want {
			t.Errorf("normalizeProbeDomain(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}
