package clash

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient поднимает подставной Clash API и клиент к нему. Настоящего
// sing-box в тестах нет и быть не должно: проверяется разбор ответов.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Options{
		Addr:         srv.Listener.Addr().String(),
		ProbeTimeout: 100 * time.Millisecond,
		HTTPClient:   &http.Client{Timeout: 300 * time.Millisecond},
	})
}

// TestDelayOK — удачная проверка: задержка приходит в миллисекундах, а запрос
// несёт цель и таймаут, иначе sing-box проверял бы что-то своё и бесконечно.
func TestDelayOK(t *testing.T) {
	var gotPath, gotURL, gotTimeout string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotURL = r.URL.Query().Get("url")
		gotTimeout = r.URL.Query().Get("timeout")
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"delay":42}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})

	d, err := c.Delay(context.Background(), "tun-abc")
	if err != nil {
		t.Fatalf("Delay: %v", err)
	}
	if d != 42*time.Millisecond {
		t.Errorf("задержка = %s, ожидалось 42ms", d)
	}
	if gotPath != "/proxies/tun-abc/delay" {
		t.Errorf("путь = %q", gotPath)
	}
	if gotURL != DefaultTestURL {
		t.Errorf("цель проверки = %q", gotURL)
	}
	if gotTimeout != "100" {
		t.Errorf("таймаут проверки = %q, ожидалось 100", gotTimeout)
	}
}

// TestDelayNotFound — тега нет в конфиге sing-box. Это «не применён», а не сбой
// сети: панель должна отличать одно от другого.
func TestDelayNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		if _, err := w.Write([]byte(`{"message":"proxy not found"}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})

	_, err := c.Delay(context.Background(), "tun-missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrNotFound", err)
	}
}

// TestDelayProbeFailed — sing-box проверку выполнил, цель не открылась.
func TestDelayProbeFailed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := w.Write([]byte(`{"message":"An error occurred in the delay test"}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})

	_, err := c.Delay(context.Background(), "tun-dead")
	if !errors.Is(err, ErrProbeFailed) {
		t.Fatalf("ошибка = %v, ожидалась ErrProbeFailed", err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Error("неответивший туннель принят за неответивший sing-box")
	}
}

// TestDelayTimeout — Clash API принял соединение и завис. Ждать бесконечно
// нельзя: панель держит на этом запросе кнопку «Проверить».
func TestDelayTimeout(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	})

	start := time.Now()
	_, err := c.Delay(context.Background(), "tun-slow")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("ожидание заняло %s — дедлайн не сработал", elapsed)
	}
}

// TestDelayNoServer — sing-box не отвечает вовсе: порт закрыт.
func TestDelayNoServer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.Listener.Addr().String()
	srv.Close()

	c := New(Options{Addr: addr, ProbeTimeout: 100 * time.Millisecond})
	_, err := c.Delay(context.Background(), "tun-abc")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if errors.Is(err, ErrProbeFailed) || errors.Is(err, ErrNotFound) {
		t.Error("мёртвый sing-box принят за состояние туннеля")
	}
}

// TestDelayBadBody — ответ 200 с мусором вместо JSON.
func TestDelayBadBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`<html>ой</html>`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})

	if _, err := c.Delay(context.Background(), "tun-abc"); !errors.Is(err, ErrBadResponse) {
		t.Fatalf("ошибка = %v, ожидалась ErrBadResponse", err)
	}
}

