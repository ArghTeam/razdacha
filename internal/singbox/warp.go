package singbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Ошибки регистрации WARP. Тексты доходят до панели и показываются как есть,
// поэтому сеть и отказ Cloudflare — разные сообщения: в первом случае чинят
// сервер, во втором ждут или пробуют позже, и путать их нельзя.
var (
	// ErrWARPUnreachable — до Cloudflare не дозвонились.
	ErrWARPUnreachable = errors.New("не удалось связаться с Cloudflare")
	// ErrWARPRejected — Cloudflare ответил, но отказом или непонятным телом.
	ErrWARPRejected = errors.New("Cloudflare отказал")
)

const (
	// warpAPIBase — версия API клиента WARP. Версия в пути значима: другую
	// сервис не обслуживает.
	warpAPIBase = "https://api.cloudflareclient.com/v0a2158"

	// warpUserAgent и warpClientVersion — заголовки клиента WARP. Без них
	// регистрация не проходит: сервис отвечает только своему приложению.
	warpUserAgent     = "okhttp/3.12.1"
	warpClientVersion = "a-6.10-2158"

	// warpEndpoint — общий вход WARP. Cloudflare отдаёт его же в ответе
	// регистрации; константа остаётся запасным вариантом.
	warpEndpoint = "engage.cloudflareclient.com:2408"

	// warpMTU — тот же 1280, что у клиентов и у wg0 (ADR 0004): туннель WARP
	// стоит за нашим же WireGuard, и запас на заголовки нужен обоим.
	warpMTU = 1280

	// warpTimeout — потолок на регистрацию. За кнопкой в панели стоит живой
	// человек, и висеть на чужом сервисе дольше его терпения незачем.
	warpTimeout = 20 * time.Second
)

// WARPDevice — зарегистрированное устройство WARP.
//
// Conf — обычный `.conf` WireGuard: он ложится в `raw` туннеля и разбирается тем
// же [Parse], что и вставленный руками. Второго пути сборки endpoint'а нет
// намеренно — иначе кнопка и вставка расходились бы в мелочах.
type WARPDevice struct {
	// Conf — конфиг WireGuard, готовый к разбору и хранению.
	Conf string
	// DeviceID — идентификатор устройства у Cloudflare. Нужен только логу:
	// туннель им не пользуется.
	DeviceID string
}

// WARPRegistrar регистрирует бесплатное устройство WARP.
//
// Внешних бинарников в цепочке нет: `wgcf` не тянем по той же причине, по которой
// не зовём `wg`, `ip` и `nft` — обычный HTTP-клиент из stdlib и своя пара ключей.
//
// Наружу регистратор ходит только когда его позвали: ни старт демона, ни миграция
// сюда не заглядывают — тот же контракт, что у туннеля-пула (ADR 0010).
type WARPRegistrar struct {
	base string
	hc   *http.Client
	now  func() time.Time
}

// WARPOptions — параметры регистратора. Base, HTTPClient и Now подменяются в
// тестах: настоящий api.cloudflareclient.com в них не участвует.
type WARPOptions struct {
	Base       string
	HTTPClient *http.Client
	Now        func() time.Time
}

// NewWARPRegistrar собирает регистратор.
func NewWARPRegistrar(o WARPOptions) *WARPRegistrar {
	base := o.Base
	if base == "" {
		base = warpAPIBase
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: warpTimeout}
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &WARPRegistrar{base: strings.TrimSuffix(base, "/"), hc: hc, now: now}
}

// warpRegRequest — тело регистрации. Пустые поля обязательны: сервис ждёт их
// присутствия, а не значений.
type warpRegRequest struct {
	Key          string `json:"key"`
	InstallID    string `json:"install_id"`
	FCMToken     string `json:"fcm_token"`
	Tos          string `json:"tos"`
	Model        string `json:"model"`
	SerialNumber string `json:"serial_number"`
	Locale       string `json:"locale"`
}

// warpRegResponse — то, что нужно из ответа регистрации. Остальные поля
// (аккаунт, токен доступа, дата) туннелю не нужны и не читаются.
type warpRegResponse struct {
	ID     string `json:"id"`
	Config struct {
		ClientID string `json:"client_id"`
		Peers    []struct {
			PublicKey string `json:"public_key"`
			Endpoint  struct {
				Host string `json:"host"`
				V4   string `json:"v4"`
			} `json:"endpoint"`
		} `json:"peers"`
		Interface struct {
			Addresses struct {
				V4 string `json:"v4"`
				V6 string `json:"v6"`
			} `json:"addresses"`
		} `json:"interface"`
	} `json:"config"`
}

// maxWARPBody — потолок на ответ регистрации. Он заведомо мелкий, а читать
// чужой поток без ограничения незачем.
const maxWARPBody = 64 << 10

