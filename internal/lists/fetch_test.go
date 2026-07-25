package lists

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

// newFetcher — загрузчик с кэшем во временном каталоге и без похода в сеть:
// httptest.Server слушает loopback, реальных запросов наружу нет.
func newFetcher(t *testing.T, srv *httptest.Server) *Fetcher {
	t.Helper()
	f, err := NewFetcher(Options{
		Dir:     filepath.Join(t.TempDir(), "lists"),
		Client:  srv.Client(),
		Logger:  quietSlog(),
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("создание загрузчика: %v", err)
	}
	return f
}

func TestFetchStoresBody(t *testing.T) {
	const body = "1.1.1.0/24\nexample.com\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	f := newFetcher(t, srv)
	res, err := f.Fetch(context.Background(), srv.URL+"/list.lst")
	if err != nil {
		t.Fatalf("загрузка: %v", err)
	}
	if res.Status != StatusUpdated {
		t.Errorf("статус: получено %q, ожидалось %q", res.Status, StatusUpdated)
	}
	if res.Format != FormatPlain {
		t.Errorf("вид списка: получено %q", res.Format)
	}
	raw, err := f.Cached(srv.URL + "/list.lst")
	if err != nil {
		t.Fatalf("чтение кэша: %v", err)
	}
	if string(raw) != body {
		t.Errorf("тело в кэше: получено %q, ожидалось %q", raw, body)
	}
	// Временных файлов после успешной записи не остаётся.
	entries, err := os.ReadDir(filepath.Dir(res.Path))
	if err != nil {
		t.Fatalf("чтение каталога кэша: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Errorf("в кэше остался временный файл %s", e.Name())
		}
	}
}

func TestFetchConditional(t *testing.T) {
	const body = "1.1.1.0/24\n"
	var requests []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Header.Clone())
		if r.Header.Get("If-None-Match") == `"v1"` || r.Header.Get("If-Modified-Since") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2026 07:28:00 GMT")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	f := newFetcher(t, srv)
	url := srv.URL + "/list.lst"

	if _, err := f.Fetch(context.Background(), url); err != nil {
		t.Fatalf("первая загрузка: %v", err)
	}
	res, err := f.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("вторая загрузка: %v", err)
	}
	if res.Status != StatusNotModified {
		t.Errorf("статус: получено %q, ожидалось %q", res.Status, StatusNotModified)
	}
	if len(requests) != 2 {
		t.Fatalf("запросов к источнику: %d, ожидалось 2", len(requests))
	}
	if got := requests[0].Get("If-None-Match"); got != "" {
		t.Errorf("первый запрос ушёл условным: If-None-Match = %q", got)
	}
	if got := requests[1].Get("If-None-Match"); got != `"v1"` {
		t.Errorf("второй запрос: If-None-Match = %q, ожидалось \"v1\"", got)
	}
	if got := requests[1].Get("If-Modified-Since"); got == "" {
		t.Error("второй запрос без If-Modified-Since")
	}
	// 304 кэш не трогает.
	raw, err := f.Cached(url)
	if err != nil {
		t.Fatalf("чтение кэша: %v", err)
	}
	if string(raw) != body {
		t.Errorf("тело в кэше: получено %q, ожидалось %q", raw, body)
	}
}

func TestFetchKeepsCacheOnFailure(t *testing.T) {
	const good = "1.1.1.0/24\n8.8.8.0/24\n"

	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "ответ 500",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			},
		},
		{
			name: "ответ 404",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "no", http.StatusNotFound)
			},
		},
		{
			name: "пустое тело",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
		},
		{
			name: "оборванное тело",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				// Обещаем больше, чем отдаём, и рвём соединение.
				w.Header().Set("Content-Length", "4096")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("1.1.1.0/24\n"))
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
				panic(http.ErrAbortHandler)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var broken bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if broken {
					tt.handler(w, r)
					return
				}
				_, _ = w.Write([]byte(good))
			}))
			srv.Config.ErrorLog = quietLogger()
			defer srv.Close()

			f := newFetcher(t, srv)
			url := srv.URL + "/list.lst"
			if _, err := f.Fetch(context.Background(), url); err != nil {
				t.Fatalf("первая загрузка: %v", err)
			}

			broken = true
			if _, err := f.Fetch(context.Background(), url); err == nil {
				t.Fatal("ожидалась ошибка загрузки")
			}

			raw, err := f.Cached(url)
			if err != nil {
				t.Fatalf("чтение кэша: %v", err)
			}
			if string(raw) != good {
				t.Errorf("кэш затёрт: получено %q, ожидалось %q", raw, good)
			}
		})
	}
}

func TestFetchTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 128))
	}))
	defer srv.Close()

	f, err := NewFetcher(Options{
		Dir:      filepath.Join(t.TempDir(), "lists"),
		Client:   srv.Client(),
		Logger:   quietSlog(),
		MaxBytes: 16,
	})
	if err != nil {
		t.Fatalf("создание загрузчика: %v", err)
	}
	if _, err := f.Fetch(context.Background(), srv.URL+"/list.lst"); !errors.Is(err, ErrBadResponse) {
		t.Fatalf("ошибка: получено %v, ожидалась ErrBadResponse", err)
	}
	if _, err := f.Cached(srv.URL + "/list.lst"); !errors.Is(err, ErrNotCached) {
		t.Fatalf("в кэш попал слишком большой список: %v", err)
	}
}

func TestParseFallsBackToCache(t *testing.T) {
	const good = "1.1.1.0/24\nexample.com\n"
	var broken bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if broken {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(good))
	}))
	defer srv.Close()

	f := newFetcher(t, srv)
	url := srv.URL + "/list.lst"
	if _, err := f.Parse(context.Background(), url); err != nil {
		t.Fatalf("первый разбор: %v", err)
	}

	broken = true
	list, err := f.Parse(context.Background(), url)
	if err != nil {
		t.Fatalf("разбор при недоступном источнике должен брать кэш: %v", err)
	}
	if len(list.Subnets) != 1 || list.Subnets[0] != "1.1.1.0/24" {
		t.Errorf("подсети из кэша: %v", list.Subnets)
	}
}

func TestParseWithoutCacheFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	f := newFetcher(t, srv)
	if _, err := f.Parse(context.Background(), srv.URL+"/list.lst"); !errors.Is(err, ErrBadResponse) {
		t.Fatalf("ошибка: получено %v, ожидалась ErrBadResponse", err)
	}
}

func TestFetchIdempotent(t *testing.T) {
	const body = "1.1.1.0/24\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Источник без валидаторов: каждый прогон перекачивает список заново,
		// и содержимое кэша от этого меняться не должно.
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	f := newFetcher(t, srv)
	url := srv.URL + "/list.lst"

	var first os.FileInfo
	for i := range 3 {
		res, err := f.Fetch(context.Background(), url)
		if err != nil {
			t.Fatalf("прогон %d: %v", i, err)
		}
		if res.Status != StatusUpdated {
			t.Errorf("прогон %d: статус %q", i, res.Status)
		}
		st, err := os.Stat(res.Path)
		if err != nil {
			t.Fatalf("прогон %d: тело в кэше: %v", i, err)
		}
		if first == nil {
			first = st
		} else if st.Size() != first.Size() {
			t.Errorf("прогон %d: размер тела изменился: %d вместо %d", i, st.Size(), first.Size())
		}
		raw, err := f.Cached(url)
		if err != nil {
			t.Fatalf("прогон %d: чтение кэша: %v", i, err)
		}
		if string(raw) != body {
			t.Errorf("прогон %d: тело в кэше %q", i, raw)
		}
	}

	// Кэш одного адреса — ровно два файла: тело и метаданные.
	entries, err := os.ReadDir(filepath.Dir(f.cache.bodyPath(url)))
	if err != nil {
		t.Fatalf("чтение каталога кэша: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("файлов в кэше: %d, ожидалось 2: %v", len(entries), entries)
	}
}

func TestFetchSRS(t *testing.T) {
	// Набор .srs собирается тем же кодеком, которым его читает sing-box:
	// фикстура в testdata устарела бы при смене версии формата.
	var buf bytes.Buffer
	set := option.PlainRuleSet{Rules: []option.HeadlessRule{{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultHeadlessRule{
			DomainSuffix: badoption.Listable[string]{"example.com"},
			IPCIDR:       badoption.Listable[string]{"1.1.1.0/24"},
		},
	}}}
	if err := srs.Write(&buf, set, C.RuleSetVersion2); err != nil {
		t.Fatalf("сборка набора .srs: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	f := newFetcher(t, srv)
	list, err := f.Parse(context.Background(), srv.URL+"/list.srs")
	if err != nil {
		t.Fatalf("загрузка .srs: %v", err)
	}
	if want := []string{"1.1.1.0/24"}; !reflect.DeepEqual(list.Subnets, want) {
		t.Errorf("подсети: получено %v, ожидалось %v", list.Subnets, want)
	}
	if want := []string{"example.com"}; !reflect.DeepEqual(list.Domains, want) {
		t.Errorf("домены: получено %v, ожидалось %v", list.Domains, want)
	}
}
