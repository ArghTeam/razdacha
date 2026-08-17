package singbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"unicode"

	"github.com/sagernet/sing-box/option"

	"github.com/ArghTeam/razdacha/internal/store"
)

// TestParseCorpus прогоняет весь корпус ссылок из Podkop. Разбираться должно всё, кроме
// транспортов, которых нет в sing-box; они обязаны давать ошибку, а не панику.
func TestParseCorpus(t *testing.T) {
	for _, line := range readCorpus(t, "testdata/proxy-urls.txt") {
		t.Run(shortName(line), func(t *testing.T) {
			res, err := Parse(line)
			if transport := transportOf(line); transport == "kcp" || transport == "xhttp" {
				requireParseError(t, err)
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) вернул ошибку: %v", line, err)
			}
			if res.Source != store.SourceURL {
				t.Errorf("Source = %q, ожидалось %q", res.Source, store.SourceURL)
			}
			if res.Outbound == nil {
				t.Fatal("Outbound не заполнен")
			}
			if res.Endpoint != nil {
				t.Error("Endpoint заполнен для ссылки на прокси")
			}
			if !json.Valid(res.Parsed) {
				t.Fatalf("Parsed — не JSON: %s", res.Parsed)
			}
			if res.Type == "" {
				t.Error("тип туннеля не определён")
			}
			if strings.Contains(line, "#") && res.Name == "" {
				t.Error("имя из фрагмента ссылки не подхвачено")
			}
		})
	}
}

