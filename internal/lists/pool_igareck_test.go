package lists

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// stubGeo — geoip-заглушка: страна по адресу задаётся картой, чтобы тест не зависел
// от встроенной базы и её обновлений.
func stubGeo(m map[string]string) func(net.IP) string {
	return func(ip net.IP) string { return m[ip.String()] }
}

// igareckGeo — общая раскладка адресов по странам для фикстур ниже.
func igareckGeo() func(net.IP) string {
	return stubGeo(map[string]string{
		"10.0.0.1": "NL", "10.0.0.2": "DE", "10.0.0.3": "NL",
		"10.0.0.4": "DE", "10.0.0.5": "NL", "10.0.0.6": "RU",
	})
}

// igareckResolve — резолвер-заглушка: единственный домен фикстуры даёт NL-адрес.
func igareckResolve(_ context.Context, name string) ([]net.IP, error) {
	if name == "nl.example.com" {
		return []net.IP{net.ParseIP("10.0.0.3")}, nil
	}
	return nil, fmt.Errorf("нет записи для %s", name)
}

// igareckDriver — драйвер с подменённой сетью: ни DNS, ни geoip настоящих не трогает.
func igareckDriver() igareck {
	return igareck{resolve: igareckResolve, country: igareckGeo()}
}

// igareckCheckKey повторяет поведение настоящей проверки (`singbox.Parse`) в пределах,
// нужных тесту: vmess генератор конфига не берёт, значит такой ключ отсеивается.
// Импортировать singbox слой lists не может (инвариант), поэтому проверка — заглушка.
func igareckCheckKey(raw string) error {
	if strings.HasPrefix(strings.ToLower(raw), "vmess://") {
		return fmt.Errorf("протокол vmess не поддерживается")
	}
	return nil
}

// igareckSubs — тело трёх подписок для фикстуры. Ключи разных схем и стран; часть
// адресов — домены, чтобы проверить резолв.
func igareckSubs() map[string]string {
	return map[string]string{
		"BLACK_VLESS_RUS_mobile.txt": strings.Join([]string{
			"# заголовок подписки, не ключ",
			"vless://uuid-1@10.0.0.1:443?security=reality#NL-1",
			"vless://uuid-2@10.0.0.2:443?security=reality#DE-1",
			"", // пустая строка
		}, "\n"),
		"BLACK_VLESS_RUS.txt": strings.Join([]string{
			"vless://uuid-3@nl.example.com:443?security=reality#NL-domain",
			"trojan://pass@10.0.0.4:443#DE-2",
			"vless://uuid-1@10.0.0.1:443?security=reality#NL-1", // точный дубль ключа mobile
		}, "\n"),
		"BLACK_SS+All_RUS.txt": strings.Join([]string{
			// SIP002: userinfo@host:port
			"ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pass")) + "@10.0.0.5:8388#NL-ss",
			// vmess: генератор конфига его не берёт — уйдёт в пропуск
			"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"10.0.0.6","port":"443","id":"x"}`)) + "#vmess",
			"# комментарий",
		}, "\n"),
	}
}