// Register заводит у Cloudflare новое устройство и собирает его конфиг.
//
// Пара ключей генерируется здесь и никуда не отправляется целиком: наружу уходит
// только публичная половина, приватная остаётся в конфиге туннеля.
func (r *WARPRegistrar) Register(ctx context.Context) (WARPDevice, error) {
	priv, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return WARPDevice{}, fmt.Errorf("генерация ключа для WARP: %w", err)
	}

	body, err := json.Marshal(warpRegRequest{
		Key:    priv.PublicKey().String(),
		Tos:    r.now().UTC().Format("2006-01-02T15:04:05.000-07:00"),
		Model:  "PC",
		Locale: "en_US",
	})
	if err != nil {
		return WARPDevice{}, fmt.Errorf("сборка запроса регистрации WARP: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/reg", bytes.NewReader(body))
	if err != nil {
		return WARPDevice{}, fmt.Errorf("запрос регистрации WARP: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", warpUserAgent)
	req.Header.Set("CF-Client-Version", warpClientVersion)

	resp, err := r.hc.Do(req)
	if err != nil {
		return WARPDevice{}, fmt.Errorf("%w: проверьте сеть на сервере", ErrWARPUnreachable)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxWARPBody))
	if err != nil {
		return WARPDevice{}, fmt.Errorf("%w: ответ не дочитан", ErrWARPUnreachable)
	}
	if resp.StatusCode/100 != 2 {
		return WARPDevice{}, fmt.Errorf("%w: код %d", ErrWARPRejected, resp.StatusCode)
	}

	var out warpRegResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return WARPDevice{}, fmt.Errorf("%w: ответ не разобран", ErrWARPRejected)
	}
	conf, err := warpConf(priv.String(), out)
	if err != nil {
		return WARPDevice{}, err
	}
	return WARPDevice{Conf: conf, DeviceID: out.ID}, nil
}

// warpConf собирает `.conf` из ответа регистрации.
//
// Адрес интерфейса берётся v4 и, если он есть, v6: сам туннель это не делает
// доступным клиентам — им IPv6 закрыт отдельно, тремя слоями (ADR 0005).
func warpConf(privateKey string, reg warpRegResponse) (string, error) {
	if len(reg.Config.Peers) == 0 || reg.Config.Peers[0].PublicKey == "" {
		return "", fmt.Errorf("%w: в ответе нет ключа сервера", ErrWARPRejected)
	}
	v4 := strings.TrimSpace(reg.Config.Interface.Addresses.V4)
	if v4 == "" {
		return "", fmt.Errorf("%w: в ответе нет адреса устройства", ErrWARPRejected)
	}

	addresses := []string{v4 + "/32"}
	if v6 := strings.TrimSpace(reg.Config.Interface.Addresses.V6); v6 != "" {
		addresses = append(addresses, v6+"/128")
	}

	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", privateKey)
	// Адреса одной строкой: в INI это одно поле, и вторая строка `Address`
	// затёрла бы первую.
	fmt.Fprintf(&b, "Address = %s\n", strings.Join(addresses, ", "))
	fmt.Fprintf(&b, "MTU = %d\n", warpMTU)
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", reg.Config.Peers[0].PublicKey)
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	fmt.Fprintf(&b, "Endpoint = %s\n", warpPeerEndpoint(reg))
	// Client ID — три байта, которые ходят в заголовке пакета. WARP работает и
	// без них (проверено на стенде), поэтому пустой или непонятный client_id не
	// повод отказывать в регистрации: поле просто не пишется.
	if reserved := reg.Config.ClientID; warpClientIDValid(reserved) {
		fmt.Fprintf(&b, "Reserved = %s\n", reserved)
	}
	return b.String(), nil
}

// warpPeerEndpoint выбирает адрес входа: сначала то, что назвал сам Cloudflare,
// иначе общий домен. Голый IPv4 из ответа не годится — по хосту endpoint туннель
// узнаётся как WARP ([wireguardSource]), а по адресу узнать его нечем.
func warpPeerEndpoint(reg warpRegResponse) string {
	host := strings.TrimSpace(reg.Config.Peers[0].Endpoint.Host)
	if host == "" {
		return warpEndpoint
	}
	name, port, err := net.SplitHostPort(host)
	if err != nil || port == "" || !isWARPHost(name) {
		return warpEndpoint
	}
	return host
}

// warpClientIDValid проверяет, что client_id — те самые три байта в base64.
// Чужую длину в конфиг не пишем: разбор такого поля — ошибка, и туннель, который
// без него работал бы, перестал бы заводиться вовсе.
func warpClientIDValid(v string) bool {
	if v == "" {
		return false
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if raw, err := enc.DecodeString(v); err == nil {
			return len(raw) == reservedLen
		}
	}
	return false
}
