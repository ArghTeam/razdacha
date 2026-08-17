package lists

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// fixture читает сохранённую страницу источника.
func fixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("чтение фикстуры %s: %v", name, err)
	}
	return string(body)
}

// reKeyID — номер ключа в пути `/key/<id>/`.
var reKeyID = regexp.MustCompile(`^/key/(\d+)/$`)

// catalogServer поднимает копию outlinekeys на сохранённых страницах: разметка
// server-side, и её правка ломает разбор молча, поэтому разборщик проверяется
// файлами, а не сетью.
//
// Драйвер выбирается по хосту, а httptest живёт на 127.0.0.1 со случайным портом —
// поэтому тот же драйвер регистрируется на адрес сервера. Страницы ключей одна и та
// же фикстура с подставленным номером: сайт отдаёт на каждой свой ключ, а фикстур у
// нас три.
func catalogServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	var (
		mu       sync.Mutex
		requests int
	)
	pages := []string{
		fixture(t, "outlinekeys-outline-page1.html"),
		fixture(t, "outlinekeys-outline-page2.html"),
		fixture(t, "outlinekeys-outline-page3.html"),
	}
	key := fixture(t, "outlinekeys-key-online.html")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")

		if m := reKeyID.FindStringSubmatch(r.URL.Path); m != nil {
			// Порт в ссылке — номер ключа: иначе все страницы отдали бы один и
			// тот же ключ, и обход схлопнулся бы в один сервер.
			_, _ = io.WriteString(w, strings.ReplaceAll(key, "212.193.6.27:234",
				"212.193.6.27:"+m[1]))
			return
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if n < 1 || n > len(pages) {
			// За последней страницей сайт отдаёт вёрстку без карточек и код 200.
			n = len(pages)
		}
		_, _ = io.WriteString(w, pages[n-1])
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(RegisterPoolDriver(srv.Listener.Addr().(*net.TCPAddr).IP.String(), outlineKeys{}))
	return srv, &requests
}

// catalogAddr — адрес каталога на тестовом сервере. Раздел тот единственный,
// который драйвер берёт.
func catalogAddr(srv *httptest.Server) string {
	return srv.URL + outlineKeysSection
}

// poolCatalog — разборщик без пауз: ждать по 300 мс между запросами в тестах незачем.
func poolCatalog(srv *httptest.Server) *PoolCatalog {
	return &PoolCatalog{Client: srv.Client(), Log: quietSlog(), Pause: -1}
}

// Обход фикстуры: две страницы с карточками, третья пустая — и по одному запросу
// на каждый живой ключ. Мёртвые карточки отдельного запроса не стоят.
func TestPoolCatalogServers(t *testing.T) {
	srv, requests := catalogServer(t)
	c := poolCatalog(srv)

	servers, err := c.Servers(context.Background(), catalogAddr(srv))
	if err != nil {
		t.Fatalf("Servers: %v", err)
	}
	// На первой странице фикстуры двадцать живых карточек, на второй пять живых
	// из двадцати, третья пустая — это конец каталога.
	if len(servers) != 25 {
		t.Fatalf("снято %d ключей, в фикстуре 25", len(servers))
	}
	// Три страницы списка плюс страница на каждый снятый ключ. Мёртвые карточки в
	// счёт не идут: за ними не ходили.
	if want := 3 + len(servers); *requests != want {
		t.Errorf("сделано %d запросов, ожидалось %d", *requests, want)
	}

	withCountry := 0
	for _, s := range servers {
		if !strings.HasPrefix(s.URL, "ss://") {
			t.Fatalf("не ссылка shadowsocks: %q", s.URL)
		}
		if strings.ContainsAny(s.URL, " \t\n") {
			t.Fatalf("в ссылке остался мусор разметки: %q", s.URL)
		}
		// Пинга этот источник не даёт вовсе, и выдумывать его нельзя.
		if s.PingMS != 0 {
			t.Fatalf("у сервера %q взялся пинг %d, а источник его не измеряет",
				s.Title, s.PingMS)
		}
		if s.Country != "" {
			withCountry++
		}
	}
	if withCountry != len(servers) {
		t.Errorf("страна снята у %d серверов из %d", withCountry, len(servers))
	}
}

