package main

import (
	"context"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
)

// quietSlog глушит логи демона: тесты проверяют поведение, а не вывод.
func quietSlog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// openStore — временная БД. Ни сети, ни root тестам не нужно.
func openStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st, path
}

// TestNftSubnetsMergesLists — в сет уходят и ручные подсети правил, и подсети
// из загруженных списков: без вторых FakeIP не спасает, клиент идёт на
// настоящий адрес и мимо метки.
func TestNftSubnetsMergesLists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("149.154.160.0/20\nexample.com\n"))
	}))
	defer srv.Close()

	fetcher, err := lists.NewFetcher(lists.Options{Dir: t.TempDir(), Logger: quietSlog()})
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	m := lists.NewManager(lists.ManagerOptions{Fetcher: fetcher, Logger: quietSlog()})
	m.SetSources([]lists.Source{{URL: srv.URL + "/telegram.lst", Format: lists.FormatPlain}})
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got := nftSubnets([]string{"192.0.2.0/24"}, m)
	for _, want := range []string{"192.0.2.0/24", "149.154.160.0/20"} {
		if !slices.Contains(got, want) {
			t.Errorf("подсеть %s не попала в сет: %v", want, got)
		}
	}
	if slices.Contains(got, "example.com") {
		t.Errorf("домен попал в сет подсетей: %v", got)
	}

	// Планировщика может не быть вовсе — тогда остаются ручные подсети.
	if got := nftSubnets([]string{"192.0.2.0/24"}, nil); !slices.Equal(got, []string{"192.0.2.0/24"}) {
		t.Errorf("без планировщика: получено %v", got)
	}
}

// TestSchedulerSurvivesFetchFailure — неудачная загрузка не прекращает работу
// планировщика: списки лежат на чужих серверах, и первый прогон на свежем VPS
// стабильно проваливается. Демон обязан дожить до момента, когда источник
// ответит, и тогда подсети должны появиться в сете.
func TestSchedulerSurvivesFetchFailure(t *testing.T) {
	fail := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-fail:
			_, _ = w.Write([]byte("149.154.160.0/20\n"))
		default:
			http.Error(w, "boom", http.StatusServiceUnavailable)
		}
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	defer srv.Close()

	fetcher, err := lists.NewFetcher(lists.Options{Dir: t.TempDir(), Logger: quietSlog()})
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	m := lists.NewManager(lists.ManagerOptions{
		Fetcher:  fetcher,
		Logger:   quietSlog(),
		Interval: time.Hour,
		Retry:    10 * time.Millisecond,
	})
	m.SetSources([]lists.Source{{URL: srv.URL + "/a.lst", Format: lists.FormatPlain}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// Первое уведомление приходит после провального прогона: сет остаётся с
	// одними ручными подсетями, но планировщик продолжает работать.
	select {
	case <-m.Updates():
	case <-time.After(10 * time.Second):
		t.Fatal("уведомление после неудачного прогона не пришло")
	}
	if got := nftSubnets([]string{"192.0.2.0/24"}, m); !slices.Equal(got, []string{"192.0.2.0/24"}) {
		t.Errorf("после неудачи в сете %v, ожидались только ручные подсети", got)
	}

	close(fail)
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-m.Updates():
			if slices.Contains(nftSubnets(nil, m), "149.154.160.0/20") {
				cancel()
				m.Wait()
				return
			}
		case <-deadline:
			t.Fatal("планировщик не повторил загрузку после неудачи")
		}
	}
}

// TestStartListsNoSources — планировщик поднимается на пустом состоянии и
// никуда не ходит: качать нечего, пока нет правил со списками.
func TestStartListsNoSources(t *testing.T) {
	st, dbPath := openStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := startLists(ctx, st, dbPath, quietSlog())
	if m == nil {
		t.Fatal("планировщик не поднялся")
	}
	if got := nftSubnets(nil, m); len(got) != 0 {
		t.Errorf("подсети без источников: %v", got)
	}
	if _, err := os.Stat(listsCacheDir(dbPath)); err != nil {
		t.Errorf("каталог кэша не создан: %v", err)
	}

	cancel()
	m.Wait()
}

// TestStartListsBrokenCache — недоступный кэш демон не роняет: планировщика
// нет, шлюз работает на ручных подсетях.
func TestStartListsBrokenCache(t *testing.T) {
	st, _ := openStore(t)

	// Файл на месте каталога кэша: MkdirAll на такой путь не проходит.
	notDir := filepath.Join(t.TempDir(), "занято")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatalf("подготовка файла: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if m := startLists(ctx, st, filepath.Join(notDir, "lists", "state.db"), quietSlog()); m != nil {
		t.Error("планировщик поднялся на недоступном кэше")
	}
	if got := nftSubnets([]string{"192.0.2.0/24"}, nil); !slices.Equal(got, []string{"192.0.2.0/24"}) {
		t.Errorf("без планировщика: получено %v", got)
	}
}

// TestPlainListsFeedsGenerator — домены plain-списка доезжают от планировщика до
// генератора конфига. Без этой проводки они разбирались и выбрасывались, а
// правило с одним таким списком выпадало из конфига (issue #125).
func TestPlainListsFeedsGenerator(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("example.com\n149.154.160.0/20\n"))
	}))
	defer srv.Close()

	fetcher, err := lists.NewFetcher(lists.Options{Dir: t.TempDir(), Logger: quietSlog()})
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	m := lists.NewManager(lists.ManagerOptions{Fetcher: fetcher, Logger: quietSlog()})
	url := srv.URL + "/blocked.txt"
	m.SetSources([]lists.Source{{URL: url, Format: lists.FormatPlain}})
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	src := plainLists(m)
	got, ok := src(url)
	if !ok {
		t.Fatal("скачанного списка нет у генератора")
	}
	if !slices.Contains(got.Domains, "example.com") {
		t.Errorf("доменов нет: %v", got.Domains)
	}
	if !slices.Contains(got.Subnets, "149.154.160.0/20") {
		t.Errorf("подсетей нет: %v", got.Subnets)
	}
	// Неизвестный адрес — не «пустой список», а «списка нет»: генератор обязан
	// различать это, чтобы сказать о пропуске вслух.
	if _, ok := src(srv.URL + "/другой.txt"); ok {
		t.Error("незнакомый список отдан как скачанный")
	}
	// Планировщика может не быть вовсе.
	if plainLists(nil) != nil {
		t.Error("без планировщика ожидалось пустое замыкание")
	}
}