// TestParseProxyURLFields сверяет разбор представителей каждого протокола и транспорта
// с ожидаемым JSON. Тег в JSON не проставляется — его выдаёт генератор конфига.
func TestParseProxyURLFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		typ  store.TunnelType
		want string
	}{
		{
			name: "socks5 с логином",
			raw:  "socks5://username:password@127.0.0.1:1080",
			typ:  store.TunnelSOCKS,
			want: `{"type":"socks","server":"127.0.0.1","server_port":1080,"version":"5",
			        "username":"username","password":"password"}`,
		},
		{
			name: "socks4a без логина",
			raw:  "socks4a://127.0.0.1:1080",
			typ:  store.TunnelSOCKS,
			want: `{"type":"socks","server":"127.0.0.1","server_port":1080,"version":"4a"}`,
		},
		{
			name: "ss с userinfo в base64",
			raw: "ss://MjAyMi1ibGFrZTMtYWVzLTI1Ni1nY206ZG1DbHkvWmgxNVd3OStzK0dGWGlGVElrcHc3Yy9x" +
				"Q0lTYUJyYWk3V2hoWT0@127.0.0.1:25144?type=tcp#shadowsocks-no-client",
			typ: store.TunnelShadowsocks,
			want: `{"type":"shadowsocks","server":"127.0.0.1","server_port":25144,
			        "method":"2022-blake3-aes-256-gcm",
			        "password":"dmCly/Zh15Ww9+s+GFXiFTIkpw7c/qCISaBrai7WhhY="}`,
		},
		{
			name: "ss с userinfo в открытом виде",
			raw: "ss://2022-blake3-aes-256-gcm:dmCly/Zh15Ww9+s+GFXiFTIkpw7c/qCISaBrai7WhhY=" +
				"@127.0.0.1:27214?type=tcp#shadowsocks-plain-user",
			typ: store.TunnelShadowsocks,
			want: `{"type":"shadowsocks","server":"127.0.0.1","server_port":27214,
			        "method":"2022-blake3-aes-256-gcm",
			        "password":"dmCly/Zh15Ww9+s+GFXiFTIkpw7c/qCISaBrai7WhhY="}`,
		},
		{
			name: "vless tcp reality",
			raw: "vless://e95163dc-905e-480a-afe5-20b146288679@127.0.0.1:16399?type=tcp" +
				"&encryption=none&security=reality&pbk=tqhSkeDR6jsqC-BYCnZWBrdL33g705ba8tV5-ZboWTM" +
				"&fp=chrome&sni=google.com&sid=f6&spx=%2F#vless-tcp-reality",
			typ: store.TunnelVLESS,
			want: `{"type":"vless","server":"127.0.0.1","server_port":16399,
			        "uuid":"e95163dc-905e-480a-afe5-20b146288679",
			        "tls":{"enabled":true,"server_name":"google.com",
			               "utls":{"enabled":true,"fingerprint":"chrome"},
			               "reality":{"enabled":true,
			                          "public_key":"tqhSkeDR6jsqC-BYCnZWBrdL33g705ba8tV5-ZboWTM",
			                          "short_id":"f6"}}}`,
		},
		{
			name: "vless tcp reality с flow xtls-rprx-vision",
			raw: "vless://e95163dc-905e-480a-afe5-20b146288679@127.0.0.1:16399?type=tcp" +
				"&encryption=none&flow=xtls-rprx-vision&security=reality" +
				"&pbk=tqhSkeDR6jsqC-BYCnZWBrdL33g705ba8tV5-ZboWTM" +
				"&fp=chrome&sni=google.com&sid=f6&spx=%2F#vless-flow",
			typ: store.TunnelVLESS,
			want: `{"type":"vless","server":"127.0.0.1","server_port":16399,
			        "uuid":"e95163dc-905e-480a-afe5-20b146288679",
			        "flow":"xtls-rprx-vision",
			        "tls":{"enabled":true,"server_name":"google.com",
			               "utls":{"enabled":true,"fingerprint":"chrome"},
			               "reality":{"enabled":true,
			                          "public_key":"tqhSkeDR6jsqC-BYCnZWBrdL33g705ba8tV5-ZboWTM",
			                          "short_id":"f6"}}}`,
		},
		{
			name: "vless ws tls insecure",
			raw: "vless://599e8659-e2ef-47d9-bf72-2f9b4b673474@127.0.0.1:36567?type=ws" +
				"&encryption=none&path=%2Fwspath&host=google.com&security=tls&fp=chrome" +
				"&alpn=h2%2Chttp%2F1.1&allowInsecure=1&sni=google.com#vless-websocket-tls-insecure",
			typ: store.TunnelVLESS,
			want: `{"type":"vless","server":"127.0.0.1","server_port":36567,
			        "uuid":"599e8659-e2ef-47d9-bf72-2f9b4b673474",
			        "tls":{"enabled":true,"server_name":"google.com","insecure":true,
			               "alpn":["h2","http/1.1"],
			               "utls":{"enabled":true,"fingerprint":"chrome"}},
			        "transport":{"type":"ws","path":"/wspath","headers":{"Host":"google.com"}}}`,
		},
		{
			name: "vless grpc без tls",
			raw: "vless://974b39e3-f7bf-42b9-933c-16699c635e77@127.0.0.1:15633?type=grpc" +
				"&encryption=none&serviceName=TunService&authority=&security=none#vless-gRPC-none",
			typ: store.TunnelVLESS,
			want: `{"type":"vless","server":"127.0.0.1","server_port":15633,
			        "uuid":"974b39e3-f7bf-42b9-933c-16699c635e77",
			        "transport":{"type":"grpc","service_name":"TunService"}}`,
		},
		{
			name: "trojan httpupgrade tls",
			raw: "trojan://MhNxbcVB14@127.0.0.1:32700?type=httpupgrade&path=%2Fhttpupgradepath" +
				"&host=google.com&security=tls&fp=chrome&alpn=h2%2Chttp%2F1.1&sni=google.com" +
				"#trojan-httpupgrade-tls",
			typ: store.TunnelTrojan,
			want: `{"type":"trojan","server":"127.0.0.1","server_port":32700,
			        "password":"MhNxbcVB14",
			        "tls":{"enabled":true,"server_name":"google.com","alpn":["h2","http/1.1"],
			               "utls":{"enabled":true,"fingerprint":"chrome"}},
			        "transport":{"type":"httpupgrade","host":"google.com",
			                     "path":"/httpupgradepath"}}`,
		},
		{
			name: "hysteria2 со всеми параметрами",
			raw: "hysteria2://mypassword@example.com:8443/?sni=example.com&obfs=salamander" +
				"&obfs-password=obfspass&insecure=1#hysteria2-all-params",
			typ: store.TunnelHysteria2,
			want: `{"type":"hysteria2","server":"example.com","server_port":8443,
			        "obfs":{"type":"salamander","password":"obfspass"},"password":"mypassword",
			        "tls":{"enabled":true,"server_name":"example.com","insecure":true}}`,
		},
		{
			name: "hy2 без параметров — tls включается сам",
			raw:  "hy2://password@example.com:443/#hysteria2-password",
			typ:  store.TunnelHysteria2,
			want: `{"type":"hysteria2","server":"example.com","server_port":443,
			        "password":"password","tls":{"enabled":true}}`,
		},
		{
			name: "hysteria2 с диапазоном портов",
			raw:  "hysteria2://password@example.com:20000-25000,30000#hysteria2-hop",
			typ:  store.TunnelHysteria2,
			want: `{"type":"hysteria2","server":"example.com","server_port":0,
			        "server_ports":["20000:25000","30000:30000"],"password":"password",
			        "tls":{"enabled":true}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse вернул ошибку: %v", err)
			}
			if res.Type != tt.typ {
				t.Errorf("Type = %q, ожидалось %q", res.Type, tt.typ)
			}
			assertJSONEqual(t, res.Parsed, tt.want)
		})
	}
}

// TestParseWireGuardConf проверяет, что .conf превращается в userspace-endpoint:
// секция endpoints, никакого bind_interface и никакого системного интерфейса
// (docs/decisions/0002-userspace-wireguard-outbound.md).
func TestParseWireGuardConf(t *testing.T) {
	res, err := Parse(readFixture(t, "testdata/wireguard.conf"))
	if err != nil {
		t.Fatalf("Parse вернул ошибку: %v", err)
	}
	if res.Type != store.TunnelWireGuard {
		t.Errorf("Type = %q, ожидалось %q", res.Type, store.TunnelWireGuard)
	}
	if res.Source != store.SourceWGConf {
		t.Errorf("Source = %q, ожидалось %q", res.Source, store.SourceWGConf)
	}
	if res.Outbound != nil {
		t.Error("WireGuard разобран в outbound, а должен быть endpoint")
	}
	if res.Endpoint == nil {
		t.Fatal("Endpoint не заполнен")
	}

	assertJSONEqual(t, res.Parsed, `{
		"type":"wireguard",
		"address":["10.66.66.2/32","fd42:42:42::2/128"],
		"private_key":"uHl8sTKvHZmqSN0dhBDl0kNmRvGaxNCEIYyNSGDPmVo=",
		"mtu":1420,
		"peers":[{"address":"vpn.example.com","port":51820,
		          "public_key":"xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
		          "pre_shared_key":"wLLdMxHRQyhCyEUmpXK1sBBKQL3ykiUJdlaHFN0EJDA=",
		          "allowed_ips":["0.0.0.0/0","::/0"],
		          "persistent_keepalive_interval":25}]}`)

	for _, forbidden := range []string{"bind_interface", "\"system\"", "interface_name"} {
		if strings.Contains(string(res.Parsed), forbidden) {
			t.Errorf("в конфиге endpoint'а есть %s — нарушен ADR 0002", forbidden)
		}
	}
}

// TestParseWireGuardConfDefaults проверяет умолчания минимального конфига: голый адрес
// без маски и отсутствующий AllowedIPs.
func TestParseWireGuardConfDefaults(t *testing.T) {
	res, err := Parse(readFixture(t, "testdata/wireguard-minimal.conf"))
	if err != nil {
		t.Fatalf("Parse вернул ошибку: %v", err)
	}
	assertJSONEqual(t, res.Parsed, `{
		"type":"wireguard",
		"address":"10.66.66.2/32",
		"private_key":"uHl8sTKvHZmqSN0dhBDl0kNmRvGaxNCEIYyNSGDPmVo=",
		"peers":[{"address":"203.0.113.7","port":51820,
		          "public_key":"xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
		          "allowed_ips":["0.0.0.0/0","::/0"]}]}`)
}

// TestParseWireGuardConfReserved проверяет расширение под WARP: client ID из секции
// [Peer] доходит до option.WireGuardPeer.Reserved. Поле разбирается ради
// совместимости с конфигами, которые его несут: без него WARP тоже работает.
func TestParseWireGuardConfReserved(t *testing.T) {
	res, err := Parse(readFixture(t, "testdata/wireguard-warp.conf"))
	if err != nil {
		t.Fatalf("Parse вернул ошибку: %v", err)
	}
	if res.Endpoint == nil {
		t.Fatal("Endpoint не заполнен")
	}
	opts, ok := res.Endpoint.Options.(*option.WireGuardEndpointOptions)
	if !ok {
		t.Fatalf("Options имеют тип %T, ожидались опции WireGuard", res.Endpoint.Options)
	}
	if len(opts.Peers) != 1 {
		t.Fatalf("пиров %d, ожидался один", len(opts.Peers))
	}
	if got := opts.Peers[0].Reserved; !bytes.Equal(got, []uint8{1, 2, 3}) {
		t.Errorf("Reserved = %v, ожидалось [1 2 3]", got)
	}
	// В конфиг sing-box client ID уезжает тем же base64, каким читает его сам sing-box.
	if !strings.Contains(string(res.Parsed), `"reserved":"AQID"`) {
		t.Errorf("в JSON пира нет reserved: %s", res.Parsed)
	}
}

// TestParseWireGuardConfReservedForms — формы записи client ID, которые встречаются
// у генераторов конфигов WARP.
func TestParseWireGuardConfReservedForms(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []uint8
	}{
		{"три числа через запятую", "1, 2, 3", []uint8{1, 2, 3}},
		{"три числа без пробелов", "0,0,0", []uint8{0, 0, 0}},
		{"числа в квадратных скобках", "[10, 20, 255]", []uint8{10, 20, 255}},
		{"base64 с набивкой", "AQID", []uint8{1, 2, 3}},
		{"base64 URL-safe", "-_-_", []uint8{251, 255, 191}},
		{"base64 из одних цифр", "1234", []uint8{215, 109, 248}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Parse(wireguardConfWithReserved(tt.value))
			if err != nil {
				t.Fatalf("Parse вернул ошибку: %v", err)
			}
			opts := res.Endpoint.Options.(*option.WireGuardEndpointOptions)
			if got := opts.Peers[0].Reserved; !bytes.Equal(got, tt.want) {
				t.Errorf("Reserved = %v, ожидалось %v", got, tt.want)
			}
		})
	}
}

// TestParseWireGuardConfWithoutReserved — конфиг без Reserved разбирается как раньше,
// поле остаётся пустым: обычному WireGuard-серверу client ID не нужен.
func TestParseWireGuardConfWithoutReserved(t *testing.T) {
	res, err := Parse(readFixture(t, "testdata/wireguard.conf"))
	if err != nil {
		t.Fatalf("Parse вернул ошибку: %v", err)
	}
	opts := res.Endpoint.Options.(*option.WireGuardEndpointOptions)
	if got := opts.Peers[0].Reserved; got != nil {
		t.Errorf("Reserved = %v, ожидалось пустое поле", got)
	}
	if strings.Contains(string(res.Parsed), "reserved") {
		t.Errorf("в JSON пира появилось reserved: %s", res.Parsed)
	}
}

// wireguardConfWithReserved собирает минимальный .conf с заданным значением Reserved.
func wireguardConfWithReserved(value string) string {
	return "[Interface]\n" +
		"PrivateKey = uHl8sTKvHZmqSN0dhBDl0kNmRvGaxNCEIYyNSGDPmVo=\n" +
		"Address = 172.16.0.2/32\n\n" +
		"[Peer]\n" +
		"PublicKey = bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=\n" +
		"Endpoint = engage.cloudflareclient.com:2408\n" +
		"Reserved = " + value + "\n"
}

// TestParseJSON проверяет запасной путь: готовый фрагмент конфига вставляется как есть.
func TestParseJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		typ  store.TunnelType
	}{
		{
			name: "известный протокол опознаётся по type",
			raw:  `{ "type": "trojan", "server": "example.com", "server_port": 443 }`,
			typ:  store.TunnelTrojan,
		},
		{
			name: "протокол без парсера остаётся raw",
			raw:  `{"type":"tuic","server":"example.com","server_port":443}`,
			typ:  store.TunnelRaw,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse вернул ошибку: %v", err)
			}
			if res.Type != tt.typ {
				t.Errorf("Type = %q, ожидалось %q", res.Type, tt.typ)
			}
			if res.Source != store.SourceJSON {
				t.Errorf("Source = %q, ожидалось %q", res.Source, store.SourceJSON)
			}
			assertJSONEqual(t, res.Parsed, tt.raw)
		})
	}
}

// TestParseInvalid — битый ввод обязан давать ошибку на русском, а не панику.
func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"пустая строка", "   "},
		{"просто текст", "какая-то строка"},
		{"ссылка без схемы", "127.0.0.1:1080"},
		{"неизвестный протокол", "vmess://abc@127.0.0.1:443"},
		{"обрезанная ссылка vless", "vless://"},
		{"vless без UUID", "vless://@127.0.0.1:443?type=tcp"},
		{"vless без порта", "vless://uuid@127.0.0.1?type=tcp"},
		{"vless с нечисловым портом", "vless://uuid@127.0.0.1:порт"},
		{"vless с портом вне диапазона", "vless://uuid@127.0.0.1:70000"},
		{"vless с неизвестным security", "vless://uuid@127.0.0.1:443?security=xtls"},
		{"vless с транспортом mKCP", "vless://uuid@127.0.0.1:443?type=kcp&seed=abc"},
		{"vless с транспортом xhttp", "vless://uuid@127.0.0.1:443?type=xhttp"},
		{"vless с шифрованием", "vless://uuid@127.0.0.1:443?encryption=aes-128-gcm"},
		{"vless с неизвестным flow", "vless://uuid@127.0.0.1:443?flow=xtls-rprx-vision-udp443"},
		{"ss с битым base64", "ss://!!!не-base64!!!@127.0.0.1:8388"},
		{"ss без метода и пароля", "ss://127.0.0.1:8388"},
		{"ss с base64 без двоеточия", "ss://YWJjZGVm@127.0.0.1:8388"},
		{"trojan без пароля", "trojan://@127.0.0.1:443"},
		{"hysteria2 без пароля", "hysteria2://example.com:443"},
		{"hysteria2 с битым диапазоном портов", "hysteria2://pass@example.com:100-abc"},
		{"hysteria2 с обфускацией без пароля", "hysteria2://pass@example.com:443?obfs=salamander"},
		{"битые параметры запроса", "vless://uuid@127.0.0.1:443?%zz=1"},
		{"JSON без type", `{"server":"example.com"}`},
		{"обрезанный JSON", `{"type":"trojan"`},
		{"wg без PrivateKey", "[Interface]\nAddress = 10.0.0.2/32\n\n[Peer]\nPublicKey = k\n"},
		{"wg без Address", "[Interface]\nPrivateKey = k\n\n[Peer]\nPublicKey = k\n"},
		{"wg без секции Peer", "[Interface]\nPrivateKey = k\nAddress = 10.0.0.2/32\n"},
		{"wg без PublicKey у пира", "[Interface]\nPrivateKey = k\nAddress = 10.0.0.2/32\n\n[Peer]\nEndpoint = h:1\n"},
		{"wg без Endpoint у пира", "[Interface]\nPrivateKey = k\nAddress = 10.0.0.2/32\n\n[Peer]\nPublicKey = k\n"},
		{"wg с битым Address", "[Interface]\nPrivateKey = k\nAddress = 300.1.1.1/32\n\n[Peer]\nPublicKey = k\nEndpoint = h:1\n"},
		{"wg с битым портом Endpoint", "[Interface]\nPrivateKey = k\nAddress = 10.0.0.2/32\n\n[Peer]\nPublicKey = k\nEndpoint = h:порт\n"},
		{"wg со строкой без знака равенства", "[Interface]\nPrivateKey\n"},
		{"wg с незакрытой секцией", "[Interface]\nPrivateKey = k\n[Peer\n"},
		{"wg с неизвестной секцией", "[Interface]\nPrivateKey = k\nAddress = 10.0.0.2/32\n\n[Proxy]\n"},
		{"wg с Reserved из двух чисел", wireguardConfWithReserved("1, 2")},
		{"wg с Reserved из четырёх чисел", wireguardConfWithReserved("1, 2, 3, 4")},
		{"wg с Reserved вне диапазона байта", wireguardConfWithReserved("1, 2, 300")},
		{"wg с нечисловым Reserved в списке", wireguardConfWithReserved("1, 2, три")},
		{"wg с незакрытой скобкой в Reserved", wireguardConfWithReserved("[1, 2, 3")},
		{"wg с Reserved не в base64", wireguardConfWithReserved("не-base64!")},
		{"wg с base64 Reserved не в три байта", wireguardConfWithReserved("AQIDBA==")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Parse(tt.raw)
			requireParseError(t, err)
			if res.Parsed != nil {
				t.Errorf("при ошибке вернулся разобранный конфиг: %s", res.Parsed)
			}
		})
	}
}

// requireParseError проверяет, что ошибка есть, относится к разбору и написана
// по-русски: её текст показывается пользователю как есть.
func requireParseError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("ошибки нет, а должна быть")
	}
	if !errors.Is(err, ErrParse) {
		t.Errorf("ошибка не обёрнута в ErrParse: %v", err)
	}
	if !hasCyrillic(err.Error()) {
		t.Errorf("текст ошибки не на русском: %v", err)
	}
}

// hasCyrillic отвечает, есть ли в строке кириллица.
func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// assertJSONEqual сравнивает JSON по значению, а не побайтово: порядок ключей в
// sing-box значим для чтения, но не для сравнения.
func assertJSONEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("разобранный конфиг — не JSON: %s", got)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("ожидаемый JSON в тесте битый: %v", err)
	}
	gotNorm, _ := json.Marshal(gotValue)
	wantNorm, _ := json.Marshal(wantValue)
	if string(gotNorm) != string(wantNorm) {
		t.Errorf("конфиг разошёлся с ожидаемым\n получено: %s\nожидалось: %s", gotNorm, wantNorm)
	}
}

// readFixture читает файл фикстуры.
func readFixture(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение фикстуры: %v", err)
	}
	return string(b)
}

// readCorpus возвращает содержательные строки файла с корпусом ссылок.
func readCorpus(t *testing.T, path string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(readFixture(t, path), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		t.Fatalf("корпус %s пуст", path)
	}
	return out
}

// transportOf достаёт значение параметра type — им отличаются транспорты, которых в
// sing-box нет.
func transportOf(raw string) string {
	u, err := splitProxyURL(raw)
	if err != nil {
		return ""
	}
	return u.param("type")
}

// shortName делает из ссылки читаемое имя подтеста: фрагмент, если он есть.
func shortName(raw string) string {
	if i := strings.LastIndex(raw, "#"); i >= 0 && i+1 < len(raw) {
		return raw[i+1:]
	}
	if len(raw) > 40 {
		return raw[:40]
	}
	return raw
}