// Мёртвая по мнению сайта карточка в пул не идёт и запроса за ключом не стоит.
func TestOutlineKeysSkipsOffline(t *testing.T) {
	cards := parseOutlineKeysList(fixture(t, "outlinekeys-outline-page2.html"))
	online, offline := 0, 0
	for _, c := range cards {
		if c.online {
			online++
		} else {
			offline++
		}
	}
	if online != 5 || offline != 15 {
		t.Fatalf("на странице %d живых и %d мёртвых, в фикстуре 5 и 15", online, offline)
	}
}

// Конец каталога — страница без карточек: сайт отдаёт её с кодом 200, а не 404.
func TestOutlineKeysEmptyPageEndsCrawl(t *testing.T) {
	if cards := parseOutlineKeysList(fixture(t, "outlinekeys-outline-page3.html")); len(cards) != 0 {
		t.Fatalf("на пустой странице нашлось %d карточек", len(cards))
	}
}

// Разделы vless и trojan того же сайта не собираются вовсе: их ссылки приходят со
// срезанным query, проходят парсер и гарантированно не соединяются (ADR 0015).
func TestOutlineKeysRefusesTruncatedSections(t *testing.T) {
	srv, requests := catalogServer(t)
	c := poolCatalog(srv)

	for _, section := range []string{"/protocols/vless/", "/protocols/trojan/", "/"} {
		_, err := c.Servers(context.Background(), srv.URL+section)
		if err == nil {
			t.Fatalf("раздел %s принят", section)
		}
		if !errors.Is(err, store.ErrInvalid) {
			t.Errorf("раздел %s отвергнут не как неверный ввод: %v", section, err)
		}
	}
	if *requests != 0 {
		t.Errorf("за чужим разделом сходили в сеть %d раз", *requests)
	}
}

// Ключ не той схемы в БД не попадает, даже если сайт отдал его в разделе outline:
// раздел мы выбрали, но подтверждение берём из самой ссылки.
func TestOutlineKeysSkipsForeignScheme(t *testing.T) {
	list := fixture(t, "outlinekeys-outline-page1.html")
	vless := fixture(t, "outlinekeys-key-vless.html")
	empty := fixture(t, "outlinekeys-outline-page3.html")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case reKeyID.MatchString(r.URL.Path):
			_, _ = io.WriteString(w, vless)
		case r.URL.Query().Get("page") == "1":
			_, _ = io.WriteString(w, list)
		default:
			_, _ = io.WriteString(w, empty)
		}
	}))
	defer srv.Close()
	defer RegisterPoolDriver(srv.Listener.Addr().(*net.TCPAddr).IP.String(), outlineKeys{})()

	_, err := poolCatalog(srv).Servers(context.Background(), catalogAddr(srv))
	if err == nil {
		t.Fatal("страница с vless-ключом дала пул")
	}
	if !errors.Is(err, ErrBadResponse) {
		t.Errorf("ожидался пустой каталог, получено: %v", err)
	}
}

// Ключ, который не осилит наш парсер, в БД не попадает: ссылка, которую не возьмёт
// генератор конфига, занимает место и проходит как исправная до первой пробы.
func TestPoolCatalogDropsUnparsedKeys(t *testing.T) {
	srv, _ := catalogServer(t)
	c := poolCatalog(srv)
	c.CheckKey = func(raw string) error {
		if strings.Contains(raw, ":39") {
			return fmt.Errorf("наш парсер такое не берёт")
		}
		return nil
	}

	servers, err := c.Servers(context.Background(), catalogAddr(srv))
	if err != nil {
		t.Fatalf("Servers: %v", err)
	}
	for _, s := range servers {
		if strings.Contains(s.URL, ":39") {
			t.Fatalf("неразобравшийся ключ попал в состав: %q", s.Title)
		}
	}
	if len(servers) != 5 {
		t.Fatalf("в составе %d серверов, отбраковано должно быть двадцать ключей первой страницы из двадцати пяти",
			len(servers))
	}
}

