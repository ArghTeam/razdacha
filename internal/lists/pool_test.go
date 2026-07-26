package lists

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// catalogServer отдаёт сохранённые страницы каталога: разметка server-side, и её
// правка ломает разбор молча, поэтому разборщик проверяется файлом, а не сетью.
func catalogServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	var (
		mu       sync.Mutex
		requests int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()

		if r.URL.Query().Get("per_page") != "100" {
			t.Errorf("per_page = %q, а сайт принимает только 20/50/100",
				r.URL.Query().Get("per_page"))
		}
		page := r.URL.Query().Get("page")
		name := "vpnkeys-vless-page" + page + ".html"
		body, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			// Страниц в фикстуре две; третью сайт отдал бы без карточек.
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><body>ничего не найдено</body></html>"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

// Разбор фикстуры даёт весь пул за два запроса, включая метаданные карточек.
func TestPoolCatalogServers(t *testing.T) {
	srv, requests := catalogServer(t)
	c := &PoolCatalog{Client: srv.Client(), Log: quietSlog()}

	servers, err := c.Servers(context.Background(), srv.URL+"/protocol/vless")
	if err != nil {
		t.Fatalf("Servers: %v", err)
	}
	// Сайт заявляет 128 ключей, карточек без ссылки на первой странице одна.
	if len(servers) != 127 {
		t.Fatalf("снято %d ключей, в фикстуре 127", len(servers))
	}
	if *requests != 2 {
		t.Errorf("сделано %d запросов, страниц в каталоге две", *requests)
	}

	withPing, withCountry := 0, 0
	for _, s := range servers {
		if !strings.HasPrefix(s.URL, "vless://") {
			t.Fatalf("не ссылка vless: %q", s.URL)
		}
		// Подпись карточки идёт в атрибуте после самого URL через пробел и
		// обязана быть отрезана.
		if strings.ContainsAny(s.URL, " \t") {
			t.Fatalf("в ссылке остался хвост атрибута: %q", s.URL)
		}
		if s.PingMS > 0 {
			withPing++
		}
		if s.Country != "" {
			withCountry++
		}
	}
	if withPing == 0 {
		t.Error("ни у одного сервера не снят пинг — отбор лучших работать не будет")
	}
	if withCountry == 0 {
		t.Error("ни у одного сервера не снята страна")
	}
}

// Обход кончается по числу карточек, а не по числу ключей: карточка без
// data-key-url на первой странице есть, и порог по ключам оборвал бы обход на ней.
func TestPoolCatalogPageEndsByCards(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "vpnkeys-vless-page1.html"))
	if err != nil {
		t.Fatalf("чтение фикстуры: %v", err)
	}
	keys, cards := parseVPNKeysPage(string(body))
	if cards != poolPerPage {
		t.Fatalf("карточек на первой странице %d, ожидалось %d", cards, poolPerPage)
	}
	if len(keys) >= cards {
		t.Fatalf("ключей %d при %d карточках — карточка без ссылки не найдена, "+
			"а обход по ключам обрывался бы на первой странице", len(keys), cards)
	}
}

func TestPoolCatalogBadResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &PoolCatalog{Client: srv.Client(), Log: quietSlog()}
	_, err := c.Servers(context.Background(), srv.URL+"/protocol/vless")
	if !errors.Is(err, ErrBadResponse) {
		t.Fatalf("ожидалась ErrBadResponse, получено: %v", err)
	}
}

// Страница без карточек — не пул из нуля серверов, а ошибка: молча обнулить состав
// значило бы выкинуть из конфига работающие серверы.
func TestPoolCatalogEmptyPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body></body></html>"))
	}))
	defer srv.Close()

	c := &PoolCatalog{Client: srv.Client(), Log: quietSlog()}
	if _, err := c.Servers(context.Background(), srv.URL+"/protocol/vless"); err == nil {
		t.Fatal("пустой каталог принят за пул без серверов")
	}
}

// poolWriter — запись состава пула без БД.
type poolWriter struct {
	mu    sync.Mutex
	calls []string
	last  []store.PoolServer
	err   error
}

func (w *poolWriter) UpdateTunnelPool(_ context.Context, id string, servers []store.PoolServer, _ time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	w.calls = append(w.calls, id)
	w.last = servers
	return nil
}

