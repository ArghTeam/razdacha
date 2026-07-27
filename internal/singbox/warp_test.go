package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/option"

	"github.com/ArghTeam/razdacha/internal/store"
)

// warpRegBody — ответ регистрации в том виде, в каком его отдаёт Cloudflare.
// Настоящий api.cloudflareclient.com в тестах не участвует.
const warpRegBody = `{
  "id": "t.00000000-0000-4000-8000-000000000000",
  "token": "00000000-0000-4000-8000-000000000000",
  "config": {
    "client_id": "Zo9x",
    "peers": [{
      "public_key": "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
      "endpoint": {"v4": "162.159.192.1:0", "host": "engage.cloudflareclient.com:2408"}
    }],
    "interface": {"addresses": {"v4": "172.16.0.2", "v6": "2606:4700:110:8a1b::1"}}
  }
}`

// warpServer поднимает подставного Cloudflare и отдаёт регистратор к нему.
func warpServer(t *testing.T, h http.HandlerFunc) *WARPRegistrar {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewWARPRegistrar(WARPOptions{Base: srv.URL, HTTPClient: srv.Client()})
}

// Регистрация даёт готовый .conf: его разбирает тот же Parse, что и вставленный
// руками, и он получает source = warp.
func TestWARPRegister(t *testing.T) {
	var got struct {
		body    warpRegRequest
		path    string
		agent   string
		version string
	}
	reg := warpServer(t, func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.agent = r.Header.Get("User-Agent")
		got.version = r.Header.Get("CF-Client-Version")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)
		_, _ = io.WriteString(w, warpRegBody)
	})

	dev, err := reg.Register(context.Background())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got.path != "/reg" {
		t.Errorf("путь регистрации %q, ожидался /reg", got.path)
	}
	if got.agent != warpUserAgent || got.version != warpClientVersion {
		t.Errorf("заголовки клиента: %q, %q", got.agent, got.version)
	}
	// Наружу уходит только публичная половина ключа: приватная остаётся в конфиге.
	if got.body.Key == "" {
		t.Fatal("в запросе нет публичного ключа устройства")
	}
	if strings.Contains(dev.Conf, got.body.Key) {
		t.Error("публичный ключ устройства попал в конфиг вместо приватного")
	}
	if dev.DeviceID == "" {
		t.Error("идентификатор устройства потерялся")
	}

	res, err := Parse(dev.Conf)
	if err != nil {
		t.Fatalf("собранный конфиг не разбирается: %v", err)
	}
	if res.Source != store.SourceWARP || res.Type != store.TunnelWireGuard {
		t.Errorf("Source = %q, Type = %q — ожидались warp и wireguard", res.Source, res.Type)
	}
	opts, ok := res.Endpoint.Options.(*option.WireGuardEndpointOptions)
	if !ok {
		t.Fatalf("Options имеют тип %T, ожидались опции WireGuard", res.Endpoint.Options)
	}
	if opts.MTU != warpMTU {
		t.Errorf("MTU = %d, ожидался %d (ADR 0004)", opts.MTU, warpMTU)
	}
	if len(opts.Address) != 2 {
		t.Errorf("адресов интерфейса %d, ожидались v4 и v6: %v", len(opts.Address), opts.Address)
	}
	if opts.Peers[0].Address != "engage.cloudflareclient.com" || opts.Peers[0].Port != 2408 {
		t.Errorf("endpoint = %s:%d", opts.Peers[0].Address, opts.Peers[0].Port)
	}
	// client_id из ответа — те самые три байта: «Zo9x» это [102 143 113].
	if want := []uint8{102, 143, 113}; string(opts.Peers[0].Reserved) != string(want) {
		t.Errorf("Reserved = %v, ожидалось %v", opts.Peers[0].Reserved, want)
	}
}