// TestSecretHeader — токен Clash API уходит заголовком, если задан.
func TestSecretHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		if _, err := w.Write([]byte(`{"delay":1}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	c := New(Options{Addr: srv.URL, Secret: "s3cret", ProbeTimeout: 100 * time.Millisecond})
	if _, err := c.Delay(context.Background(), "tun-abc"); err != nil {
		t.Fatalf("Delay: %v", err)
	}
	if got != "Bearer s3cret" {
		t.Errorf("заголовок = %q", got)
	}
}

// TestProxiesAndState — список прокси и состояние одного тега.
func TestProxiesAndState(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Path {
		case "/proxies":
			body = `{"proxies":{"tun-abc":{"name":"tun-abc","type":"Socks","udp":true,
				"history":[{"time":"2026-07-25T12:00:00Z","delay":17}]}}}`
		case "/proxies/tun-abc":
			body = `{"name":"tun-abc","type":"Socks","udp":true,
				"history":[{"time":"2026-07-25T12:00:00Z","delay":0},
				           {"time":"2026-07-25T12:01:00Z","delay":17}]}`
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})

	all, err := c.Proxies(context.Background())
	if err != nil {
		t.Fatalf("Proxies: %v", err)
	}
	if _, ok := all["tun-abc"]; !ok {
		t.Fatalf("в списке нет tun-abc: %v", all)
	}

	p, err := c.Proxy(context.Background(), "tun-abc")
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if p.Type != "Socks" || !p.UDP {
		t.Errorf("состояние разобрано неверно: %+v", p)
	}
	d, ok := p.Latency()
	if !ok || d != 17*time.Millisecond {
		t.Errorf("последняя задержка = %s, %v", d, ok)
	}

	if _, err := c.Proxy(context.Background(), "tun-missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ошибка = %v, ожидалась ErrNotFound", err)
	}
}

// TestLatencyUnknown — пустой журнал и нулевая задержка означают «не измерялось»,
// а не «ноль миллисекунд».
func TestLatencyUnknown(t *testing.T) {
	for _, p := range []Proxy{
		{},
		{History: []History{{Delay: 0}}},
	} {
		if d, ok := p.Latency(); ok {
			t.Errorf("%+v дал задержку %s", p, d)
		}
	}
}

// TestVersionOK — рантайм отвечает своей версией.
//
// Формы ответа разные, а версия одна: живой sing-box отдаёт «sing-box 1.12.25»
// (проверено на стенде), ведущая «v» встречается тоже. Наружу уходит голая
// версия — её сравнивают с версией библиотеки из go.mod, записанной без имени
// продукта и без «v».
func TestVersionOK(t *testing.T) {
	for _, body := range []string{
		`{"meta":true,"premium":false,"version":"sing-box 1.12.25"}`,
		`{"meta":true,"premium":false,"version":"v1.12.25"}`,
		`{"meta":true,"premium":false,"version":"1.12.25"}`,
	} {
		var gotPath string
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(body)); err != nil {
				t.Errorf("запись ответа: %v", err)
			}
		})

		v, err := c.Version(context.Background())
		if err != nil {
			t.Fatalf("Version (%s): %v", body, err)
		}
		if v != "1.12.25" {
			t.Errorf("версия = %q, ожидалась 1.12.25 (ответ %s)", v, body)
		}
		if gotPath != "/version" {
			t.Errorf("путь = %q, ожидался /version", gotPath)
		}
	}
}

// TestVersionUnavailable — sing-box не запущен. Это состояние демона, и оно
// обязано отличаться от «версия такая-то»: панель показывает причину словами.
func TestVersionUnavailable(t *testing.T) {
	c := New(Options{
		Addr:       "127.0.0.1:1",
		HTTPClient: &http.Client{Timeout: 300 * time.Millisecond},
	})
	if _, err := c.Version(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestVersionEmpty — пустое поле в ответе это не пустая версия: рантайм ответил
// не тем, и выдавать пустую строку за версию нельзя.
func TestVersionEmpty(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"meta":true}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})
	if _, err := c.Version(context.Background()); !errors.Is(err, ErrBadResponse) {
		t.Errorf("ошибка = %v, ожидалась ErrBadResponse", err)
	}
}

// TestRules — правила рантайма приходят в порядке проверки, с действием как
// есть: их разбирает вызывающий, пакет про наши теги ничего не знает.
func TestRules(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rules" {
			t.Errorf("путь = %q, ожидался /rules", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"rules":[
			{"type":"default","payload":"inbound=dns-in","proxy":"hijack-dns"},
			{"type":"default","payload":"rule_set=[list-youtube]","proxy":"route(tun-abc)"}]}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})

	got, err := c.Rules(context.Background())
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("правил %d, ожидалось 2", len(got))
	}
	if got[1].Payload != "rule_set=[list-youtube]" || got[1].Proxy != "route(tun-abc)" {
		t.Errorf("второе правило = %+v", got[1])
	}
}

// TestResolveA — из ответа резолвера берутся только записи A: CNAME адресом не
// является, а FakeIP опознаётся по адресу.
func TestResolveA(t *testing.T) {
	var gotName, gotType string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotName = r.URL.Query().Get("name")
		gotType = r.URL.Query().Get("type")
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"Status":0,"Answer":[
			{"name":"youtube.com.","type":5,"TTL":60,"data":"cdn.youtube.com."},
			{"name":"cdn.youtube.com.","type":1,"TTL":60,"data":"198.18.0.9"}]}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})

	addrs, err := c.ResolveA(context.Background(), "youtube.com")
	if err != nil {
		t.Fatalf("ResolveA: %v", err)
	}
	if gotName != "youtube.com" || gotType != "A" {
		t.Errorf("запрос = name %q, type %q", gotName, gotType)
	}
	if len(addrs) != 1 || addrs[0].String() != "198.18.0.9" {
		t.Fatalf("адреса = %v, ожидался один 198.18.0.9", addrs)
	}
}

// TestResolveAFailed — рантайм ответил отказом: это ответ о домене, а не о
// самом sing-box, и путать их нельзя.
func TestResolveAFailed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte(`{"message":"upstream не ответил"}`)); err != nil {
			t.Errorf("запись ответа: %v", err)
		}
	})

	_, err := c.ResolveA(context.Background(), "nowhere.example")
	if !errors.Is(err, ErrResolveFailed) {
		t.Fatalf("ошибка = %v, ожидалась ErrResolveFailed", err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Error("отказ резолва принят за недоступный sing-box")
	}
}
