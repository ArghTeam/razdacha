package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
)

// syncTestInterval — интервал сверки в тестах. Прод берёт poolTunnelsInterval, тут
// нужны доли секунды, иначе один тест стоил бы полминуты.
const syncTestInterval = 5 * time.Millisecond

// logSink — журнал демона, доступный на чтение из теста. Сверка идёт по сообщению
// «набор пулов изменился»: именно оно, повторявшееся каждые 30 секунд, и было
// симптомом дефекта (issue #77).
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *logSink) count(sub string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Count(s.buf.String(), sub)
}

func (s *logSink) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(s, nil))
}

// setChanged — сколько раз сверка объявила набор пулов изменившимся.
func (s *logSink) setChanged() int { return s.count("набор пулов изменился") }

// poolCatalogServer отдаёт сохранённые страницы каталога из фикстуры слоя lists:
// в сеть тесты не ходят. Вторая функция — число сделанных запросов.
//
// Драйвер каталога выбирается по хосту, а httptest живёт на 127.0.0.1 со случайным
// портом, поэтому боевой драйвер регистрируется на адрес тестового сервера
// ([lists.RegisterPoolDriver]) — обход тогда идёт тем же кодом, что в проде.
func poolCatalogServer(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	var (
		mu       sync.Mutex
		requests int
	)
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join("..", "..", "internal", "lists", "testdata", name))
		if err != nil {
			t.Fatalf("чтение фикстуры %s: %v", name, err)
		}
		return string(body)
	}
	pages := []string{
		read("outlinekeys-outline-page1.html"),
		read("outlinekeys-outline-page2.html"),
		read("outlinekeys-outline-page3.html"),
	}
	key := read("outlinekeys-key-online.html")
	keyPath := regexp.MustCompile(`^/key/(\d+)/$`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")

		if m := keyPath.FindStringSubmatch(r.URL.Path); m != nil {
			// Порт в ссылке — номер ключа: одна фикстура на все страницы дала бы
			// один и тот же ключ, и пул схлопнулся бы в один сервер.
			_, _ = io.WriteString(w, strings.ReplaceAll(key, "212.193.6.27:234",
				"212.193.6.27:"+m[1]))
			return
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if n < 1 || n > len(pages) {
			n = len(pages)
		}
		_, _ = io.WriteString(w, pages[n-1])
	}))
	t.Cleanup(srv.Close)

	d, _, err := lists.PoolDriverFor(lists.DefaultPoolCatalogURL)
	if err != nil {
		t.Fatalf("драйвер каталога по умолчанию: %v", err)
	}
	t.Cleanup(lists.RegisterPoolDriver(srv.Listener.Addr().(*net.TCPAddr).IP.String(), d))
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return requests
	}
}

// poolCatalogAddr — адрес каталога на тестовом сервере: раздел тот единственный,
// который берёт драйвер.
func poolCatalogAddr(srv *httptest.Server) string {
	return srv.URL + "/protocols/outline/"
}

// createPool заводит туннель-пул в БД. Не встроенный: встроенный не удаляется, а
// исчезновение пула из набора проверять надо.
func createPool(t *testing.T, st *store.Store, name, catalogURL string, enabled bool) store.Tunnel {
	t.Helper()
	tn, err := st.CreateTunnel(context.Background(), store.Tunnel{
		Name:    name,
		Type:    store.TunnelVLESS,
		Source:  store.SourcePool,
		Raw:     catalogURL,
		Enabled: enabled,
	})
	if err != nil {
		t.Fatalf("создание пула %q: %v", name, err)
	}
	return tn
}

// waitFor ждёт выполнения условия, опрашивая его. Ждём именно так: сверка живёт в
// своей горутине и на тик отвечает не мгновенно.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(syncTestInterval)
	}
	t.Fatalf("не дождались: %s", what)
}

// startSync поднимает сверку набора пулов на текущем состоянии БД.
func startSync(t *testing.T, st *store.Store, m *lists.PoolManager, log *slog.Logger) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go syncPoolTunnels(ctx, st, m, poolTunnels(ctx, st, log), syncTestInterval, log)
}

