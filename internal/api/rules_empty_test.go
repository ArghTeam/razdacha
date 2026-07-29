package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

// Правило без единого условия совпадения отклоняется на записи: в конфиг оно не
// попадает, и его трафик уходит напрямую с адреса сервера (issue #142, ADR 0013).
func TestRuleWithoutMatchRejected(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	first, _ := chainTunnels(t, ts)

	// Действие роли не играет, выбранные пиры условием совпадения не считаются.
	cases := map[string]string{
		"в туннель":   `{"name":"Пустое","action":"tunnel","tunnel_id":"` + first.ID + `","peer_scope":"all"}`,
		"напрямую":    `{"name":"Пустое","action":"direct","peer_scope":"all"}`,
		"блокировать": `{"name":"Пустое","action":"block","peer_scope":"all"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			res := createRule(t, ts, cookie, body)
			if res.code != http.StatusBadRequest {
				t.Fatalf("код %d, ожидался 400; тело %s", res.code, res.body)
			}
			if !strings.Contains(res.body, "условия совпадения") {
				t.Errorf("в отказе нет причины: %s", res.body)
			}
		})
	}
}

// Снять последнее условие через PATCH тоже нельзя: иначе рабочее правило
// превращалось бы в утечку правкой одного поля.
func TestRuleUpdateKeepsMatchRequired(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	res := createRule(t, ts, cookie,
		`{"name":"YouTube","action":"direct","domains":["youtube.com"],"peer_scope":"all"}`)
	if res.code != http.StatusCreated {
		t.Fatalf("код %d, тело %s", res.code, res.body)
	}
	var created store.Rule
	if err := json.Unmarshal([]byte(res.body), &created); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}

	bad := ts.do(t, request{
		method: http.MethodPatch, path: "/api/rules/" + created.ID,
		body:    `{"domains":[]}`,
		cookies: []*http.Cookie{cookie},
	})
	if bad.code != http.StatusBadRequest {
		t.Fatalf("код %d, ожидался 400; тело %s", bad.code, bad.body)
	}
	if !strings.Contains(bad.body, "условия совпадения") {
		t.Errorf("в отказе нет причины: %s", bad.body)
	}

	// Обмен одного условия на другое остаётся законным.
	good := ts.do(t, request{
		method: http.MethodPatch, path: "/api/rules/" + created.ID,
		body:    `{"domains":[],"subnets":["192.0.2.0/24"]}`,
		cookies: []*http.Cookie{cookie},
	})
	if good.code != http.StatusOK {
		t.Fatalf("код %d, тело %s", good.code, good.body)
	}
}
