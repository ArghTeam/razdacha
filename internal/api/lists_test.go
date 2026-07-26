package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
)

// TestCommunityListsRequiresSession — каталог за сессией: неавторизованный
// запрос не должен отличать «эндпоинта нет» от «данных нет».
func TestCommunityListsRequiresSession(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.do(t, request{method: http.MethodGet, path: "/api/lists/community"})
	if resp.code != http.StatusUnauthorized {
		t.Fatalf("без сессии = %d, ожидался 401 (%s)", resp.code, resp.body)
	}
	if got := decodeError(t, resp).Code; got != codeUnauthorized {
		t.Errorf("код ошибки = %q, ожидался %q", got, codeUnauthorized)
	}
}

// TestCommunityListsCatalog — под сессией отдаётся каталог из слоя lists,
// массивом: UI кладёт ответ прямо в state.communityLists.
func TestCommunityListsCatalog(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.do(t, request{
		method:  http.MethodGet,
		path:    "/api/lists/community",
		cookies: []*http.Cookie{cookie},
	})
	if resp.code != http.StatusOK {
		t.Fatalf("каталог = %d, ожидался 200 (%s)", resp.code, resp.body)
	}

	var got []lists.CommunityService
	if err := json.Unmarshal([]byte(resp.body), &got); err != nil {
		t.Fatalf("разбор каталога: %v (%s)", err, resp.body)
	}
	want := lists.Catalog()
	if len(got) != len(want) {
		t.Fatalf("в каталоге %d записей, ожидалось %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("запись %d = %+v, ожидалась %+v", i, got[i], want[i])
		}
	}

	// Сеть и БД для ответа не нужны: каталог статический, и он должен приехать
	// даже когда планировщик ничего не скачал.
	if len(got) == 0 {
		t.Error("каталог пуст")
	}
}