// Потолок собранного соблюдается, и лишних запросов за ним не делается.
func TestPoolCatalogRespectsLimit(t *testing.T) {
	srv, requests := catalogServer(t)
	c := poolCatalog(srv)
	c.Limit = 7

	servers, err := c.Servers(context.Background(), catalogAddr(srv))
	if err != nil {
		t.Fatalf("Servers: %v", err)
	}
	if len(servers) != 7 {
		t.Fatalf("собрано %d ключей при потолке 7", len(servers))
	}
	// Одна страница списка и семь страниц ключей: за восьмым уже не ходили.
	if *requests != 8 {
		t.Errorf("сделано %d запросов, ожидалось 8", *requests)
	}
}

// Драйвер выбирается по хосту: добавление источника — запись в карте, а не правка
// существующего разборщика.
func TestPoolDriverByHost(t *testing.T) {
	defer RegisterPoolDriver("keys.example", fakePoolDriver{})()

	d, u, err := PoolDriverFor("https://keys.example/anything")
	if err != nil {
		t.Fatalf("PoolDriverFor: %v", err)
	}
	if d.Name() != "fake" || u.Host != "keys.example" {
		t.Errorf("выбран драйвер %q для %s", d.Name(), u)
	}
	if got, err := PoolKeyType("https://keys.example/anything"); err != nil || got != store.TunnelSOCKS {
		t.Errorf("протокол пула %q (%v), а его сообщает драйвер", got, err)
	}
	if got, err := PoolKeyType(DefaultPoolCatalogURL); err != nil || got != store.TunnelShadowsocks {
		t.Errorf("у каталога по умолчанию протокол %q (%v)", got, err)
	}
}

// Хост без драйвера — внятная ошибка, а не пустой пул. Умерший источник узнаётся
// отдельно: у всех установок до ADR 0015 в БД лежит именно он.
func TestPoolDriverUnknownHost(t *testing.T) {
	for _, addr := range []string{
		"https://example.org/protocol/vless",
		"https://vpnkeys.me/protocol/vless",
	} {
		if _, _, err := PoolDriverFor(addr); !errors.Is(err, ErrNoPoolDriver) {
			t.Errorf("%s: ожидалась ErrNoPoolDriver, получено: %v", addr, err)
		}
	}
	if !PoolCatalogRetired("https://vpnkeys.me/protocol/vless") {
		t.Error("умерший источник не опознан")
	}
	if PoolCatalogRetired(DefaultPoolCatalogURL) {
		t.Error("живой каталог сочтён умершим")
	}
}

// fakePoolDriver — драйвер-пустышка: доказывает, что источник добавляется, не трогая
// разборщик outlinekeys.
type fakePoolDriver struct{}

func (fakePoolDriver) Name() string              { return "fake" }
func (fakePoolDriver) KeyType() store.TunnelType { return store.TunnelSOCKS }
func (fakePoolDriver) Servers(context.Context, *poolCrawl, *url.URL) ([]store.PoolServer, error) {
	return []store.PoolServer{{URL: "socks://198.51.100.1:1080"}}, nil
}

// blockingDriver — драйвер, застревающий посреди обхода, пока тест его не отпустит.
// Совпадение двух обходов иначе не воспроизводится: настоящий обход слишком быстр.
type blockingDriver struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func newBlockingDriver() *blockingDriver {
	return &blockingDriver{started: make(chan struct{}, 1), release: make(chan struct{})}
}

func (d *blockingDriver) Name() string              { return "blocking" }
func (d *blockingDriver) KeyType() store.TunnelType { return store.TunnelSOCKS }

