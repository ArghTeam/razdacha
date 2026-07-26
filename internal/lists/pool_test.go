package lists

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// Порядок и пинг из каталога изменением не считаются: иначе каждый обход
// переписывал бы конфиг и перезапускал sing-box.
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
		stored[len(w.last)-1-i] = s
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