func (w *poolWriter) written() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

func poolManager(t *testing.T, srv *httptest.Server, w *poolWriter) *PoolManager {
	t.Helper()
	return NewPoolManager(PoolManagerOptions{
		Catalog: &PoolCatalog{Client: srv.Client(), Log: quietSlog()},
		Writer:  w,
		Logger:  quietSlog(),
	})
}

// Первый прогон наполняет пул и сообщает об изменении; повторный на том же составе
// в БД не пишет и не будит перегенерацию конфига.
func TestPoolManagerWritesOnlyOnChange(t *testing.T) {
	srv, _ := catalogServer(t)
	w := &poolWriter{}
	m := poolManager(t, srv, w)

	catalog := srv.URL + "/protocol/vless"
	m.SetTunnels([]PoolTunnel{{ID: "pppp", Name: "пул", CatalogURL: catalog, Enabled: true}})

	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("первый прогон: %v", err)
	}
	if got := w.written(); len(got) != 1 {
		t.Fatalf("состав записан %d раз, ожидался один", len(got))
	}
	select {
	case <-m.Updates():
	default:
		t.Error("об изменении состава никто не узнал")
	}
	if m.LastRefresh().IsZero() {
		t.Error("время прогона не отмечено")
	}

	// Второй прогон: в БД уже лежит то же самое.
	m.SetTunnels([]PoolTunnel{{
		ID: "pppp", Name: "пул", CatalogURL: catalog, Enabled: true, Servers: w.last,
	}})
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("второй прогон: %v", err)
	}
	if got := w.written(); len(got) != 1 {
		t.Fatalf("неизменившийся состав перезаписан: записей %d", len(got))
	}
	select {
	case <-m.Updates():
		t.Error("неизменившийся состав разбудил перегенерацию конфига")
	default:
	}
}

// Пинг и подписи из каталога изменением состава не считаются: иначе каждый обход
// переписывал бы конфиг и перезапускал sing-box.
//
// Порядок серверов, в отличие от прежнего поведения, значим — это приоритет отбора в
// конфиг, и перетасованный список слияние приводит в порядок (см. TestMergePool*).
// Поэтому здесь дрейфует только то, что каталог действительно меняет от запроса к
// запросу: пинг и подпись карточки.
func TestPoolManagerIgnoresPingDrift(t *testing.T) {
	srv, _ := catalogServer(t)
	w := &poolWriter{}
	m := poolManager(t, srv, w)
	catalog := srv.URL + "/protocol/vless"

	m.SetTunnels([]PoolTunnel{{ID: "pppp", Name: "пул", CatalogURL: catalog, Enabled: true}})
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("первый прогон: %v", err)
	}

	stored := make([]store.PoolServer, len(w.last))
	for i, s := range w.last {
		s.PingMS += 137
		s.Title = "другая подпись"
		stored[i] = s
	}
	m.SetTunnels([]PoolTunnel{{
		ID: "pppp", Name: "пул", CatalogURL: catalog, Enabled: true, Servers: stored,
	}})
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("второй прогон: %v", err)
	}
	if got := w.written(); len(got) != 1 {
		t.Fatalf("дрейф пинга и порядка сочтён изменением состава: записей %d", len(got))
	}
}

// Выключенный пул список не обновляет: за него не ходят на чужой сайт и в БД не пишут.
func TestPoolManagerSkipsDisabled(t *testing.T) {
	srv, requests := catalogServer(t)
	w := &poolWriter{}
	m := poolManager(t, srv, w)

	m.SetTunnels([]PoolTunnel{{
		ID: "pppp", Name: "выключенный пул",
		CatalogURL: srv.URL + "/protocol/vless", Enabled: false,
	}})
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if *requests != 0 {
		t.Errorf("за выключенным пулом сходили в каталог %d раз", *requests)
	}
	if got := w.written(); len(got) != 0 {
		t.Errorf("состав выключенного пула перезаписан: %v", got)
	}
}

