package netstack

import (
	"context"
	"fmt"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
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
		out.SubnetIntervals = diagCountIntervals(elements)
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

// diagCountIntervals считает начала интервалов. Диапазон лежит в ядре парой
// элементов — началом и маркером конца, — поэтому маркеры в счёт не идут:
// иначе число подсетей в сводке оказывается вдвое больше настоящего.
func diagCountIntervals(elements []nftables.SetElement) int {
	n := 0
	for _, e := range elements {
		if !e.IntervalEnd {
			n++
		}
	}
	return n
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