func (d *blockingDriver) Servers(context.Context, *poolCrawl, *url.URL) ([]store.PoolServer, error) {
	d.calls.Add(1)
	d.started <- struct{}{}
	<-d.release
	return []store.PoolServer{{URL: "socks://198.51.100.1:1080", Title: "заглушка"}}, nil
}

// logged — каталог, чей лог можно прочитать: подавленный обход обязан быть виден.
func logged(buf *bytes.Buffer) *PoolCatalog {
	return &PoolCatalog{Log: slog.New(slog.NewTextHandler(buf, nil)), Pause: -1}
}

// Второй обход того же каталога не запускается: ручная кнопка и такт расписания,
// совпавшие по времени, стоили сотни лишних запросов к чужому сайту (issue #156).
// Отказ приходит сразу и виден в логе — молча проглатывать его нельзя.
func TestPoolCatalogSuppressesConcurrentCrawl(t *testing.T) {
	d := newBlockingDriver()
	defer RegisterPoolDriver("keys.example", d)()

	var logs bytes.Buffer
	c := logged(&logs)
	const catalog = "https://keys.example/catalog"

	done := make(chan error, 1)
	go func() {
		_, err := c.Servers(context.Background(), catalog)
		done <- err
	}()
	<-d.started

	if _, err := c.Servers(context.Background(), catalog); !errors.Is(err, ErrPoolCrawlBusy) {
		t.Fatalf("второй обход получил %v, ожидалась ErrPoolCrawlBusy", err)
	}
	close(d.release)
	if err := <-done; err != nil {
		t.Fatalf("первый обход: %v", err)
	}
	if got := d.calls.Load(); got != 1 {
		t.Errorf("каталог обойден %d раза, ожидался один", got)
	}
	if !strings.Contains(logs.String(), "каталог уже обходится") {
		t.Errorf("подавленный обход не виден в логе: %s", logs.String())
	}

	// Обход кончился — каталог свободен: замок не должен переживать свой обход.
	d.release = make(chan struct{})
	close(d.release)
	if _, err := c.Servers(context.Background(), catalog); err != nil {
		t.Fatalf("обход после освобождения каталога: %v", err)
	}
}

// Подавляется совпадение обходов одного каталога, а не обходов вообще: два пула с
// разными источниками бьют по разным сайтам и мешать друг другу не должны.
func TestPoolCatalogCrawlsDifferentCatalogsAtOnce(t *testing.T) {
	first, second := newBlockingDriver(), newBlockingDriver()
	defer RegisterPoolDriver("one.example", first)()
	defer RegisterPoolDriver("two.example", second)()

	c := &PoolCatalog{Log: quietSlog(), Pause: -1}
	done := make(chan error, 2)
	for _, addr := range []string{"https://one.example/c", "https://two.example/c"} {
		go func() {
			_, err := c.Servers(context.Background(), addr)
			done <- err
		}()
	}

	// Оба обхода дошли до драйвера — значит, второй не ждал первого.
	<-first.started
	<-second.started
	close(first.release)
	close(second.release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("обход каталога: %v", err)
		}
	}
}

// Расписание, наткнувшееся на идущий обход, не считает это неудачей и в ретраи не
// уходит; кнопка «Обновить» на том же каталоге получает внятный отказ.
func TestPoolManagerSkipsBusyCatalog(t *testing.T) {
	d := newBlockingDriver()
	defer RegisterPoolDriver("keys.example", d)()

	var logs bytes.Buffer
	c := logged(&logs)
	m := NewPoolManager(PoolManagerOptions{Catalog: c, Writer: &poolWriter{}, Logger: c.Log})
	tun := PoolTunnel{ID: "pppp", Name: "пул", CatalogURL: "https://keys.example/catalog", Enabled: true}
	m.SetTunnels([]PoolTunnel{tun})

	go func() { _, _ = c.Servers(context.Background(), tun.CatalogURL) }()
	<-d.started

	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("такт расписания на занятом каталоге: %v", err)
	}
	if _, err := m.RefreshPool(context.Background(), tun); !errors.Is(err, ErrPoolCrawlBusy) {
		t.Fatalf("обход по требованию получил %v, ожидалась ErrPoolCrawlBusy", err)
	}
	close(d.release)
	if got := d.calls.Load(); got != 1 {
		t.Errorf("каталог обойден %d раза, ожидался один", got)
	}
	if !strings.Contains(logs.String(), "такт расписания пропущен") {
		t.Errorf("пропущенный такт не виден в логе: %s", logs.String())
	}
}

