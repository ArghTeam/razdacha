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
	drv := igareck{fallback: srv.Listener.Addr().(*net.TCPAddr).String()}
	t.Cleanup(RegisterPoolDriver(host, drv))
	return srv.URL + "/igareck/vpn-configs-for-russia/main/", &requests
}

// Драйвер отдаёт все прошедшие парсер серверы одним списком, без страны. Дубль ключа
// между файлами схлопывается, vmess отсеивается нашим парсером.
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

	for _, s := range servers {
		if s.PingMS != 0 {
			t.Errorf("у %q взялся пинг, источник его не даёт", s.URL)
		}
		if s.Country != "" {
			t.Errorf("у %q проставлена страна, geo-IP убран (ADR 0018): %q", s.URL, s.Country)
		}
	}
	// uuid-1, uuid-2, uuid-3(домен), trojan, ss = 5. vmess отсеян парсером, дубль
	// uuid-1 схлопнут.
	if len(servers) != 5 {
		t.Errorf("всего серверов %d, ожидалось 5 (vmess отсеян, дубль схлопнут): %v",
			len(servers), servers)
	}
}

// Пулы с общим каталогом обходятся один раз на группу: два пула на одном источнике —
// один обход (три файла), и каждый получает всю выдачу целиком (ADR 0018).
func TestIgareckFetchOnce(t *testing.T) {
	catalog, requests := igareckStub(t, igareckSubs())
	w := newWriterByID()
	m := NewPoolManager(PoolManagerOptions{
		Catalog: &PoolCatalog{Log: quietSlog(), Pause: -1, CheckKey: igareckCheckKey},
		Writer:  w,
		Logger:  quietSlog(),
	})
	m.SetTunnels([]PoolTunnel{
		{ID: "a", Name: "Пул A", CatalogURL: catalog, Enabled: true},
		{ID: "b", Name: "Пул B", CatalogURL: catalog, Enabled: true},
	})

	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// Два пула на общем каталоге — один обход, три файла. Не шесть.
	if *requests != 3 {
		t.Fatalf("сделано %d запросов, ожидалось 3 (один обход на группу)", *requests)
	}

	for _, id := range []string{"a", "b"} {
		if got := len(w.get(id)); got != 5 {
			t.Errorf("в пул %s легло %d серверов, ожидалось 5", id, got)
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
		{ID: "nl", Name: "NL", CatalogURL: catalog, Enabled: false},
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

	body, err := igareck{}.fetchSubscription(context.Background(), c, catalog, "BLACK_VLESS_RUS.txt")
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
// срывается: ключи из уцелевших файлов собираются.
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
	// Без BLACK_VLESS_RUS.txt пропали uuid-3(домен), trojan и дубль. Остаётся
	// uuid-1, ss = 2; uuid-2 = 1. Всего 3.
	if len(servers) != 3 {
		t.Fatalf("собрано %d серверов, ожидалось 3 после падения одной подписки: %v",
			len(servers), servers)
	}
}

// writerByID — запись состава пула с разбивкой по идентификатору туннеля: нужно
// проверить, что каждый пул получил выдачу.
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
