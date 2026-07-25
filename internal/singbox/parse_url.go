package singbox

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/ArghTeam/razdacha/internal/store"
)

// proxyURL — ссылка, разобранная на части.
//
// Стандартный net/url здесь не годится: в userinfo shadowsocks встречается base64 с
// символом «/», на котором url.Parse обрывает authority и падает. Разбор повторяет
// помощники Podkop (`podkop/files/usr/lib/helpers.sh`, url_get_* — строки 129–203).
type proxyURL struct {
	scheme   string
	userinfo string
	host     string
	port     string
	query    url.Values
	fragment string
}

// schemeOf возвращает схему ссылки в нижнем регистре или пустую строку.
func schemeOf(s string) string {
	scheme, _, ok := strings.Cut(s, "://")
	if !ok || scheme == "" {
		return ""
	}
	for _, r := range scheme {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '+', r == '-', r == '.':
		default:
			return ""
		}
	}
	return strings.ToLower(scheme)
}

// splitProxyURL режет ссылку на части, не проверяя протокол.
func splitProxyURL(raw string) (proxyURL, error) {
	u := proxyURL{scheme: schemeOf(raw)}
	if u.scheme == "" {
		return proxyURL{}, fmt.Errorf("%w: в ссылке нет схемы вида «vless://»", ErrParse)
	}
	_, rest, _ := strings.Cut(raw, "://")

	if i := strings.Index(rest, "#"); i >= 0 {
		u.fragment = unescape(rest[i+1:])
		rest = rest[:i]
	}
	var rawQuery string
	if i := strings.Index(rest, "?"); i >= 0 {
		rawQuery = rest[i+1:]
		rest = rest[:i]
	}
	// userinfo отрезается раньше пути: в открытом виде пароль shadowsocks — base64,
	// и «/» внутри него нельзя принимать за начало пути. Собаку берём последнюю.
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		u.userinfo = unescape(rest[:i])
		rest = rest[i+1:]
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}

	var err error
	if u.query, err = url.ParseQuery(rawQuery); err != nil {
		return proxyURL{}, fmt.Errorf("%w: параметры ссылки не разбираются: %w", ErrParse, err)
	}
	if u.host, u.port, err = splitHostPort(rest); err != nil {
		return proxyURL{}, err
	}
	return u, nil
}

// splitHostPort делит «host:port», понимая IPv6 в квадратных скобках.
func splitHostPort(s string) (host, port string, err error) {
	if s == "" {
		return "", "", fmt.Errorf("%w: в ссылке не указан адрес сервера", ErrParse)
	}
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return "", "", fmt.Errorf("%w: незакрытая скобка в адресе %q", ErrParse, s)
		}
		host, rest := s[1:end], s[end+1:]
		if strings.HasPrefix(rest, ":") {
			port = rest[1:]
		}
		if host == "" {
			return "", "", fmt.Errorf("%w: в ссылке не указан адрес сервера", ErrParse)
		}
		return host, port, nil
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		host, port = s[:i], s[i+1:]
	} else {
		host = s
	}
	if host == "" {
		return "", "", fmt.Errorf("%w: в ссылке не указан адрес сервера", ErrParse)
	}
	return host, port, nil
}

// unescape снимает процентное кодирование, оставляя строку как есть, если она битая.
func unescape(s string) string {
	if out, err := url.PathUnescape(s); err == nil {
		return out
	}
	return s
}

// param возвращает первое значение параметра запроса.
func (u proxyURL) param(name string) string { return u.query.Get(name) }

// serverPort проверяет порт и приводит его к числу.
func (u proxyURL) serverPort() (uint16, error) {
	if u.port == "" {
		return 0, fmt.Errorf("%w: в ссылке не указан порт сервера", ErrParse)
	}
	n, err := strconv.ParseUint(u.port, 10, 16)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("%w: неверный порт %q", ErrParse, u.port)
	}
	return uint16(n), nil
}

// server собирает общую для всех протоколов часть outbound'а.
func (u proxyURL) server() (option.ServerOptions, error) {
	port, err := u.serverPort()
	if err != nil {
		return option.ServerOptions{}, err
	}
	return option.ServerOptions{Server: u.host, ServerPort: port}, nil
}