// Ошибка одного пула не отменяет остальные.
func TestPoolManagerOneFailureDoesNotStopOthers(t *testing.T) {
	srv, _ := catalogServer(t)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()

	w := &poolWriter{}
	m := poolManager(t, srv, w)
	m.SetTunnels([]PoolTunnel{
		{ID: "broken", Name: "битый", CatalogURL: broken.URL + "/protocol/vless", Enabled: true},
		{ID: "pppp", Name: "пул", CatalogURL: srv.URL + "/protocol/vless", Enabled: true},
	})

	err := m.Refresh(context.Background())
	if err == nil {
		t.Fatal("ошибка битого каталога потерялась")
	}
	got := w.written()
	if len(got) != 1 || got[0] != "pppp" {
		t.Fatalf("исправный пул не обновился: %v", got)
	}
}

// PoolTunnels выбирает из снимка только пулы, сохраняя выключенные: решение
// «обновлять или нет» принимает расписание.
func TestPoolTunnels(t *testing.T) {
	snap := store.Snapshot{Tunnels: []store.Tunnel{
		{ID: "a", Name: "обычный", Source: store.SourceURL, Raw: "vless://…", Enabled: true},
		{
			ID: "b", Name: "пул", Source: store.SourcePool, Enabled: false,
			Raw:  DefaultPoolCatalogURL,
			Pool: []store.PoolServer{{URL: "vless://x@1.2.3.4:443"}},
		},
	}}
	got := PoolTunnels(snap)
	if len(got) != 1 {
		t.Fatalf("выбрано %d пулов, ожидался один: %+v", len(got), got)
	}
	if got[0].ID != "b" || got[0].CatalogURL != DefaultPoolCatalogURL ||
		got[0].Enabled || len(got[0].Servers) != 1 {
		t.Errorf("пул выбран не целиком: %+v", got[0])
	}
}

// poolCards изображает выдачу каталога: n карточек, пинг убывает с номером, так что
// лучшие по пингу — последние.
func poolCards(n int) []store.PoolServer {
	out := make([]store.PoolServer, 0, n)
	for i := range n {
		out = append(out, store.PoolServer{
			URL:     fmt.Sprintf("vless://key-%02d@10.0.0.%d:443", i, i+1),
			Country: "Нидерланды",
			Title:   fmt.Sprintf("сервер %d", i),
			PingMS:  1000 - i,
		})
	}
	return out
}

// poolURLs — ссылки списка по порядку.
func poolURLs(servers []store.PoolServer) []string {
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		out = append(out, s.URL)
	}
	return out
}

// poolWithout — выдача каталога без перечисленных ссылок и с уехавшим пингом: сайт
// отдаёт часть карточек без ссылки на ключ и меняет пинг на каждом обходе.
func poolWithout(servers []store.PoolServer, skip ...string) []store.PoolServer {
	skipped := make(map[string]bool, len(skip))
	for _, u := range skip {
		skipped[u] = true
	}
	out := make([]store.PoolServer, 0, len(servers))
	for i := len(servers) - 1; i >= 0; i-- {
		s := servers[i]
		if skipped[s.URL] {
			continue
		}
		s.PingMS += 137
		s.Title = "подпись этого обхода"
		out = append(out, s)
	}
	return out
}

// Первый обход выстраивает пул по пингу: окна ещё нет, все карточки новые, и в его
// начале — лучшие. Пинг не показан — сервер уходит в конец, но не выбрасывается.
func TestMergePoolFirstCrawlOrdersByPing(t *testing.T) {
	cards := poolCards(4)
	cards = append(cards, store.PoolServer{URL: "vless://key-nп@10.0.0.9:443"})

	merged, changed := MergePool(nil, cards)
	if !changed {
		t.Fatal("первый обход не счёлся изменением")
	}
	want := []string{cards[3].URL, cards[2].URL, cards[1].URL, cards[0].URL, cards[4].URL}
	if got := poolURLs(merged); !slices.Equal(got, want) {
		t.Errorf("порядок %v, ожидался %v", got, want)
	}
}

// Пинг и подпись известного сервера при обходе не перезаписываются, порядок не
// пересчитывается: иначе отбор ехал бы от шума измерений чужого сайта, а с ним
// менялся бы и конфиг (issue #68).
func TestMergePoolFreezesKnownCards(t *testing.T) {
	cards := poolCards(20)
	stored, _ := MergePool(nil, poolWithout(cards))

	merged, changed := MergePool(stored, poolWithout(cards))
	if changed {
		t.Error("дрейф пинга и подписей сочтён изменением состава")
	}
	if !slices.Equal(poolURLs(merged), poolURLs(stored)) {
		t.Error("порядок пула пересчитался при неизменившемся наборе")
	}
	for i, s := range merged {
		if s.PingMS != stored[i].PingMS || s.Title != stored[i].Title {
			t.Fatalf("запись %d перезаписана: %+v, было %+v", i, s, stored[i])
		}
	}
}

