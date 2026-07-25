package lists

import (
	"context"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// quietLogger глушит лог httptest.Server: оборванные ответы в тестах ожидаемы,
// и их трассировки только зашумляют вывод.
func quietLogger() *log.Logger { return log.New(io.Discard, "", 0) }

// quietSlog глушит логи слоя: тесты проверяют поведение, а не вывод.
func quietSlog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSources(t *testing.T) {
	tests := []struct {
		name  string
		rules []store.Rule
		want  []Source
	}{
		{
			name: "community-список с подсетями",
			rules: []store.Rule{{
				Enabled:        true,
				CommunityLists: []string{"telegram", "youtube"},
			}},
			want: []Source{{
				URL:    "https://raw.githubusercontent.com/itdoginfo/allow-domains/main/Subnets/IPv4/telegram.lst",
				Format: FormatPlain,
			}},
		},
		{
			name: "свои списки в любом виде",
			rules: []store.Rule{{
				Enabled:     true,
				RemoteLists: []string{"https://ex.com/a.lst", "https://ex.com/b.srs", "https://ex.com/c.json"},
			}},
			want: []Source{
				{URL: "https://ex.com/a.lst", Format: FormatPlain},
				{URL: "https://ex.com/b.srs", Format: FormatSRS},
				{URL: "https://ex.com/c.json", Format: FormatJSON},
			},
		},
		{
			name: "выключенное правило не качается",
			rules: []store.Rule{{
				Enabled:     false,
				RemoteLists: []string{"https://ex.com/a.lst"},
			}},
		},
		{
			name: "правило block тоже даёт подсети",
			rules: []store.Rule{{
				Enabled:     true,
				Action:      store.ActionBlock,
				RemoteLists: []string{"https://ex.com/ads.lst"},
			}},
			want: []Source{{URL: "https://ex.com/ads.lst", Format: FormatPlain}},
		},
		{
			name: "один адрес в двух правилах качается один раз",
			rules: []store.Rule{
				{Enabled: true, RemoteLists: []string{"https://ex.com/a.lst"}},
				{Enabled: true, RemoteLists: []string{"https://ex.com/a.lst"}},
			},
			want: []Source{{URL: "https://ex.com/a.lst", Format: FormatPlain}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Sources(store.Snapshot{Rules: tt.rules})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("получено %v, ожидалось %v", got, tt.want)
			}
		})
	}
}

func TestManagerRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.lst":
			_, _ = w.Write([]byte("1.1.1.0/24\nexample.com\n"))
		case "/b.lst":
			_, _ = w.Write([]byte("8.8.8.0/24\n1.1.1.0/24\n"))
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	srv.Config.ErrorLog = quietLogger()
	defer srv.Close()

	m := NewManager(ManagerOptions{Fetcher: newFetcher(t, srv), Logger: quietSlog()})
	m.SetSources([]Source{
		{URL: srv.URL + "/a.lst", Format: FormatPlain},
		{URL: srv.URL + "/b.lst", Format: FormatPlain},
		{URL: srv.URL + "/missing.lst", Format: FormatPlain},
	})

	if err := m.Refresh(context.Background()); err == nil {
		t.Error("недоступный источник должен попасть в ошибку прогона")
	}

	want := []string{"1.1.1.0/24", "8.8.8.0/24"}
	if got := m.Subnets(); !reflect.DeepEqual(got, want) {
		t.Errorf("подсети: получено %v, ожидалось %v", got, want)
	}
	if l, ok := m.List(srv.URL + "/a.lst"); !ok || len(l.Domains) != 1 {
		t.Errorf("домены списка a.lst: %v", l.Domains)
	}
	if m.LastRefresh().IsZero() {
		t.Error("время прогона не проставлено")
	}

	// Повторный прогон идемпотентен: набор подсетей тот же.
	if err := m.Refresh(context.Background()); err == nil {
		t.Error("недоступный источник должен попасть в ошибку и на втором прогоне")
	}
	if got := m.Subnets(); !reflect.DeepEqual(got, want) {
		t.Errorf("подсети после второго прогона: получено %v, ожидалось %v", got, want)
	}
}

func TestManagerStartDoesNotBlock(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = w.Write([]byte("1.1.1.0/24\n"))
	}))
	defer srv.Close()

	m := NewManager(ManagerOptions{Fetcher: newFetcher(t, srv), Logger: quietSlog(), Interval: time.Hour})
	m.SetSources([]Source{{URL: srv.URL + "/a.lst", Format: FormatPlain}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := time.Now()
	m.Start(ctx)
	// Start возвращается, пока источник ещё держит соединение: старт демона
	// не ждёт ни сети, ни чужого сервера.
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Start заблокировал вызывающего на %s", elapsed)
	}
	if got := m.Subnets(); len(got) != 0 {
		t.Fatalf("подсети появились до окончания первого прогона: %v", got)
	}

	close(release)
	select {
	case <-m.Updates():
	case <-time.After(10 * time.Second):
		t.Fatal("уведомление об окончании прогона не пришло")
	}
	if got := m.Subnets(); !reflect.DeepEqual(got, []string{"1.1.1.0/24"}) {
		t.Errorf("подсети после прогона: %v", got)
	}

	cancel()
	m.Wait()
}

func TestManagerRetriesAfterFailure(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			// Первый прогон при неподнявшейся сети: демон обязан повторить
			// раньше суточного интервала.
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("1.1.1.0/24\n"))
	}))
	srv.Config.ErrorLog = quietLogger()
	defer srv.Close()

	m := NewManager(ManagerOptions{
		Fetcher:  newFetcher(t, srv),
		Logger:   quietSlog(),
		Interval: time.Hour,
		Retry:    10 * time.Millisecond,
	})
	m.SetSources([]Source{{URL: srv.URL + "/a.lst", Format: FormatPlain}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-m.Updates():
			if got := m.Subnets(); len(got) == 1 {
				cancel()
				m.Wait()
				return
			}
		case <-deadline:
			t.Fatal("повтор после неудачного прогона не случился")
		}
	}
}

func TestManagerDefaults(t *testing.T) {
	m := NewManager(ManagerOptions{})
	if got, want := m.currentInterval(), store.DefaultSettings().ListUpdateInterval; got != want {
		t.Errorf("интервал по умолчанию: получено %s, ожидалось %s", got, want)
	}
	m.SetInterval(time.Minute)
	if got := m.currentInterval(); got != time.Minute {
		t.Errorf("интервал после SetInterval: %s", got)
	}
	m.SetInterval(0)
	if got := m.currentInterval(); got != time.Minute {
		t.Errorf("нулевой интервал не должен применяться: %s", got)
	}
}