// parseProxyURL разбирает ссылку на прокси. Разбор протоколов повторяет
// `podkop/files/usr/lib/sing_box_config_facade.sh:58` (sing_box_cf_add_proxy_outbound).
func parseProxyURL(raw string) (ParseResult, error) {
	u, err := splitProxyURL(raw)
	if err != nil {
		return ParseResult{}, err
	}

	var (
		typ store.TunnelType
		out *option.Outbound
	)
	switch u.scheme {
	case "socks4", "socks4a", "socks5":
		typ, out, err = u.socksOutbound()
	case "vless":
		typ, out, err = u.vlessOutbound()
	case "ss":
		typ, out, err = u.shadowsocksOutbound()
	case "trojan":
		typ, out, err = u.trojanOutbound()
	case "hysteria2", "hy2":
		typ, out, err = u.hysteria2Outbound()
	default:
		return ParseResult{}, fmt.Errorf("%w: протокол %q не поддерживается", ErrParse, u.scheme)
	}
	if err != nil {
		return ParseResult{}, err
	}

	parsed, err := marshalOutbound(out)
	if err != nil {
		return ParseResult{}, err
	}
	return ParseResult{
		Type:     typ,
		Source:   store.SourceURL,
		Name:     u.fragment,
		Outbound: out,
		Parsed:   parsed,
	}, nil
}

// socksOutbound — socks4/socks4a/socks5. Версия берётся из схемы, логин и пароль
// читаются только у socks5: у ранних версий пароля нет.
func (u proxyURL) socksOutbound() (store.TunnelType, *option.Outbound, error) {
	server, err := u.server()
	if err != nil {
		return "", nil, err
	}
	opts := &option.SOCKSOutboundOptions{
		ServerOptions: server,
		Version:       strings.TrimPrefix(u.scheme, "socks"),
	}
	if u.scheme == "socks5" && u.userinfo != "" {
		opts.Username, opts.Password, _ = strings.Cut(u.userinfo, ":")
	}
	return store.TunnelSOCKS, &option.Outbound{Type: "socks", Options: opts}, nil
}

// vlessOutbound — vless с TLS/Reality и транспортом.
func (u proxyURL) vlessOutbound() (store.TunnelType, *option.Outbound, error) {
	server, err := u.server()
	if err != nil {
		return "", nil, err
	}
	if u.userinfo == "" {
		return "", nil, fmt.Errorf("%w: в ссылке vless нет UUID пользователя", ErrParse)
	}
	// sing-box не умеет шифрование внутри vless: единственное рабочее значение — none.
	if enc := u.param("encryption"); enc != "" && enc != "none" {
		return "", nil, fmt.Errorf("%w: шифрование vless %q не поддерживается", ErrParse, enc)
	}

	opts := &option.VLESSOutboundOptions{
		ServerOptions: server,
		UUID:          u.userinfo,
		Flow:          u.param("flow"),
	}
	if pe := u.param("packetEncoding"); pe != "" {
		opts.PacketEncoding = &pe
	}
	if opts.TLS, err = u.tls(); err != nil {
		return "", nil, err
	}
	if opts.Transport, err = u.transport(); err != nil {
		return "", nil, err
	}
	return store.TunnelVLESS, &option.Outbound{Type: "vless", Options: opts}, nil
}

// shadowsocksOutbound — ss. userinfo либо «method:password» в открытом виде, либо тот
// же текст в base64; проверка формата — как в Podkop (helpers.sh:44).
func (u proxyURL) shadowsocksOutbound() (store.TunnelType, *option.Outbound, error) {
	server, err := u.server()
	if err != nil {
		return "", nil, err
	}
	userinfo, err := shadowsocksUserinfo(u.userinfo)
	if err != nil {
		return "", nil, err
	}
	method, password, _ := strings.Cut(userinfo, ":")

	opts := &option.ShadowsocksOutboundOptions{
		ServerOptions: server,
		Method:        method,
		Password:      password,
	}
	return store.TunnelShadowsocks, &option.Outbound{Type: "shadowsocks", Options: opts}, nil
}