// Cloudflare не назвал client_id или назвал непонятный — регистрация всё равно
// проходит: WARP работает и без него, и отказ здесь означал бы, что кнопка не
// заводит рабочий туннель на ровном месте.
func TestWARPRegisterWithoutClientID(t *testing.T) {
	body := strings.Replace(warpRegBody, `"client_id": "Zo9x"`, `"client_id": ""`, 1)
	reg := warpServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	})

	dev, err := reg.Register(context.Background())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if strings.Contains(dev.Conf, "Reserved") {
		t.Errorf("пустой client_id попал в конфиг:\n%s", dev.Conf)
	}
	if _, err := Parse(dev.Conf); err != nil {
		t.Fatalf("конфиг без Reserved не разбирается: %v", err)
	}
}

// Эндпоинт из ответа берётся, только если это имя в домене WARP: по нему туннель
// и узнаётся как WARP. Голый адрес заменяется общим входом.
func TestWARPRegisterEndpointFallback(t *testing.T) {
	body := strings.Replace(warpRegBody,
		`"host": "engage.cloudflareclient.com:2408"`, `"host": "162.159.192.1:2408"`, 1)
	reg := warpServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	})

	dev, err := reg.Register(context.Background())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !strings.Contains(dev.Conf, "Endpoint = "+warpEndpoint) {
		t.Errorf("endpoint не заменён общим входом:\n%s", dev.Conf)
	}
}

// Ошибка сети и отказ Cloudflare — разные события: в первом случае чинят сервер,
// во втором ждут. Один текст на оба увёл бы пользователя не туда.
func TestWARPRegisterErrors(t *testing.T) {
	t.Run("сеть", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		client := srv.Client()
		srv.Close() // сервера больше нет — соединение не установится
		reg := NewWARPRegistrar(WARPOptions{Base: srv.URL, HTTPClient: client})

		_, err := reg.Register(context.Background())
		if !errors.Is(err, ErrWARPUnreachable) {
			t.Fatalf("ожидалась ErrWARPUnreachable, получено: %v", err)
		}
	})

	t.Run("отказ", func(t *testing.T) {
		reg := warpServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"success":false}`)
		})
		_, err := reg.Register(context.Background())
		if !errors.Is(err, ErrWARPRejected) {
			t.Fatalf("ожидалась ErrWARPRejected, получено: %v", err)
		}
	})

	t.Run("ответ не разобран", func(t *testing.T) {
		reg := warpServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "<html>captcha</html>")
		})
		_, err := reg.Register(context.Background())
		if !errors.Is(err, ErrWARPRejected) {
			t.Fatalf("ожидалась ErrWARPRejected, получено: %v", err)
		}
	})

	t.Run("ответ без ключа сервера", func(t *testing.T) {
		reg := warpServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"t.1","config":{"peers":[]}}`)
		})
		_, err := reg.Register(context.Background())
		if !errors.Is(err, ErrWARPRejected) {
			t.Fatalf("ожидалась ErrWARPRejected, получено: %v", err)
		}
	})
}

// Вставленный руками конфиг WARP узнаётся сам — без кнопки регистрации. Это
// второй путь, которым проставляется source (ADR 0012).
func TestParseWireGuardConfWARPSource(t *testing.T) {
	res, err := Parse(readFixture(t, "testdata/wireguard-warp.conf"))
	if err != nil {
		t.Fatalf("Parse вернул ошибку: %v", err)
	}
	if res.Source != store.SourceWARP {
		t.Errorf("Source = %q, ожидалось %q", res.Source, store.SourceWARP)
	}
}

// Чужой WireGuard остаётся чужим: признак ставится по домену эндпоинта, а не по
// наличию Reserved и не по типу туннеля.
func TestIsWARPHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"engage.cloudflareclient.com", true},
		{"ENGAGE.CloudflareClient.com", true},
		{"cloudflareclient.com", true},
		{"engage.cloudflareclient.com.", true},
		{"vpn.example.com", false},
		{"cloudflareclient.com.evil.org", false},
		{"162.159.192.1", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isWARPHost(tt.host); got != tt.want {
			t.Errorf("isWARPHost(%q) = %v, ожидалось %v", tt.host, got, tt.want)
		}
	}
}
