package singbox

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/ArghTeam/razdacha/internal/store"
)

// hasInterfaceSection опознаёт INI-конфиг WireGuard по обязательной секции [Interface].
func hasInterfaceSection(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "[Interface]") {
			return true
		}
	}
	return false
}

// iniSection — одна секция INI-файла: имя и пары «ключ — значение» в порядке появления.
type iniSection struct {
	name   string
	values map[string]string
}

// get возвращает значение ключа без учёта регистра.
func (s iniSection) get(key string) string { return s.values[strings.ToLower(key)] }

// parseINI разбирает INI-подмножество, которым пользуется wg-quick: секции в квадратных
// скобках, пары «ключ = значение», комментарии с «#» и «;».
func parseINI(text string) ([]iniSection, error) {
	var (
		sections []iniSection
		current  *iniSection
	)
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("%w: строка %d — незакрытая секция %q",
					ErrParse, n+1, line)
			}
			sections = append(sections, iniSection{
				name:   strings.ToLower(strings.TrimSpace(line[1 : len(line)-1])),
				values: map[string]string{},
			})
			current = &sections[len(sections)-1]
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%w: строка %d — ожидалось «ключ = значение», а не %q",
				ErrParse, n+1, line)
		}
		if current == nil {
			return nil, fmt.Errorf("%w: строка %d — параметр %q вне секции",
				ErrParse, n+1, strings.TrimSpace(key))
		}
		current.values[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return sections, nil
}

// parseWireGuardConf разбирает .conf в endpoint sing-box.
//
// Туннель поднимается внутри sing-box, в userspace: сетевого интерфейса не появляется,
// bind_interface не используется — см. docs/decisions/0002-userspace-wireguard-outbound.md.
// Поэтому поле System остаётся false, а Name — пустым.
func parseWireGuardConf(text string) (ParseResult, error) {
	sections, err := parseINI(text)
	if err != nil {
		return ParseResult{}, err
	}

	opts := &option.WireGuardEndpointOptions{}
	var seenInterface bool
	for _, sec := range sections {
		switch sec.name {
		case "interface":
			if seenInterface {
				return ParseResult{}, fmt.Errorf(
					"%w: в конфиге больше одной секции [Interface]", ErrParse)
			}
			seenInterface = true
			if err := fillInterface(opts, sec); err != nil {
				return ParseResult{}, err
			}
		case "peer":
			peer, err := buildPeer(sec)
			if err != nil {
				return ParseResult{}, err
			}
			opts.Peers = append(opts.Peers, peer)
		default:
			return ParseResult{}, fmt.Errorf("%w: неизвестная секция [%s]", ErrParse, sec.name)
		}
	}
	if len(opts.Peers) == 0 {
		return ParseResult{}, fmt.Errorf("%w: в конфиге нет секции [Peer]", ErrParse)
	}

	endpoint := &option.Endpoint{Type: "wireguard", Options: opts}
	parsed, err := marshalEndpoint(endpoint)
	if err != nil {
		return ParseResult{}, err
	}
	return ParseResult{
		Type:     store.TunnelWireGuard,
		Source:   store.SourceWGConf,
		Endpoint: endpoint,
		Parsed:   parsed,
	}, nil
}

// fillInterface переносит секцию [Interface] в опции endpoint'а. DNS, Table и хуки
// wg-quick игнорируются: маршрутизацией занимается sing-box, а не интерфейс.
func fillInterface(opts *option.WireGuardEndpointOptions, sec iniSection) error {
	opts.PrivateKey = sec.get("PrivateKey")
	if opts.PrivateKey == "" {
		return fmt.Errorf("%w: в секции [Interface] нет PrivateKey", ErrParse)
	}

	addresses, err := parsePrefixList(sec.get("Address"))
	if err != nil {
		return fmt.Errorf("%w: Address в секции [Interface]: %w", ErrParse, err)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("%w: в секции [Interface] нет Address", ErrParse)
	}
	opts.Address = addresses

	if v := sec.get("MTU"); v != "" {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return fmt.Errorf("%w: неверный MTU %q", ErrParse, v)
		}
		opts.MTU = uint32(n)
	}
	if v := sec.get("ListenPort"); v != "" {
		n, err := strconv.ParseUint(v, 10, 16)
		if err != nil {
			return fmt.Errorf("%w: неверный ListenPort %q", ErrParse, v)
		}
		opts.ListenPort = uint16(n)
	}
	return nil
}