// Новая карточка не выселяет из окна конфига живой сервер, даже если у неё пинг лучше
// всех: смена окна стоит перезапуска sing-box, а работающему серверу замена не нужна.
func TestMergePoolNewcomerWaitsBehindWindow(t *testing.T) {
	cards := poolCards(20)
	stored, _ := MergePool(nil, poolWithout(cards))

	newcomer := store.PoolServer{URL: "vless://новый@10.9.9.9:443", PingMS: 1}
	merged, changed := MergePool(stored, append(poolWithout(cards), newcomer))
	if !changed {
		t.Fatal("новая карточка не попала в пул")
	}
	if !slices.Equal(poolURLs(merged)[:PoolConfigServers], poolURLs(stored)[:PoolConfigServers]) {
		t.Error("новая карточка сдвинула окно конфига")
	}
	// В хвосте она встаёт по пингу, то есть первой за окном.
	if got := merged[PoolConfigServers].URL; got != newcomer.URL {
		t.Errorf("за окном первым стоит %q, ожидался новый сервер с лучшим пингом", got)
	}
}

// Карточка, не пришедшая на одном обходе, исчезнувшим сервером не считается: сайт
// отдаёт часть карточек без ссылки почти на каждом запросе. Место она уступает только
// после нескольких обходов подряд, и ровно своё — остальные не сдвигаются.
func TestMergePoolReplacesOnlyAfterRepeatedMiss(t *testing.T) {
	cards := poolCards(20)
	stored, _ := MergePool(nil, poolWithout(cards))
	victim := stored[3]

	merged, changed := MergePool(stored, poolWithout(cards, victim.URL))
	if !changed {
		t.Fatal("пропуск карточки не отмечен в БД")
	}
	if merged[3].URL != victim.URL {
		t.Fatalf("сервер уступил место после одного пропуска: на его позиции %q", merged[3].URL)
	}
	if merged[3].Misses != 1 {
		t.Errorf("пропуск не посчитан: misses=%d", merged[3].Misses)
	}
	if !slices.Equal(poolURLs(merged), poolURLs(stored)) {
		t.Error("одиночный пропуск карточки сдвинул состав пула")
	}

	for i := 1; i < poolMissesBeforeDrop; i++ {
		merged, _ = MergePool(merged, poolWithout(cards, victim.URL))
	}

	if merged[3].URL == victim.URL {
		t.Fatalf("сервер не уступил место после %d обходов подряд", poolMissesBeforeDrop)
	}
	// Место занимает лучший по пингу из остальных: пинг убывает с номером, серверов
	// на четыре больше окна, значит это четвёртый по счёту.
	if want := cards[len(cards)-PoolConfigServers-1].URL; merged[3].URL != want {
		t.Errorf("место занял %q, ожидался лучший по пингу из остальных %q", merged[3].URL, want)
	}
	// Все прочие позиции окна на месте: позиция — это тег участника в конфиге.
	for i := 0; i < PoolConfigServers; i++ {
		if i == 3 {
			continue
		}
		if merged[i].URL != stored[i].URL {
			t.Errorf("позиция %d переехала с %q на %q", i, stored[i].URL, merged[i].URL)
		}
	}
	// Исчезнувший сервер из пула ушёл, а его место занял бывший хвостовой: серверов
	// стало на одного меньше.
	if len(merged) != len(stored)-1 {
		t.Errorf("серверов стало %d, ожидалось %d", len(merged), len(stored)-1)
	}
}

// Пагинация добавляется к адресу каталога, не затирая его параметров.
func TestPoolPageURL(t *testing.T) {
	got, err := poolPageURL("https://vpnkeys.me/protocol/vless?sort=ping", 2)
	if err != nil {
		t.Fatalf("poolPageURL: %v", err)
	}
	for _, want := range []string{"page=2", "per_page=100", "sort=ping"} {
		if !strings.Contains(got, want) {
			t.Errorf("в адресе %q нет %q", got, want)
		}
	}
}