// catalogStub — каталог на своей разметке: тот же драйвер, но страницы задаёт тест.
func catalogStub(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Cleanup(RegisterPoolDriver(srv.Listener.Addr().(*net.TCPAddr).IP.String(), outlineKeys{}))
	return srv
}

func TestPoolCatalogBadResponse(t *testing.T) {
	srv := catalogStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	c := poolCatalog(srv)
	_, err := c.Servers(context.Background(), catalogAddr(srv))
	if !errors.Is(err, ErrBadResponse) {
		t.Fatalf("ожидалась ErrBadResponse, получено: %v", err)
	}
}

// Страница без карточек — не пул из нуля серверов, а ошибка: молча обнулить состав
// значило бы выкинуть из конфига работающие серверы.
func TestPoolCatalogEmptyPage(t *testing.T) {
	srv := catalogStub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body></body></html>"))
	})

	c := poolCatalog(srv)
	if _, err := c.Servers(context.Background(), catalogAddr(srv)); err == nil {
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
		Catalog: poolCatalog(srv),
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

	catalog := catalogAddr(srv)
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
	catalog := catalogAddr(srv)

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
		CatalogURL: catalogAddr(srv), Enabled: false,
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
		{ID: "broken", Name: "битый", CatalogURL: broken.URL + outlineKeysSection, Enabled: true},
		{ID: "pppp", Name: "пул", CatalogURL: catalogAddr(srv), Enabled: true},
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

// Набор пулов сверяется по признакам пула, а не по составу серверов: состав меняется
// после каждого обхода каталога, и сверка по нему объявляла набор изменившимся всегда —
// каталог обходился каждые полминуты вместо двенадцати часов (issue #77).
func TestSamePoolSet(t *testing.T) {
	base := []PoolTunnel{{
		ID: "pppp", Name: "пул", CatalogURL: DefaultPoolCatalogURL, Enabled: true,
		Servers: []store.PoolServer{{URL: "vless://x@1.2.3.4:443", PingMS: 50}},
	}}
	// Копия набора, которой можно править одно поле, не задевая base.
	with := func(f func(*PoolTunnel)) []PoolTunnel {
		out := append([]PoolTunnel(nil), base...)
		f(&out[0])
		return out
	}

	cases := []struct {
		name string
		next []PoolTunnel
		same bool
	}{
		{"тот же набор", with(func(*PoolTunnel) {}), true},
		{"другой состав серверов", with(func(t *PoolTunnel) {
			t.Servers = []store.PoolServer{
				{URL: "vless://x@1.2.3.4:443", PingMS: 90, Misses: 2},
				{URL: "vless://y@5.6.7.8:443"},
			}
		}), true},
		{"состав опустел", with(func(t *PoolTunnel) { t.Servers = nil }), true},
		{"переименован туннель", with(func(t *PoolTunnel) { t.Name = "другое имя" }), true},
		{"сменился каталог", with(func(t *PoolTunnel) { t.CatalogURL += "?x=1" }), false},
		{"пул выключен", with(func(t *PoolTunnel) { t.Enabled = false }), false},
		{"другой пул", with(func(t *PoolTunnel) { t.ID = "qqqq" }), false},
		{"пул исчез", nil, false},
		{"пул появился", append(with(func(*PoolTunnel) {}),
			PoolTunnel{ID: "qqqq", Name: "второй", CatalogURL: "https://example.org", Enabled: true}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SamePoolSet(base, c.next); got != c.same {
				t.Errorf("SamePoolSet = %v, ожидалось %v", got, c.same)
			}
			// Сверка симметрична: исчезновение и появление — одно и то же различие.
			if got := SamePoolSet(c.next, base); got != c.same {
				t.Errorf("SamePoolSet в обратную сторону = %v, ожидалось %v", got, c.same)
			}
		})
	}
}

// Сторож набора полей: [PoolIdentity] перечисляет поля явно, и новое поле [PoolTunnel]
// в сверку само не попадёт — но и мимо внимания пройти не должно. Тест падает на любом
// новом поле и требует решить, делает ли оно набор пулов другим набором.
//
// Стережёт именно тот способ ошибиться, которым дефект и был сделан: `reflect.DeepEqual`
// молча учитывал `Servers`, потому что учитывал вообще всё (issue #77).
func TestPoolTunnelFieldsGuard(t *testing.T) {
	// Поля, про которые решение принято: входит в сверку или нет.
	known := map[string]bool{
		"ID":         true,
		"Name":       false, // переименование каталога не меняет
		"CatalogURL": true,
		"Enabled":    true,
		"Country":    false, // неизменен для пула, ID и так различает; отбор среза, не набор
		"Servers":    false, // меняется после каждого обхода, сверку не ведёт
	}

	typ := reflect.TypeOf(PoolTunnel{})
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if _, ok := known[name]; !ok {
			t.Errorf("в PoolTunnel появилось поле %s: решите, делает ли оно набор пулов "+
				"другим набором, впишите его в PoolIdentity.Identity при необходимости "+
				"и добавьте сюда", name)
		}
	}
	if typ.NumField() != len(known) {
		t.Errorf("полей в PoolTunnel %d, известных %d", typ.NumField(), len(known))
	}

	// Поля, входящие в сверку, обязаны быть в PoolIdentity — и наоборот.
	idType := reflect.TypeOf(PoolIdentity{})
	inIdentity := make(map[string]bool, idType.NumField())
	for i := range idType.NumField() {
		inIdentity[idType.Field(i).Name] = true
	}
	for name, want := range known {
		if inIdentity[name] != want {
			t.Errorf("поле %s: в PoolIdentity %v, ожидалось %v", name, inIdentity[name], want)
		}
	}
}

