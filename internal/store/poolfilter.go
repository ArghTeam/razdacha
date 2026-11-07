package store

import (
	"encoding/base64"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Отбраковка члена пула (ADR 0020).
//
// Живёт в store, а не в lists, где идёт обход, и не в singbox, где собирается конфиг:
// зовут её оба, а импортировать друг друга они не могут и не должны. Общее у них —
// ровно `store`: здесь лежит и [PoolServer], и [Settings] с чёрным списком. Второй
// экземпляр таблицы стран в другом слое разошёлся бы с первым молча.
//
// Проверка офлайновая по построению: разбирается подпись карточки и сама ссылка, в
// сеть не ходит никто. Обещание «пул выключен — наружу за ключами никто не ходит»
// фильтром не нарушается (issue #161, ADR 0020).

// Причины отбраковки. Текст уходит в панель как есть — как и все ошибки, доходящие
// до UI.
const (
	poolReasonNoEncryption = "transport encryption is absent or cannot be verified"
	poolReasonCountry      = "страна в чёрном списке: "
)

// DefaultPoolCountryBlocklist — страны, ноды которых в пул не берутся по умолчанию.
//
// RU и BY: пул наполняется бесплатными ключами, `urltest` выбирает быстрейший живой,
// а быстрейшим систематически оказывается ближайший — то есть российский. Шлюз,
// уводящий трафик правила через ту же страну, от которой его и заводили, бесполезен
// (ADR 0020).
//
// Функция, а не переменная: экспортированный срез правится вызывающим на месте, и
// дефолт перестал бы быть дефолтом после первой такой правки.
func DefaultPoolCountryBlocklist() []string { return []string{"RU", "BY"} }

// PoolFilter — чем отбраковывается член пула. Нулевое значение не отбраковывает
// ничего по стране, но ключ без шифрования отвергает всегда: отсутствие шифрования —
// не вопрос настройки.
type PoolFilter struct {
	// Countries — ISO-коды стран, ноды которых в пул не идут.
	Countries []string
}

// PoolFilterFrom собирает фильтр из настроек.
func PoolFilterFrom(v Settings) PoolFilter {
	return PoolFilter{Countries: v.PoolCountryBlocklist}
}

// Allows — проходит ли сервер фильтр.
func (f PoolFilter) Allows(s PoolServer) bool { return f.Exclusion(s) == "" }

// Exclusion — причина, по которой сервер в пул не берётся. Пустая строка означает
// «берётся».
//
// Страна проверяется первой: она же и есть причина, ради которой решение принималось,
// и в панели про российскую ноду без TLS честнее сказать про страну.
func (f PoolFilter) Exclusion(s PoolServer) string {
	if code := f.blockedCountry(strings.ToUpper(strings.TrimSpace(s.Country))); code != "" {
		return poolReasonCountry + code
	}
	if code := f.blockedCountry(s.Title); code != "" {
		return poolReasonCountry + code
	}
	if poolKeyUnencrypted(s.URL) {
		return poolReasonNoEncryption
	}
	return ""
}

// blockedCountry — какая страна чёрного списка упомянута в подписи карточки.
//
// Отбраковка идёт по **любому** совпадению: карточка вида «🇷🇺 🇺🇸» (anycast) содержит
// два флага, часть её выходов в РФ, и какая именно — из подписи не видно. Пропустить
// такую значит пропустить утечку, выбросить — потерять одну ноду из полутора сотен
// (ADR 0020).
//
// Совпадением считаются три вещи: флаг-эмодзи (пара regional indicator), текстовое
// название страны из [poolCountryAliases] и голый ISO-код отдельным словом в верхнем
// регистре. Голый код только в верхнем регистре не от вкуса: «by» — обычное английское
// слово, и в подписи «Frankfurt by AEZA» оно означает не Беларусь.
func (f PoolFilter) blockedCountry(title string) string {
	if title == "" || len(f.Countries) == 0 {
		return ""
	}
	flags := poolFlagCountries(title)
	lower := strings.ToLower(title)

	for _, raw := range f.Countries {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if code == "" {
			continue
		}
		if flags[code] {
			return code
		}
		for _, name := range poolCountryAliases[code] {
			if containsWord(lower, name) {
				return code
			}
		}
		if containsWord(title, code) {
			return code
		}
	}
	return ""
}

// poolCountryAliases — текстовые названия стран, по которым узнаётся подпись без
// флага. Полного справочника здесь нет намеренно: флаг-эмодзи разбирается для любого
// ISO-кода, а названия нужны лишь там, где источник пишет их словами.
//
// Города в таблицу не входят: «Moscow» бывает в Айдахо, а «Орёл» — птицей. Ложное
// срабатывание стоит выброшенной ноды, но подпись — единственное, что у нас есть, и
// добавлять к её недостоверности ещё и свои догадки незачем.
var poolCountryAliases = map[string][]string{
	"RU": {"россия", "российская федерация", "рф", "russia", "russian federation"},
	"BY": {"беларусь", "белоруссия", "республика беларусь", "рб", "belarus"},
	"CN": {"китай", "china"},
	"IR": {"иран", "iran"},
	"KP": {"кндр", "северная корея", "north korea"},
}

// poolFlagCountries собирает ISO-коды всех флагов-эмодзи подписи.
//
// Флаг — пара символов regional indicator (U+1F1E6…U+1F1FF), каждый из которых
// кодирует букву: 🇷 + 🇺 = RU. Разбор ручной и без таблиц — соответствие буквам прямое.
func poolFlagCountries(title string) map[string]bool {
	const (
		first = '\U0001F1E6'
		last  = '\U0001F1FF'
	)
	out := make(map[string]bool)
	runes := []rune(title)
	for i := 0; i+1 < len(runes); i++ {
		a, b := runes[i], runes[i+1]
		if a < first || a > last || b < first || b > last {
			continue
		}
		out[string([]rune{'A' + (a - first), 'A' + (b - first)})] = true
		// Пара разобрана целиком: без сдвига «🇷🇺🇺🇸» дало бы ещё и мнимое «UU».
		i++
	}
	return out
}

// containsWord — встречается ли needle в haystack отдельным словом. Границей считается
// всё, что не буква и не цифра: подпись каталога — это флаг, запятые, дефисы и пробелы.
func containsWord(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for at := 0; ; {
		i := strings.Index(haystack[at:], needle)
		if i < 0 {
			return false
		}
		i += at
		if boundaryBefore(haystack, i) && boundaryAfter(haystack, i+len(needle)) {
			return true
		}
		at = i + 1
		if at >= len(haystack) {
			return false
		}
	}
}

func boundaryBefore(s string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return !isWordRune(r)
}

func boundaryAfter(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return !isWordRune(r)
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// poolKeyUnencrypted rejects automatic members without verifiable encryption.
// Manual tunnel parsing is deliberately independent of this catalog policy.
func poolKeyUnencrypted(rawURL string) bool {
	s := strings.TrimSpace(rawURL)
	i := strings.Index(s, "://")
	if i < 0 {
		return false
	}
	switch strings.ToLower(s[:i]) {
	case "ss":
		return !poolShadowsocksEncrypted(s[i+3:])
	case "vless", "trojan":
	default:
		return false
	}

	rest := s[i+3:]
	if h := strings.Index(rest, "#"); h >= 0 {
		rest = rest[:h]
	}
	q := ""
	if j := strings.Index(rest, "?"); j >= 0 {
		q = rest[j+1:]
	}
	values, err := url.ParseQuery(q)
	if err != nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(values.Get("security"))) {
	case "", "none":
		return true
	default:
		return false
	}
}

// SplitPool делит состав пула на прошедший фильтр и отбракованный с причинами.
//
// Нужен слою api: панели показываются оба списка — состав пула и, отдельно,
// исключённые. Молча исчезнувшая нода выглядела бы поломкой каталога, а не работой
// фильтра (ADR 0020).
func SplitPool(pool []PoolServer, f PoolFilter) (keep []PoolServer, excluded []PoolExclusion) {
	for _, s := range pool {
		if reason := f.Exclusion(s); reason != "" {
			excluded = append(excluded, PoolExclusion{Server: s, Reason: reason})
			continue
		}
		keep = append(keep, s)
	}
	return keep, excluded
}

// PoolExclusion — отбракованный член пула и причина отбраковки.
type PoolExclusion struct {
	Server PoolServer
	Reason string
}

// poolShadowsocksEncrypted accepts known encrypted methods of the pinned runtime.
// Userinfo parsing matches singbox, including '/' within a password.
func poolShadowsocksEncrypted(rest string) bool {
	rest, _, _ = strings.Cut(rest, "#")
	rest, query, _ := strings.Cut(rest, "?")
	if _, err := url.ParseQuery(query); err != nil {
		return false
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return false
	}
	userinfo, err := url.PathUnescape(rest[:at])
	if err != nil {
		return false
	}
	if !strings.Contains(userinfo, ":") {
		decoded := false
		for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
			if b, err := enc.DecodeString(userinfo); err == nil {
				userinfo, decoded = string(b), true
				break
			}
		}
		if !decoded {
			return false
		}
	}
	parts := strings.Split(userinfo, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	// The runtime does not normalize cipher names.
	switch parts[0] {
	case "aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305",
		"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305",
		"aes-128-ctr", "aes-192-ctr", "aes-256-ctr", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "rc4-md5", "chacha20-ietf", "xchacha20":
		return true
	default:
		return false
	}
}
