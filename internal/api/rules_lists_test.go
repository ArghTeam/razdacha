package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
)

// listsRule заводит правило со всеми видами списков: community с подсетями,
// community без подсетей и свой по ссылке.
func listsRule(t *testing.T, ts *testServer) store.Rule {
	t.Helper()
	ctx := context.Background()
	tunnel, err := ts.st.CreateTunnel(ctx, store.Tunnel{
		Name: "Амстердам", Type: store.TunnelVLESS, Source: store.SourceURL,
		Raw: "vless://…", Parsed: json.RawMessage(`{"server":"1.2.3.4"}`), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	r, err := ts.st.CreateRule(ctx, store.Rule{
		Name: "Всё сразу", Action: store.ActionTunnel, TunnelID: tunnel.ID, Enabled: true,
		CommunityLists: []string{"telegram", "youtube"},
		RemoteLists:    []string{"https://example.com/my.lst", "https://example.com/dead.lst"},
		PeerScope:      store.ScopeAll,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	return r
}

func listRules(t *testing.T, ts *testServer, cookie *http.Cookie) []ruleResponse {
	t.Helper()
	res := ts.do(t, request{method: http.MethodGet, path: "/api/rules", cookies: []*http.Cookie{cookie}})
	if res.code != http.StatusOK {
		t.Fatalf("список правил = %d (%s)", res.code, res.body)
	}
	var got []ruleResponse
	if err := json.Unmarshal([]byte(res.body), &got); err != nil {
		t.Fatalf("разбор списка правил: %v (%s)", err, res.body)
	}
	return got
}

// Три состояния списка приезжают разными, а не схлопываются в «есть/нет»:
// обновился, не обновился с текстом причины, ни разу не обновлялся. Плюс
// четвёртое, честное: список, который качает не демон, а сам sing-box.
func TestRuleListsStatus(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	listsRule(t, ts)

	updated := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	tgURL, ok := lists.CommunitySubnetURL("telegram")
	if !ok {
		t.Fatal("у telegram пропали подсети — тест держится на них")
	}
	ts.listStates = func() map[string]ListState {
		return map[string]ListState{
			tgURL:                          {UpdatedAt: updated, Cached: true},
			"https://example.com/dead.lst": {FailedAt: updated, Err: "сервер ответил 404 Not Found"},
		}
	}

	rules := listRules(t, ts, cookie)
	if len(rules) != 1 {
		t.Fatalf("правил %d, ожидалось 1", len(rules))
	}
	got := rules[0].Lists
	if len(got) != 4 {
		t.Fatalf("состояний %d, ожидалось 4: %+v", len(got), got)
	}

	// telegram: подсети качает демон, и они обновились.
	if got[0].State != listUpdated || got[0].UpdatedAt == nil || !got[0].UpdatedAt.Equal(updated) {
		t.Errorf("telegram = %+v, ожидалось updated с временем", got[0])
	}
	if !got[0].SubnetsOnly {
		t.Error("у telegram не отмечено, что демон качает только подсети")
	}
	// youtube: подсетей нет, .srs ведёт сам sing-box — состояния у демона нет.
	if got[1].State != listCore || got[1].UpdatedAt != nil {
		t.Errorf("youtube = %+v, ожидалось core без времени", got[1])
	}
	// Свой список, до которого планировщик не дошёл, — не то же самое, что ошибка.
	if got[2].State != listNever || got[2].Error != "" {
		t.Errorf("my.lst = %+v, ожидалось never без ошибки", got[2])
	}
	// Свой список с отказом: причина едет текстом, а не флагом.
	if got[3].State != listFailed || got[3].Error == "" {
		t.Errorf("dead.lst = %+v, ожидалось failed с текстом ошибки", got[3])
	}
	if got[3].UpdatedAt != nil {
		t.Errorf("dead.lst: время обновления = %v, а удачного обновления не было", got[3].UpdatedAt)
	}
}

// Планировщик не поднялся — «неизвестно», а не «обновился»: списки в этом
// состоянии не обновляются вовсе, и зелёная отметка была бы враньём.
func TestRuleListsStatusWithoutScheduler(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	listsRule(t, ts)
	ts.listStates = nil

	got := listRules(t, ts, cookie)[0].Lists
	for _, st := range got {
		if st.Key == "youtube" {
			continue // .srs ведёт ядро, планировщик тут ни при чём
		}
		if st.State != listUnknown {
			t.Errorf("%+v: ожидалось unknown без планировщика", st)
		}
	}
}

// Правило без списков не получает выдуманных записей — только пустой массив.
func TestRuleListsStatusEmpty(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	ctx := context.Background()
	if _, err := ts.st.CreateRule(ctx, store.Rule{
		Name: "Свои домены", Action: store.ActionDirect, Enabled: true,
		Domains: []string{"bank.ru"}, PeerScope: store.ScopeAll,
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if got := listRules(t, ts, cookie)[0].Lists; len(got) != 0 {
		t.Errorf("состояний %d, ожидалось ни одного: %+v", len(got), got)
	}
}