// Появившийся пул обходится сразу, а дальнейший дрейф состава серверов внепланового
// обхода не вызывает.
//
// Дрейф здесь настоящий: обход пишет состав в ту же БД, из которой сверка читает набор.
// Сверка через reflect.DeepEqual на этом и ломалась — состав в наборе не совпадал с
// прошлым никогда, каталог обходился каждые полминуты вместо двенадцати часов, то есть
// около 2880 запросов к чужому сайту в сутки вместо двух (issue #77).
func TestSyncPoolTunnelsIgnoresServerChurn(t *testing.T) {
	st, _ := openStore(t)
	srv, requests := poolCatalogServer(t)
	sink := &logSink{}

	m := lists.NewPoolManager(lists.PoolManagerOptions{
		Catalog: &lists.PoolCatalog{Client: srv.Client(), Log: sink.logger(), Pause: -1},
		Writer:  st,
		Logger:  sink.logger(),
	})

	// Сверка стартует на пустом состоянии: пул появляется уже при ней.
	startSync(t, st, m, sink.logger())
	tn := createPool(t, st, "пул", poolCatalogAddr(srv), true)

	waitFor(t, "появившийся пул не обошёлся сразу", func() bool { return sink.setChanged() >= 1 })
	waitFor(t, "состав пула не записан в БД", func() bool {
		got, err := st.Tunnel(context.Background(), tn.ID)
		return err == nil && len(got.Pool) > 0
	})
	if sink.setChanged() != 1 {
		t.Fatalf("набор объявлен изменившимся %d раз при одном появлении пула", sink.setChanged())
	}
	after := requests()

	// Дальше в БД меняется только состав серверов — обходов быть не должно.
	stored, err := st.Tunnel(context.Background(), tn.ID)
	if err != nil {
		t.Fatalf("чтение пула: %v", err)
	}
	churn := append([]store.PoolServer(nil), stored.Pool...)
	churn[0].Misses++
	churn = append(churn, store.PoolServer{URL: "vless://новый@203.0.113.7:443", PingMS: 12})
	if err := st.UpdateTunnelPool(context.Background(), tn.ID, churn, time.Now().UTC()); err != nil {
		t.Fatalf("запись состава: %v", err)
	}

	// Тиков за это время — десятки: дефект проявился бы на первом же.
	time.Sleep(40 * syncTestInterval)
	if got := sink.setChanged(); got != 1 {
		t.Errorf("набор объявлен изменившимся %d раз, состав серверов сверку вести не должен", got)
	}
	if got := requests(); got != after {
		t.Errorf("каталог обойден ещё %d раз из-за дрейфа состава", got-after)
	}
}

// Всё, что делает набор пулов другим набором, вызывает обход сразу, не дожидаясь
// двенадцати часов. Пулы здесь выключенные: в каталог за ними не ходят, а сверка
// набора от этого не зависит — проверяется именно она.
func TestSyncPoolTunnelsRefreshesOnSetChange(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, st *store.Store, tn store.Tunnel)
	}{
		{"пул исчез", func(t *testing.T, st *store.Store, tn store.Tunnel) {
			if err := st.DeleteTunnel(context.Background(), tn.ID); err != nil {
				t.Fatalf("удаление пула: %v", err)
			}
		}},
		{"появился второй пул", func(t *testing.T, st *store.Store, _ store.Tunnel) {
			createPool(t, st, "второй пул", "https://example.org/protocol/vless", false)
		}},
		{"сменился каталог", func(t *testing.T, st *store.Store, tn store.Tunnel) {
			tn.Raw = "https://example.net/protocol/vless"
			if err := st.UpdateTunnel(context.Background(), tn); err != nil {
				t.Fatalf("смена каталога: %v", err)
			}
		}},
		{"пул включили", func(t *testing.T, st *store.Store, tn store.Tunnel) {
			tn.Enabled = true
			if err := st.UpdateTunnel(context.Background(), tn); err != nil {
				t.Fatalf("включение пула: %v", err)
			}
		}},
		{"пул переименовали", func(t *testing.T, st *store.Store, tn store.Tunnel) {
			tn.Name = "пул под другим именем"
			if err := st.UpdateTunnel(context.Background(), tn); err != nil {
				t.Fatalf("переименование пула: %v", err)
			}
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, _ := openStore(t)
			sink := &logSink{}
			// Каталог недостижим намеренно: выключенный пул в него не ходит, а если
			// пойдёт — тест это заметит по адресу example.org, а не по молчанию.
			tn := createPool(t, st, "пул", "https://example.org/protocol/vless", false)

			m := lists.NewPoolManager(lists.PoolManagerOptions{Writer: st, Logger: sink.logger()})
			startSync(t, st, m, sink.logger())

			// Сверка успевает несколько раз увидеть неизменившийся набор.
			time.Sleep(10 * syncTestInterval)
			if got := sink.setChanged(); got != 0 {
				t.Fatalf("набор объявлен изменившимся %d раз без единой правки", got)
			}

			c.change(t, st, tn)
			// Переименование каталога не меняет: обход не нужен, но и вреда от него нет.
			// Проверяем ровно то, что заявлено — набор пулов ведут ID, каталог и
			// включённость, поэтому имя тут исключение с обратным ожиданием.
			if c.name == "пул переименовали" {
				time.Sleep(20 * syncTestInterval)
				if got := sink.setChanged(); got != 0 {
					t.Errorf("переименование пула вызвало обход каталога (%d раз)", got)
				}
				return
			}
			waitFor(t, "изменение набора не вызвало обход", func() bool { return sink.setChanged() >= 1 })
		})
	}
}