// shadowsocksUserinfo приводит userinfo ссылки ss к виду «method:password».
// Открытый вид опознаётся по форме «a:b» или «a:b:c» — так же, как в Podkop
// (`podkop/files/usr/lib/helpers.sh:44`, is_shadowsocks_userinfo_format).
func shadowsocksUserinfo(userinfo string) (string, error) {
	if userinfo == "" {
		return "", fmt.Errorf("%w: в ссылке ss нет метода и пароля", ErrParse)
	}
	if isPlainShadowsocksUserinfo(userinfo) {
		return userinfo, nil
	}
	decoded, err := decodeBase64(userinfo)
	if err != nil {
		return "", fmt.Errorf("%w: метод и пароль ss не разбираются — это не base64 "+
			"и не «метод:пароль»", ErrParse)
	}
	if !isPlainShadowsocksUserinfo(decoded) {
		return "", fmt.Errorf("%w: после раскодирования base64 в ссылке ss нет "+
			"пары «метод:пароль»", ErrParse)
	}
	return decoded, nil
}

// isPlainShadowsocksUserinfo проверяет форму «a:b» или «a:b:c» с непустыми частями.
func isPlainShadowsocksUserinfo(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
	}
	return true
}

// decodeBase64 пробует все четыре варианта кодировки: клиенты пишут кто во что горазд.
func decodeBase64(s string) (string, error) {
	encodings := []*base64.Encoding{
		base64.RawURLEncoding, base64.URLEncoding,
		base64.RawStdEncoding, base64.StdEncoding,
	}
	for _, enc := range encodings {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), nil
		}
	}
	return "", fmt.Errorf("строка %q не является base64", s)
}

// trojanOutbound — trojan с TLS/Reality и транспортом.
func (u proxyURL) trojanOutbound() (store.TunnelType, *option.Outbound, error) {
	server, err := u.server()
	if err != nil {
		return "", nil, err
	}
	if u.userinfo == "" {
		return "", nil, fmt.Errorf("%w: в ссылке trojan нет пароля", ErrParse)
	}

	opts := &option.TrojanOutboundOptions{ServerOptions: server, Password: u.userinfo}
	if opts.TLS, err = u.tls(); err != nil {
		return "", nil, err
	}
	if opts.Transport, err = u.transport(); err != nil {
		return "", nil, err
	}
	return store.TunnelTrojan, &option.Outbound{Type: "trojan", Options: opts}, nil
}

// hysteria2Outbound — hysteria2/hy2. Транспорта у протокола нет, TLS включён всегда.
func (u proxyURL) hysteria2Outbound() (store.TunnelType, *option.Outbound, error) {
	if u.userinfo == "" {
		return "", nil, fmt.Errorf("%w: в ссылке hysteria2 нет пароля", ErrParse)
	}
	opts := &option.Hysteria2OutboundOptions{Password: u.userinfo}
	opts.Server = u.host

	// Порт может быть диапазоном или списком — hysteria2 умеет прыгать по портам.
	if isMultiPort(u.port) {
		ports, err := parseServerPorts(u.port)
		if err != nil {
			return "", nil, err
		}
		opts.ServerPorts = ports
	} else {
		port, err := u.serverPort()
		if err != nil {
			return "", nil, err
		}
		opts.ServerPort = port
	}

	if obfs := u.param("obfs"); obfs != "" {
		// sing-box отказывается поднимать обфускацию без пароля — ловим это здесь,
		// пока ошибку ещё можно показать рядом с полем ввода.
		password := u.param("obfs-password")
		if password == "" {
			return "", nil, fmt.Errorf("%w: для обфускации %q не указан obfs-password",
				ErrParse, obfs)
		}
		opts.Obfs = &option.Hysteria2Obfs{Type: obfs, Password: password}
	}
	var err error
	if opts.UpMbps, err = optionalMbps(u.param("upmbps")); err != nil {
		return "", nil, err
	}
	if opts.DownMbps, err = optionalMbps(u.param("downmbps")); err != nil {
		return "", nil, err
	}
	if opts.TLS, err = u.tls(); err != nil {
		return "", nil, err
	}
	return store.TunnelHysteria2, &option.Outbound{Type: "hysteria2", Options: opts}, nil
}

// optionalMbps разбирает необязательный лимит полосы.
func optionalMbps(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: неверное ограничение полосы %q", ErrParse, s)
	}
	return n, nil
}

// isMultiPort отличает список или диапазон портов от одиночного порта.
func isMultiPort(port string) bool {
	return strings.ContainsAny(port, ",-")
}

