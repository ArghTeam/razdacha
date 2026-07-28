package netstack

import (
	"context"
	"fmt"
	"net/netip"
	"sort"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"go4.org/netipx"
)

// DiagNft читает состояние таблицы `inet razdacha` отдельным соединением.
//
// Соединение своё, а не то, которым заливались правила: диагностика ходит из
// обработчика HTTP, параллельно нескольким запросам, а соединение nftables на
// параллельное использование не рассчитано. Чтение дешёвое — netlink-сокет
// открывается и закрывается на каждый запрос внутри самой библиотеки.
func DiagNft(_ context.Context) (DiagNftState, error) {
	n, err := NewNft()
	if err != nil {
		return DiagNftState{}, err
	}
	return n.DiagState()
}

// DiagState собирает снимок таблицы: цепочки, сеты, наличие masquerade и
// размер сета подсетей. Ничего не меняет и не требует прав сверх тех, с
// которыми демон уже залил правила.
func (n *Nft) DiagState() (DiagNftState, error) {
	out := DiagNftState{Table: NftTable}

	table, err := n.existingTable()
	if err != nil {
		return DiagNftState{}, err
	}
	if table == nil {
		return out, nil
	}
	out.Exists = true

	chains, err := n.conn.ListChainsOfTableFamily(nftables.TableFamilyINet)
	if err != nil {
		return DiagNftState{}, fmt.Errorf("список цепочек таблицы %s: %w", NftTable, err)
	}
	var postrouting *nftables.Chain
	for _, c := range chains {
		if c.Table == nil || c.Table.Name != NftTable {
			continue
		}
		out.Chains = append(out.Chains, c.Name)
		if c.Name == ChainPostrouting {
			postrouting = c
		}
	}

	sets, err := n.conn.GetSets(table)
	if err != nil {
		return DiagNftState{}, fmt.Errorf("список сетов таблицы %s: %w", NftTable, err)
	}
	for _, s := range sets {
		out.Sets = append(out.Sets, s.Name)
		if s.Name != SetSubnets {
			continue
		}
		elements, err := n.conn.GetSetElements(s)
		if err != nil {
			return DiagNftState{}, fmt.Errorf("содержимое сета %s: %w", s.Name, err)
		}
		out.Subnets = diagRanges(elements)
	}

	if postrouting != nil {
		rules, err := n.conn.GetRules(table, postrouting)
		if err != nil {
			return DiagNftState{}, fmt.Errorf("правила цепочки %s: %w", ChainPostrouting, err)
		}
		out.Masquerade, out.MasqueradeOIf = diagMasquerade(rules)
	}
	return out, nil
}

// diagMaxV4 — верх адресного пространства: им закрывается интервал, у которого
// маркера конца нет и быть не может (следующего адреса за 255.255.255.255 не
// существует, поэтому [setElements] маркер и не ставит).
var diagMaxV4 = netip.AddrFrom4([4]byte{255, 255, 255, 255})

// diagBound — граница интервала: адрес и то, открывает он диапазон или
// закрывает.
type diagBound struct {
	addr netip.Addr
	end  bool
}

// diagRanges собирает интервалы сета обратно из того, что отдаёт ядро.
//
// Форм две, и встречаются обе. Интервал одним элементом — границы в Key и
// KeyEnd, конец включающий. Интервал парой элементов — начало и отдельный
// элемент с флагом IntervalEnd, конец исключающий; так их пишет [setElements].
//
// **Порядок элементов в дампе ничего не гарантирует.** Живое ядро 6.12 отдаёт
// пары «конец, начало», то есть маркер приходит раньше своего начала (issue
// #123: разбор по порядку склеивал соседние интервалы в один и объявлял сет
// покрывающим что угодно). Поэтому границы сначала собираются, потом
// сортируются по адресу, и только потом разбираются на интервалы. На равных
// адресах конец идёт раньше начала: два стыкующихся интервала — это два
// интервала, а не вложенность.
func diagRanges(elements []nftables.SetElement) []netipx.IPRange {
	var out []netipx.IPRange
	add := func(from, to netip.Addr) {
		if r := netipx.IPRangeFrom(from, to); r.IsValid() {
			out = append(out, r)
		}
	}

	bounds := make([]diagBound, 0, len(elements))
	for _, e := range elements {
		key, ok := diagAddr4(e.Key)
		if !ok {
			continue
		}
		if end, ok := diagAddr4(e.KeyEnd); ok {
			add(key, end)
			continue
		}
		bounds = append(bounds, diagBound{addr: key, end: e.IntervalEnd})
	}
	sort.Slice(bounds, func(i, j int) bool {
		if c := bounds[i].addr.Compare(bounds[j].addr); c != 0 {
			return c < 0
		}
		return bounds[i].end && !bounds[j].end
	})

	var start netip.Addr
	open := false
	for _, b := range bounds {
		if b.end {
			if open {
				add(start, b.addr.Prev())
				open = false
			}
			// Маркер, которому нечего закрывать, — не наш интервал: молча
			// пропускаем, выдумывать под него диапазон нельзя.
			continue
		}
		if open {
			add(start, b.addr.Prev())
		}
		start, open = b.addr, true
	}
	if open {
		add(start, diagMaxV4)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].From().Compare(out[j].From()) < 0 })
	return out
}

// diagAddr4 — адрес IPv4 из ключа элемента. Ключи чужих семейств и пустые
// пропускаются: сет объявлен как ipv4_addr (ADR 0005).
func diagAddr4(key []byte) (netip.Addr, bool) {
	addr, ok := netip.AddrFromSlice(key)
	if !ok || !addr.Is4() {
		return netip.Addr{}, false
	}
	return addr, true
}

// diagMasquerade ищет в правилах цепочки masquerade и интерфейс, на который он
// смотрит. Имя интерфейса — данные сравнения после `meta oifname`, из ядра оно
// приходит строкой с завершающим нулём.
func diagMasquerade(rules []*nftables.Rule) (bool, string) {
	for _, r := range rules {
		oif := ""
		lastMeta := ""
		for _, e := range r.Exprs {
			switch v := e.(type) {
			case *expr.Meta:
				lastMeta = ""
				if v.Key == expr.MetaKeyOIFNAME {
					lastMeta = "oifname"
				}
			case *expr.Cmp:
				if lastMeta == "oifname" {
					oif = diagTrimZero(string(v.Data))
				}
				lastMeta = ""
			case *expr.Masq:
				return true, oif
			default:
				lastMeta = ""
			}
		}
	}
	return false, ""
}

// diagTrimZero снимает завершающий нулевой байт имени интерфейса.
func diagTrimZero(s string) string {
	for len(s) > 0 && s[len(s)-1] == 0 {
		s = s[:len(s)-1]
	}
	return s
}
