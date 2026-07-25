package lists

import (
	"bufio"
	"bytes"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
)

// Parse разбирает тело списка по его виду.
func Parse(body []byte, format Format) (List, error) {
	switch format {
	case FormatSRS:
		return parseSRS(body)
	case FormatJSON:
		return parseJSON(body)
	default:
		return ParsePlain(body), nil
	}
}

// ParsePlain разбирает список «по строке на запись»: подсети, отдельные адреса
// и домены вперемешку — так устроены и .lst из allow-domains, и списки, которые
// пользователь приносит своим URL.
//
// Неразбираемая строка не отменяет список целиком: источник чужой, из-за одной
// битой строки терять остальные тысячи нельзя. Такие строки считаются в Skipped.
func ParsePlain(body []byte) List {
	var out List
	sc := bufio.NewScanner(bytes.NewReader(body))
	// Строки коротки, но буфер по умолчанию (64 КиБ) на слипшемся в одну
	// строку файле обрывает разбор молча.
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimSuffix(sc.Text(), "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") ||
			strings.HasPrefix(line, "//") {
			continue
		}
		// Комментарий в хвосте строки: встречается в списках подсетей.
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
			if line == "" {
				continue
			}
		}
		// Строка вида «1.2.3.0/24 Cloudflare»: берём первое поле.
		if i := strings.IndexAny(line, " \t"); i >= 0 {
			line = line[:i]
		}

		switch {
		case isSubnet(line):
			if p, ok := ipv4Prefix(line); ok {
				out.Subnets = append(out.Subnets, p)
			} else {
				// IPv6 у клиентов выключен (ADR 0005), в nft-сете ему места нет.
				out.Skipped++
			}
		case isDomain(line):
			out.Domains = append(out.Domains, normalizeDomain(line))
		default:
			out.Skipped++
		}
	}
	if err := sc.Err(); err != nil {
		// Читаем из памяти: ошибка тут означает только переполнение буфера.
		out.Skipped++
	}

	out.Domains = dedup(out.Domains)
	out.Subnets = dedup(out.Subnets)
	return out
}

// parseSRS разбирает бинарный набор sing-box. Нужен ради подсетей: FakeIP их не
// закрывает, а sing-box наружу содержимое набора не отдаёт.
func parseSRS(body []byte) (List, error) {
	// recover = true: без него Read оставляет скомпилированные матчеры и не
	// заполняет domain_suffix и ip_cidr, а нам нужно именно содержимое.
	// Podkop получает то же самое внешним `sing-box rule-set decompile`.
	compat, err := srs.Read(bytes.NewReader(body), true)
	if err != nil {
		return List{}, fmt.Errorf("%w: разбор набора .srs: %w", ErrBadResponse, err)
	}
	set, err := compat.Upgrade()
	if err != nil {
		return List{}, fmt.Errorf("%w: версия набора .srs: %w", ErrBadResponse, err)
	}
	return fromHeadless(set.Rules), nil
}

// parseJSON разбирает текстовый набор sing-box.
func parseJSON(body []byte) (List, error) {
	var compat option.PlainRuleSetCompat
	if err := json.Unmarshal(body, &compat); err != nil {
		return List{}, fmt.Errorf("%w: разбор набора .json: %w", ErrBadResponse, err)
	}
	set, err := compat.Upgrade()
	if err != nil {
		return List{}, fmt.Errorf("%w: версия набора .json: %w", ErrBadResponse, err)
	}
	return fromHeadless(set.Rules), nil
}

// fromHeadless собирает домены и подсети из записей набора, включая вложенные
// логические записи.
func fromHeadless(rules []option.HeadlessRule) List {
	var out List
	var walk func([]option.HeadlessRule)
	walk = func(rr []option.HeadlessRule) {
		for _, r := range rr {
			switch r.Type {
			case C.RuleTypeLogical:
				walk(r.LogicalOptions.Rules)
			default:
				out.Domains = append(out.Domains, r.DefaultOptions.Domain...)
				out.Domains = append(out.Domains, r.DefaultOptions.DomainSuffix...)
				for _, cidr := range r.DefaultOptions.IPCIDR {
					if p, ok := ipv4Prefix(cidr); ok {
						out.Subnets = append(out.Subnets, p)
					} else {
						out.Skipped++
					}
				}
			}
		}
	}
	walk(rules)

	out.Domains = dedup(out.Domains)
	out.Subnets = dedup(out.Subnets)
	return out
}

// isSubnet — похожа ли строка на подсеть или адрес.
func isSubnet(s string) bool {
	if strings.Contains(s, "/") {
		_, err := netip.ParsePrefix(s)
		return err == nil
	}
	_, err := netip.ParseAddr(s)
	return err == nil
}

// ipv4Prefix приводит подсеть или одиночный адрес к префиксу IPv4. Второе
// значение false для IPv6 и мусора.
func ipv4Prefix(s string) (string, bool) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil || !p.Addr().Is4() {
			return "", false
		}
		return p.Masked().String(), true
	}
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() {
		return "", false
	}
	return netip.PrefixFrom(a, 32).String(), true
}

// isDomain — похожа ли строка на доменное имя.
func isDomain(s string) bool {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "*."), ".")
	if s == "" || len(s) > 253 || !strings.Contains(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
				r >= '0' && r <= '9', r == '-', r == '_':
			default:
				return false
			}
		}
	}
	return true
}

// normalizeDomain убирает разметку подстановки: в наборах sing-box совпадение
// по суффиксу задаётся самим полем, а не звёздочкой.
func normalizeDomain(s string) string {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "*."), ".")
	return strings.ToLower(s)
}

// dedup сортирует и убирает повторы: списки перекрываются между собой, а в
// nft-сет один элемент дважды не добавить.
func dedup(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:0]
	var prev string
	for i, v := range in {
		if i > 0 && v == prev {
			continue
		}
		out = append(out, v)
		prev = v
	}
	return out
}