// buildPeer переносит секцию [Peer] в описание пира.
func buildPeer(sec iniSection) (option.WireGuardPeer, error) {
	peer := option.WireGuardPeer{
		PublicKey:    sec.get("PublicKey"),
		PreSharedKey: sec.get("PresharedKey"),
	}
	if peer.PublicKey == "" {
		return option.WireGuardPeer{}, fmt.Errorf("%w: в секции [Peer] нет PublicKey", ErrParse)
	}

	endpoint := sec.get("Endpoint")
	if endpoint == "" {
		return option.WireGuardPeer{}, fmt.Errorf("%w: в секции [Peer] нет Endpoint", ErrParse)
	}
	host, port, err := splitHostPort(endpoint)
	if err != nil {
		return option.WireGuardPeer{}, err
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return option.WireGuardPeer{}, fmt.Errorf("%w: неверный порт в Endpoint %q",
			ErrParse, endpoint)
	}
	peer.Address, peer.Port = host, uint16(n)

	allowed, err := parsePrefixList(sec.get("AllowedIPs"))
	if err != nil {
		return option.WireGuardPeer{}, fmt.Errorf("%w: AllowedIPs в секции [Peer]: %w",
			ErrParse, err)
	}
	if len(allowed) == 0 {
		// wg-quick без AllowedIPs не гонит в пир ничего; полный туннель — разумный
		// дефолт для арендованного сервера, ради которого конфиг и вставляют.
		allowed = badoption.Listable[netip.Prefix]{
			netip.MustParsePrefix("0.0.0.0/0"),
			netip.MustParsePrefix("::/0"),
		}
	}
	peer.AllowedIPs = allowed

	if v := sec.get("PersistentKeepalive"); v != "" {
		k, err := strconv.ParseUint(v, 10, 16)
		if err != nil {
			return option.WireGuardPeer{}, fmt.Errorf("%w: неверный PersistentKeepalive %q",
				ErrParse, v)
		}
		peer.PersistentKeepaliveInterval = uint16(k)
	}

	if v := sec.get("Reserved"); v != "" {
		reserved, err := parseReserved(v)
		if err != nil {
			return option.WireGuardPeer{}, err
		}
		peer.Reserved = reserved
	}
	return peer, nil
}

// reservedLen — длина client ID: три байта, которые Cloudflare ждёт в заголовке пакета.
const reservedLen = 3

// parseReserved разбирает поле Reserved секции [Peer].
//
// В wg-quick такого поля нет: это расширение под Cloudflare WARP, где эндпоинт молча
// отбрасывает пакеты без трёхбайтового client ID. Обычному WireGuard-серверу поле не
// нужно, поэтому его отсутствие остаётся законным, а вот заданное неверно — ошибка:
// туннель с битым client ID поднимется и не пропустит ни байта.
//
// Принимаются обе записи, в которых client ID ходит по свету: три числа через запятую
// («0, 0, 0», квадратные скобки необязательны) и base64 из четырёх символов, как его
// отдают генераторы конфигов WARP.
func parseReserved(v string) ([]uint8, error) {
	body := strings.TrimSpace(v)
	if b, ok := strings.CutPrefix(body, "["); ok {
		rest, closed := strings.CutSuffix(strings.TrimSpace(b), "]")
		if !closed {
			return nil, fmt.Errorf("%w: неверный Reserved %q: незакрытая скобка", ErrParse, v)
		}
		body = strings.TrimSpace(rest)
	}

	// Запятая — единственный надёжный признак списка: client ID из одних цифр («1234»)
	// разбирать как число нельзя, он тоже законный base64.
	if strings.Contains(body, ",") {
		return parseReservedNumbers(v, body)
	}
	return parseReservedBase64(v, body)
}

// parseReservedNumbers разбирает запись списком: три числа 0–255 через запятую.
func parseReservedNumbers(orig, body string) ([]uint8, error) {
	parts := strings.Split(body, ",")
	if len(parts) != reservedLen {
		return nil, fmt.Errorf("%w: неверный Reserved %q: нужно ровно три числа 0–255",
			ErrParse, orig)
	}
	out := make([]uint8, 0, reservedLen)
	for _, part := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(part), 10, 8)
		if err != nil {
			return nil, fmt.Errorf("%w: неверный Reserved %q: %q — не число 0–255",
				ErrParse, orig, strings.TrimSpace(part))
		}
		out = append(out, uint8(n))
	}
	return out, nil
}

// parseReservedBase64 разбирает запись client ID в base64 — с набивкой и без, в обычном
// алфавите и в URL-safe: генераторы конфигов WARP расходятся во всех трёх мелочах.
func parseReservedBase64(orig, body string) ([]uint8, error) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		raw, err := enc.DecodeString(body)
		if err != nil {
			continue
		}
		if len(raw) != reservedLen {
			return nil, fmt.Errorf("%w: неверный Reserved %q: client ID — три байта, а не %d",
				ErrParse, orig, len(raw))
		}
		return []uint8(raw), nil
	}
	return nil, fmt.Errorf(
		"%w: неверный Reserved %q: ожидались три числа 0–255 через запятую или client ID в base64",
		ErrParse, orig)
}

// parsePrefixList разбирает список подсетей через запятую. Голый адрес без маски
// считается одиночным хостом: wg-quick допускает и такую запись.
func parsePrefixList(s string) (badoption.Listable[netip.Prefix], error) {
	var out badoption.Listable[netip.Prefix]
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, fmt.Errorf("подсеть %q не разбирается", item)
			}
			out = append(out, p)
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("адрес %q не разбирается", item)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}