// Обход по требованию не зависит от набора расписания: пул приходит целиком от того,
// кто прочитал его из БД. Раньше менеджер искал пул у себя и на только что заведённом
// отвечал отказом до ближайшей сверки с БД, то есть до полуминуты (issue #74).
//
// Выключенный пул обходится и здесь: за него попросил человек, а в конфиг он всё равно
// не попадёт, пока выключен.
func TestRefreshPoolWithoutSetTunnels(t *testing.T) {
	srv, requests := catalogServer(t)
	w := &poolWriter{}
	m := poolManager(t, srv, w)

	changed, err := m.RefreshPool(context.Background(), PoolTunnel{
		ID: "свежий", Name: "пул", CatalogURL: catalogAddr(srv), Enabled: false,
	})
	if err != nil {
		t.Fatalf("RefreshPool: %v", err)
	}
	if !changed {
		t.Error("состав пустого пула не изменился после первого обхода")
	}
	if *requests == 0 {
		t.Error("в каталог не сходили вовсе")
	}
	if got := w.written(); len(got) != 1 || got[0] != "свежий" {
		t.Fatalf("состав записан для %v, ожидался свежий пул", got)
	}
	select {
	case <-m.Updates():
	default:
		t.Error("об изменении состава никто не узнал")
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
func TestOutlineKeysPageURL(t *testing.T) {
	u, err := url.Parse("https://outlinekeys.com/protocols/outline/?sort=new")
	if err != nil {
		t.Fatalf("разбор адреса: %v", err)
	}
	got := outlineKeysPageURL(u, 2)
	for _, want := range []string{"page=2", "sort=new", "/protocols/outline/"} {
		if !strings.Contains(got, want) {
			t.Errorf("в адресе %q нет %q", got, want)
		}
	}
}