// igareckStub поднимает зеркало igareck на сохранённых подписках и регистрирует
// драйвер с подменённой сетью на адрес сервера. Возвращает адрес каталога (базовый URL
// с путём репозитория) и счётчик запросов.
func igareckStub(t *testing.T, subs map[string]string) (string, *int) {
	t.Helper()
	var (
		mu       sync.Mutex
		requests int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		name := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
		body, ok := subs[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	// Резерв драйвера указывает на тот же тестовый сервер: настоящий
	// raw.githubusercontent.com в тесте трогать нельзя, а так падение основного (если
	// оно случится) остаётся внутри httptest.
	host := srv.Listener.Addr().(*net.TCPAddr).IP.String()
	drv := igareckDriver()
	drv.fallback = srv.Listener.Addr().(*net.TCPAddr).String()
	t.Cleanup(RegisterPoolDriver(host, drv))
	return srv.URL + "/igareck/vpn-configs-for-russia/main/", &requests
}

// Драйвер отдаёт все серверы с проставленной страной exit-адреса: IP — напрямую,
// домен — через резолв. Дубль ключа между файлами схлопывается, vmess отсеивается
// нашим парсером.
func TestIgareckServers(t *testing.T) {
	catalog, requests := igareckStub(t, igareckSubs())
	c := &PoolCatalog{Log: quietSlog(), Pause: -1, CheckKey: igareckCheckKey}

	servers, err := c.Servers(context.Background(), catalog)
	if err != nil {
		t.Fatalf("Servers: %v", err)
	}
	// Три файла — три запроса: обход одношаговый, за отдельным ключом не ходят.
	if *requests != 3 {
		t.Errorf("сделано %d запросов, ожидалось 3", *requests)
	}

	byCountry := map[string]int{}
	for _, s := range servers {
		byCountry[s.Country]++
		if s.PingMS != 0 {
			t.Errorf("у %q взялся пинг, источник его не даёт", s.URL)
		}
	}
	// NL: uuid-1(10.0.0.1), uuid-3(домен nl→10.0.0.3), ss(10.0.0.5) = 3.
	// DE: uuid-2(10.0.0.2), trojan(10.0.0.4) = 2. vmess отсеян парсером, дубль
	// uuid-1 схлопнут.
	if byCountry["NL"] != 3 {
		t.Errorf("NL серверов %d, ожидалось 3: %v", byCountry["NL"], byCountry)
	}
	if byCountry["DE"] != 2 {
		t.Errorf("DE серверов %d, ожидалось 2: %v", byCountry["DE"], byCountry)
	}
	if len(servers) != 5 {
		t.Errorf("всего серверов %d, ожидалось 5 (vmess отсеян, дубль схлопнут): %v",
			len(servers), servers)
	}
}

// Один обход общего источника наполняет несколько страновых пулов — каждый только
// серверами своей страны (fetch-once-partition, ADR 0017).
func TestIgareckFetchOncePartition(t *testing.T) {
	catalog, requests := igareckStub(t, igareckSubs())
	w := newWriterByID()
	m := NewPoolManager(PoolManagerOptions{
		Catalog: &PoolCatalog{Log: quietSlog(), Pause: -1, CheckKey: igareckCheckKey},
		Writer:  w,
		Logger:  quietSlog(),
	})
	m.SetTunnels([]PoolTunnel{
		{ID: "nl", Name: "🇳🇱 Нидерланды", CatalogURL: catalog, Enabled: true, Country: "NL"},
		{ID: "de", Name: "🇩🇪 Германия", CatalogURL: catalog, Enabled: true, Country: "DE"},
		{ID: "fr", Name: "🇫🇷 Франция", CatalogURL: catalog, Enabled: true, Country: "FR"},
	})

	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// Три пула на общем каталоге — один обход, три файла. Не девять.
	if *requests != 3 {
		t.Fatalf("сделано %d запросов, ожидалось 3 (один обход на группу)", *requests)
	}

	if got := len(w.get("nl")); got != 3 {
		t.Errorf("в пул NL легло %d серверов, ожидалось 3", got)
	}
	if got := len(w.get("de")); got != 2 {
		t.Errorf("в пул DE легло %d серверов, ожидалось 2", got)
	}
	// У FR своих серверов нет — пустой пул в БД не пишется вовсе (MergePool не увидел
	// изменения), а чужими серверами не наполняется.
	if w.wrote("fr") {
		t.Errorf("пустой пул FR записан: %v", w.get("fr"))
	}
	for _, id := range []string{"nl", "de"} {
		for _, s := range w.get(id) {
			if !strings.EqualFold(s.Country, id) {
				t.Errorf("в пул %s попал сервер страны %s: %q", id, s.Country, s.URL)
			}
		}
	}
}

// Выключенный пул общий источник не тянет: за него на чужой сайт не ходят.
func TestIgareckSkipsDisabled(t *testing.T) {
	catalog, requests := igareckStub(t, igareckSubs())
	w := newWriterByID()
	m := NewPoolManager(PoolManagerOptions{
		Catalog: &PoolCatalog{Log: quietSlog(), Pause: -1, CheckKey: igareckCheckKey},
		Writer:  w,
		Logger:  quietSlog(),
	})
	m.SetTunnels([]PoolTunnel{
		{ID: "nl", Name: "NL", CatalogURL: catalog, Enabled: false, Country: "NL"},
	})
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if *requests != 0 {
		t.Errorf("за выключенным пулом сходили в каталог %d раз", *requests)
	}
	if w.wrote("nl") {
		t.Errorf("состав выключенного пула записан: %v", w.get("nl"))
	}
}

// Недоступность основного зеркала → переход на резерв, обход не срывается. Проверяется
// перебор зеркал напрямую (`fetchSubscription`): настоящий резервный хост
// (raw.githubusercontent.com) в тесте недоступен, поэтому падение подменяет фейковый
// обход, фиксирующий порядок запрошенных URL.
func TestIgareckMirrorFallback(t *testing.T) {
	catalog, err := url.Parse("https://" + igareckPrimaryHost + "/igareck/vpn-configs-for-russia/main/")
	if err != nil {
		t.Fatalf("разбор каталога: %v", err)
	}

	var tried []string
	c := &poolCrawl{
		log: quietSlog(),
		fetch: func(_ context.Context, pageURL string) (string, error) {
			tried = append(tried, pageURL)
			// Основное зеркало «лежит», резерв отвечает.
			if strings.Contains(pageURL, igareckPrimaryHost) {
				return "", fmt.Errorf("основное зеркало 500")
			}
			return "vless://uuid@10.0.0.1:443#ok", nil
		},
	}

	body, err := igareckDriver().fetchSubscription(context.Background(), c, catalog, "BLACK_VLESS_RUS.txt")
	if err != nil {
		t.Fatalf("fetchSubscription: %v", err)
	}
	if !strings.Contains(body, "vless://") {
		t.Errorf("резерв не дал тело: %q", body)
	}
	if len(tried) != 2 {
		t.Fatalf("зеркал перебрано %d, ожидалось 2 (основное, затем резерв): %v", len(tried), tried)
	}
	if !strings.Contains(tried[0], igareckPrimaryHost) {
		t.Errorf("первым спрашивалось не основное зеркало: %s", tried[0])
	}
	if !strings.Contains(tried[1], igareckFallbackHost) {
		t.Errorf("резервом оказался не %s: %s", igareckFallbackHost, tried[1])
	}
	// Путь к файлу на обоих зеркалах одинаков и содержит имя подписки как есть.
	if !strings.HasSuffix(tried[1], "/igareck/vpn-configs-for-russia/main/BLACK_VLESS_RUS.txt") {
		t.Errorf("путь резерва собран неверно: %s", tried[1])
	}
}

// Оба зеркала недоступны — подписка пропускается, но обход остальных файлов не
// срывается: страны из уцелевших файлов наполняются.
func TestIgareckSubscriptionFailureDoesNotStopCrawl(t *testing.T) {
	subs := igareckSubs()
	delete(subs, "BLACK_VLESS_RUS.txt") // этот файл сервер отдаст 404 с обоих «зеркал»
	catalog, _ := igareckStub(t, subs)
	c := &PoolCatalog{Log: quietSlog(), Pause: -1, CheckKey: igareckCheckKey}

	// Резерв (raw.githubusercontent.com) в тесте недоступен, но httptest-хост каталога
	// — единственное зеркало, отдающее 404 на пропавший файл; драйвер сунется и на
	// резерв, тот тоже упадёт, файл пропустится. Остальные два файла разберутся.
	servers, err := c.Servers(context.Background(), catalog)
	if err != nil {
		t.Fatalf("Servers: %v", err)
	}
	// Без BLACK_VLESS_RUS.txt пропали uuid-3(NL-домен), trojan(DE-2) и дубль. Остаётся
	// NL: uuid-1, ss = 2; DE: uuid-2 = 1. Всего 3.
	if len(servers) != 3 {
		t.Fatalf("собрано %d серверов, ожидалось 3 после падения одной подписки: %v",
			len(servers), servers)
	}
}

// poolKeyHost достаёт хост из ссылок разных схем.
func TestPoolKeyHost(t *testing.T) {
	ssSIP002 := "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pass")) + "@203.0.113.7:8388#tag"
	ssLegacy := "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pass@203.0.113.8:8388")) + "#tag"
	vmess := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"203.0.113.9","port":"443"}`))
	cases := []struct{ raw, host string }{
		{"vless://uuid@198.51.100.1:443?security=reality#x", "198.51.100.1"},
		{"trojan://pass@example.org:443#y", "example.org"},
		{ssSIP002, "203.0.113.7"},
		{ssLegacy, "203.0.113.8"},
		{vmess, "203.0.113.9"},
		{"мусор без схемы", ""},
	}
	for _, c := range cases {
		if got := poolKeyHost(c.raw); got != c.host {
			t.Errorf("poolKeyHost(%.30q) = %q, ожидалось %q", c.raw, got, c.host)
		}
	}
}

// writerByID — запись состава пула с разбивкой по идентификатору туннеля: нужно
// проверить, что каждый пул получил именно свой страновой срез.
type writerByID struct {
	mu      sync.Mutex
	written map[string][]store.PoolServer
}

func newWriterByID() *writerByID {
	return &writerByID{written: map[string][]store.PoolServer{}}
}

func (w *writerByID) UpdateTunnelPool(_ context.Context, id string, servers []store.PoolServer, _ time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.written[id] = servers
	return nil
}

func (w *writerByID) get(id string) []store.PoolServer {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.written[id]
}

func (w *writerByID) wrote(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.written[id]
	return ok
}