// Свежая установка: демон заводит один выключенный общий пул (ADR 0018) и сообщает
// об этом в лог.
func TestEnsureBuiltinPoolSeedsOne(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	sink := &logSink{}

	ensureBuiltinPool(ctx, st, sink.logger())

	list, err := st.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	pools := 0
	for _, tn := range list {
		if tn.Builtin && tn.Source == store.SourcePool {
			pools++
			if tn.Enabled {
				t.Error("пул заведён включённым")
			}
			if tn.Raw != igareckCatalogURL {
				t.Errorf("у пула каталог %q, ожидался igareck", tn.Raw)
			}
		}
	}
	if pools != 1 {
		t.Fatalf("встроенных пулов %d, ожидался один", pools)
	}
	if sink.count("заведён встроенный общий пул") == 0 {
		t.Error("заведение пула не видно в логе")
	}

	// Второй старт ничего не заводит и молчит.
	ensureBuiltinPool(ctx, st, sink.logger())
	if got := sink.count("заведён встроенный общий пул"); got != 1 {
		t.Errorf("заведение объявлено %d раз", got)
	}
}

// Установка с версии со странами: в БД лежат страновые встроенные пулы, на один из них
// ссылается правило. Старт демона сворачивает их в один, отвязывает и выключает правило
// и сообщает об этом в лог (ADR 0013 — отвязка первого звена делает правило видимо
// нерабочим, а не тихо утекающим).
func TestEnsureBuiltinPoolCollapsesCountryPools(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	sink := &logSink{}

	base := time.Now().Add(-time.Hour)
	// Выживет самый ранний; правило вешаем на более поздний, чтобы проверить отвязку.
	if _, err := st.CreateTunnel(ctx, store.Tunnel{
		Name: "🇳🇱 Нидерланды", Type: store.TunnelVLESS, Source: store.SourcePool,
		Raw: igareckCatalogURL, Country: "NL", Enabled: true, Builtin: true, CreatedAt: base,
	}); err != nil {
		t.Fatalf("заведение странового пула: %v", err)
	}
	extra, err := st.CreateTunnel(ctx, store.Tunnel{
		Name: "🇩🇪 Германия", Type: store.TunnelVLESS, Source: store.SourcePool,
		Raw: igareckCatalogURL, Country: "DE", Enabled: true, Builtin: true,
		CreatedAt: base.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("заведение странового пула: %v", err)
	}
	rule, err := st.CreateRule(ctx, store.Rule{
		Name:      "В страновой пул",
		Action:    store.ActionTunnel,
		TunnelID:  extra.ID,
		Enabled:   true,
		Domains:   []string{"example.org"},
		PeerScope: store.ScopeAll,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	ensureBuiltinPool(ctx, st, sink.logger())

	if _, err := st.Tunnel(ctx, extra.ID); err == nil {
		t.Error("лишний пул не свёрнут")
	}
	got, err := st.Rule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("Rule: %v", err)
	}
	if got.TunnelID != "" || got.Enabled {
		t.Errorf("правило не отвязано/не выключено: %+v", got)
	}
	if sink.count("страновой встроенный пул свёрнут в общий") == 0 {
		t.Error("сворачивание пула не видно в логе")
	}
	if sink.count("правило ссылалось на свёрнутый пул и выключено") == 0 {
		t.Error("отвязка правила не видна в логе")
	}
}
