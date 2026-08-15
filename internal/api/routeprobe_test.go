package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/singbox"
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
			{"type":"default","payload":"rule_set=[list-block]","proxy":"reject"}]}`,
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

/* --- дефекты, найденные на живом стенде (sing-box 1.12.25) ---------------- */

// Д1. Правило, у которого единственное условие — не скачавшийся список, в
// конфиг не попадает, и расхождением это не является: применять там нечего.
// Счёт по `Enabled` объявлял непримененной правкой ровно тот сценарий со
// сломанным списком, ради которого задача и заводилась.
func TestRouteProbeDivergenceIgnoresRuleOutOfConfig(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	own, _ := probeRules(t, ts)
	ctx := context.Background()
	// Список не .srs и не .json — его качает демон, а он не скачался: набора у
	// правила нет, условий не остаётся, правило мимо конфига.
	if _, err := ts.st.CreateRule(ctx, store.Rule{
		Name: "Только сломанный список", Action: store.ActionTunnel, TunnelID: own.TunnelID,
		Enabled: true, RemoteLists: []string{"https://no-such-host-149.invalid/list.lst"},
		PeerScope: store.ScopeAll,
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	// Плюс правило со списком, который скачался: оно в конфиге есть.
	plain, err := ts.st.CreateRule(ctx, store.Rule{
		Name: "Скачавшийся список", Action: store.ActionTunnel, TunnelID: own.TunnelID,
		Enabled: true, RemoteLists: []string{"https://example.com/ok.lst"}, PeerScope: store.ScopeAll,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	ts.plainLists = func(url string) (singbox.PlainList, bool) {
		if url == "https://example.com/ok.lst" {
			return singbox.PlainList{Domains: []string{"mylist149.example"}}, true
		}
		return singbox.PlainList{}, false
	}

	fakeCore{
		rules: `{"rules":[
			{"type":"default","payload":"rule_set=rule-` + own.ID + `","proxy":"route(tun-x)"},
			{"type":"default","payload":"rule_set=[list-youtube]","proxy":"route(tun-x)"},
			{"type":"default","payload":"rule_set=rule-` + plain.ID + `-plain-0","proxy":"route(tun-x)"}]}`,
		answer: `{"Status":0,"Answer":[{"name":"bank.ru.","type":1,"TTL":60,"data":"203.0.113.7"}]}`,
	}.start(t, ts)

	got := decodeProbe(t, runProbe(t, ts, cookie, "bank.ru"))
	if got.CoreRules != 3 {
		t.Fatalf("правил в ядре %d, ожидалось 3", got.CoreRules)
	}
	if got.Diverged {
		t.Errorf("расхождение объявлено там, где применять нечего: %q", got.Verdict)
	}
}

// Д1, обратная сторона: правило, которое в конфиг попадает, но до ядра не
// доехало, — это и есть непримененная правка.
func TestRouteProbeDivergenceSeesUnappliedRule(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	own, _ := probeRules(t, ts)

	fakeCore{
		rules:  `{"rules":[{"type":"default","payload":"rule_set=rule-` + own.ID + `","proxy":"route(tun-x)"}]}`,
		answer: `{"Status":0,"Answer":[{"name":"bank.ru.","type":1,"TTL":60,"data":"203.0.113.7"}]}`,
	}.start(t, ts)

	got := decodeProbe(t, runProbe(t, ts, cookie, "bank.ru"))
	if !got.Diverged {
		t.Errorf("непримененное правило не замечено: %q", got.Verdict)
	}
}

// Д3. Настоящий адрес от резолвера — доказательство ядра: правило с туннелем не
// сработало, иначе был бы FakeIP. Кандидат снимается, а не висит предположением.
func TestRouteProbeRealAddressDropsTunnelCandidates(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	_, withList := probeRules(t, ts)

	fakeCore{
		rules: `{"rules":[
			{"type":"default","payload":"rule_set=[list-youtube]","proxy":"route(tun-x)"}]}`,
		answer: `{"Status":0,"Answer":[{"name":"example.org.","type":1,"TTL":60,"data":"93.184.216.34"}]}`,
	}.start(t, ts)

	got := decodeProbe(t, runProbe(t, ts, cookie, "example.org"))
	if got.Rule != nil || len(got.Candidates) != 0 {
		t.Fatalf("правило «%s» осталось кандидатом вопреки настоящему адресу: %+v",
			withList.Name, got)
	}
	if !strings.Contains(got.Verdict, "Ни одно правило") {
		t.Errorf("вердикт = %q, ожидалось «ни одно правило»", got.Verdict)
	}
}

// Д3. Достоверное совпадение побеждает «может быть»: домен лежит в plain-списке
// правила, которое демон видит целиком, — победителем должно быть оно, а не
// стоящее выше правило со списком, состав которого ведёт ядро.
func TestRouteProbeCertainBeatsCandidate(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	_, withList := probeRules(t, ts)
	ctx := context.Background()
	direct, err := ts.st.CreateRule(ctx, store.Rule{
		Name: "Мой список напрямую", Action: store.ActionDirect, Enabled: true,
		RemoteLists: []string{"https://example.com/ok.lst"}, PeerScope: store.ScopeAll,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	ts.plainLists = func(url string) (singbox.PlainList, bool) {
		if url == "https://example.com/ok.lst" {
			return singbox.PlainList{Domains: []string{"mylist149.example"}}, true
		}
		return singbox.PlainList{}, false
	}

	fakeCore{
		rules: `{"rules":[
			{"type":"default","payload":"rule_set=[list-youtube]","proxy":"route(tun-x)"},
			{"type":"default","payload":"rule_set=rule-` + direct.ID + `-plain-0","proxy":"route(direct)"}]}`,
		answer: `{"Status":0,"Answer":[{"name":"mylist149.example.","type":1,"TTL":60,"data":"203.0.113.9"}]}`,
	}.start(t, ts)

	got := decodeProbe(t, runProbe(t, ts, cookie, "mylist149.example"))
	if got.Rule == nil || got.Rule.ID != direct.ID {
		t.Fatalf("победителем названо не «%s», а %+v (кандидаты %+v)",
			direct.Name, got.Rule, got.Candidates)
	}
	if got.Rule.Outbound != "route(direct)" {
		t.Errorf("outbound = %q, ожидался route(direct)", got.Rule.Outbound)
	}
	// Правило со списком выше по порядку отсеяно настоящим адресом: FakeIP ему
	// не выдали, значит оно не совпало.
	if len(got.Candidates) != 0 {
		t.Errorf("кандидаты остались вопреки доказательству ядра: %+v (правило «%s»)",
			got.Candidates, withList.Name)
	}
}

// Д3. Отказ резолвера ничего не отсеивает: он как раз и означает, что сработало
// правило с отказом. Кандидаты остаются, домыслов нет.
func TestRouteProbeRefusedKeepsCandidates(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	probeRules(t, ts)

	fakeCore{
		rules: `{"rules":[
			{"type":"default","payload":"rule_set=[list-youtube]","proxy":"reject"}]}`,
		answer: `{"Status":5,"Question":[{"Name":"youtube.com."}],"RA":false}`,
	}.start(t, ts)

	got := decodeProbe(t, runProbe(t, ts, cookie, "youtube.com"))
	if !got.Refused {
		t.Fatalf("отказ по правилу не опознан: %+v", got)
	}
	if got.Rule != nil || len(got.Candidates) != 1 {
		t.Fatalf("кандидат снят при отказе резолвера: %+v", got)
	}
	if !strings.Contains(got.ResolveError, "REFUSED") {
		t.Errorf("resolve_error = %q, ожидалось про REFUSED", got.ResolveError)
	}
	if strings.Contains(got.Verdict, "вопрос к DNS") {
		t.Errorf("отказ по правилу назван вопросом к DNS: %q", got.Verdict)
	}
}

// Д2. Несуществующий домен: sing-box 1.12.25 отвечает кодом 200 и Status 3.
// Разбор по HTTP-коду это молча пропускал, и resolve_error не заполнялся никогда.
func TestRouteProbeNXDomainFromBody(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	own, _ := probeRules(t, ts)

	fakeCore{
		rules:  `{"rules":[{"type":"default","payload":"rule_set=rule-` + own.ID + `","proxy":"route(tun-x)"}]}`,
		answer: `{"Status":3,"Question":[{"Name":"nowhere149.example."}],"Authority":[{"name":"example.","type":6}]}`,
	}.start(t, ts)

	got := decodeProbe(t, runProbe(t, ts, cookie, "nowhere149.example"))
	if got.ResolveError == "" {
		t.Fatal("resolve_error пуст, хотя резолвер ответил NXDOMAIN")
	}
	if !strings.Contains(got.ResolveError, "NXDOMAIN") {
		t.Errorf("resolve_error = %q, ожидалось про NXDOMAIN", got.ResolveError)
	}
	if got.Refused {
		t.Error("NXDOMAIN принят за отказ по правилу")
	}
	if len(got.Addresses) != 0 {
		t.Errorf("адреса = %v, а их нет", got.Addresses)
	}
}