// parseServerPorts переводит «1000-2000,3000» в формат sing-box «1000:2000», «3000:3000».
func parseServerPorts(port string) (badoption.Listable[string], error) {
	var out badoption.Listable[string]
	for _, item := range strings.Split(port, ",") {
		item = strings.TrimSpace(item)
		lo, hi, ranged := strings.Cut(item, "-")
		if !ranged {
			hi = lo
		}
		for _, p := range []string{lo, hi} {
			if n, err := strconv.ParseUint(p, 10, 16); err != nil || n == 0 {
				return nil, fmt.Errorf("%w: неверный порт %q", ErrParse, item)
			}
		}
		out = append(out, lo+":"+hi)
	}
	return out, nil
}

// tls собирает секцию tls по параметру security.
// Портируется из `sing_box_config_facade.sh:174` (_add_outbound_security).
func (u proxyURL) tls() (*option.OutboundTLSOptions, error) {
	security := u.param("security")
	if security == "" && (u.scheme == "hysteria2" || u.scheme == "hy2") {
		// У hysteria2 транспорт всегда поверх QUIC с TLS, параметра в ссылке может не быть.
		security = "tls"
	}
	switch security {
	case "", "none":
		return nil, nil
	case "tls", "reality":
	default:
		return nil, fmt.Errorf("%w: неизвестное значение security=%q", ErrParse, security)
	}

	tls := &option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: u.param("sni"),
		Insecure:   u.insecure(),
		ALPN:       splitComma(u.param("alpn")),
	}
	// uTLS подменяет отпечаток TLS-клиента; у hysteria2 своего TLS-стека нет.
	if fp := u.param("fp"); fp != "" && u.scheme != "hysteria2" && u.scheme != "hy2" {
		tls.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: fp}
	}
	if pbk := u.param("pbk"); pbk != "" {
		tls.Reality = &option.OutboundRealityOptions{
			Enabled:   true,
			PublicKey: pbk,
			ShortID:   u.param("sid"),
		}
	}
	return tls, nil
}

// insecure читает «не проверять сертификат»: у разных клиентов параметр называется
// по-разному (facade.sh:224, _get_insecure_query_param_from_url).
func (u proxyURL) insecure() bool {
	v := u.param("allowInsecure")
	if v == "" {
		v = u.param("insecure")
	}
	return v == "1" || strings.EqualFold(v, "true")
}

// transport собирает секцию transport по параметру type.
// Портируется из `sing_box_config_facade.sh:236` (_add_outbound_transport).
func (u proxyURL) transport() (*option.V2RayTransportOptions, error) {
	switch t := u.param("type"); t {
	case "", "tcp", "raw":
		return nil, nil
	case "ws":
		ws := option.V2RayWebsocketOptions{Path: u.param("path")}
		if host := u.param("host"); host != "" {
			ws.Headers = badoption.HTTPHeader{"Host": badoption.Listable[string]{host}}
		}
		if ed := u.param("ed"); ed != "" {
			n, err := strconv.ParseUint(ed, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("%w: неверный размер early data %q", ErrParse, ed)
			}
			ws.MaxEarlyData = uint32(n)
		}
		return &option.V2RayTransportOptions{Type: "ws", WebsocketOptions: ws}, nil
	case "grpc":
		return &option.V2RayTransportOptions{
			Type:        "grpc",
			GRPCOptions: option.V2RayGRPCOptions{ServiceName: u.param("serviceName")},
		}, nil
	case "httpupgrade":
		return &option.V2RayTransportOptions{
			Type: "httpupgrade",
			HTTPUpgradeOptions: option.V2RayHTTPUpgradeOptions{
				Host: u.param("host"),
				Path: u.param("path"),
			},
		}, nil
	case "http":
		http := option.V2RayHTTPOptions{Path: u.param("path")}
		if host := u.param("host"); host != "" {
			http.Host = badoption.Listable[string]{host}
		}
		return &option.V2RayTransportOptions{Type: "http", HTTPOptions: http}, nil
	default:
		// kcp (mKCP) и xhttp — транспорты Xray, в sing-box их нет вовсе.
		return nil, fmt.Errorf("%w: транспорт %q не поддерживается sing-box", ErrParse, t)
	}
}

// splitComma режет список через запятую, отбрасывая пустые элементы.
func splitComma(s string) badoption.Listable[string] {
	if s == "" {
		return nil
	}
	var out badoption.Listable[string]
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
