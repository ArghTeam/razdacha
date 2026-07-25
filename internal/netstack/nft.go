//go:build linux

package netstack

import (
	"fmt"
	"net/netip"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"go4.org/netipx"
	"golang.org/x/sys/unix"
)

// icmpv6AdminProhibited — код ICMPv6 «administratively prohibited»
// (RFC 4443, type 1 code 1). Слой 3 отключения IPv6 (ADR 0005).
const icmpv6AdminProhibited = 1

// nftConn — то, что слой берёт от netlink-соединения nftables. Интерфейс
// существует ради тестов: подменённое соединение записывает вызовы, и состав
// таблицы проверяется без root и без ядра.
type nftConn interface {
	ListTablesOfFamily(family nftables.TableFamily) ([]*nftables.Table, error)
	DelTable(t *nftables.Table)
	AddTable(t *nftables.Table) *nftables.Table
	AddChain(c *nftables.Chain) *nftables.Chain
	AddSet(s *nftables.Set, vals []nftables.SetElement) error
	AddRule(r *nftables.Rule) *nftables.Rule
	Flush() error
}

// Nft заливает и снимает таблицу inet razdacha.
type Nft struct {
	conn nftConn
}

// NewNft открывает netlink-соединение с подсистемой nftables.
func NewNft() (*Nft, error) {
	c, err := nftables.New()
	if err != nil {
		return nil, fmt.Errorf("подключение к nftables: %w", err)
	}
	return &Nft{conn: c}, nil
}

// Apply перезаливает таблицу целиком по состоянию cfg и возвращает собранный
// набор — вызывающему он нужен ради SkippedSubnets в логе.
//
// Атомарность: снятие старой таблицы, создание новой, сеты и правила уходят в
// ядро одним пакетом изменений и применяются одной транзакцией. Окна, в котором
// правил нет и трафик уходит мимо туннеля, не возникает; при ошибке в любом
// объекте ядро откатывает пакет целиком и остаётся прежняя таблица.
func (n *Nft) Apply(cfg NftConfig) (NftRuleset, error) {
	rs, err := BuildNftRuleset(cfg)
	if err != nil {
		return NftRuleset{}, err
	}

	existing, err := n.existingTable()
	if err != nil {
		return NftRuleset{}, err
	}
	if existing != nil {
		n.conn.DelTable(existing)
	}

	table := n.conn.AddTable(&nftables.Table{
		Family: nftables.TableFamilyINet,
		Name:   rs.Table,
	})

	sets := make(map[string]*nftables.Set, len(rs.Sets))
	for _, s := range rs.Sets {
		set := &nftables.Set{
			Table:    table,
			Name:     s.Name,
			KeyType:  nftables.TypeIPAddr,
			Interval: true,
		}
		if err := n.conn.AddSet(set, setElements(s.Elements)); err != nil {
			return NftRuleset{}, fmt.Errorf("сет %s: %w", s.Name, err)
		}
		sets[s.Name] = set
	}

	for _, ch := range rs.Chains {
		chain, err := addChain(n.conn, table, ch)
		if err != nil {
			return NftRuleset{}, err
		}
		for _, r := range ch.Rules {
			exprs, err := ruleExprs(r, sets)
			if err != nil {
				return NftRuleset{}, fmt.Errorf("цепочка %s: %w", ch.Name, err)
			}
			n.conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: exprs})
		}
	}

	if err := n.conn.Flush(); err != nil {
		return NftRuleset{}, fmt.Errorf("применение nft-правил: %w", err)
	}
	return rs, nil
}

// Clear снимает таблицу целиком. Отсутствие таблицы ошибкой не считается:
// снятие вызывается и при остановке демона, и по --reset-network.
func (n *Nft) Clear() error {
	existing, err := n.existingTable()
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	n.conn.DelTable(existing)
	if err := n.conn.Flush(); err != nil {
		return fmt.Errorf("снятие nft-таблицы: %w", err)
	}
	return nil
}

// existingTable ищет нашу таблицу среди уже существующих. Удалять её вслепую
// нельзя: в одном пакете изменений удаление отсутствующей таблицы отменяет
// весь пакет, включая создание новой.
func (n *Nft) existingTable() (*nftables.Table, error) {
	tables, err := n.conn.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return nil, fmt.Errorf("список nft-таблиц: %w", err)
	}
	for _, t := range tables {
		if t.Name == NftTable {
			return t, nil
		}
	}
	return nil, nil
}

// addChain переводит цепочку набора в объект nftables.
func addChain(conn nftConn, table *nftables.Table, ch NftChain) (*nftables.Chain, error) {
	var hook *nftables.ChainHook
	switch ch.Hook {
	case "prerouting":
		hook = nftables.ChainHookPrerouting
	case "forward":
		hook = nftables.ChainHookForward
	case "postrouting":
		hook = nftables.ChainHookPostrouting
	default:
		return nil, fmt.Errorf("неизвестный хук %q цепочки %s", ch.Hook, ch.Name)
	}

	chainType := nftables.ChainTypeFilter
	if ch.Type == "nat" {
		chainType = nftables.ChainTypeNAT
	}

	policy := nftables.ChainPolicyAccept
	return conn.AddChain(&nftables.Chain{
		Table:    table,
		Name:     ch.Name,
		Type:     chainType,
		Hooknum:  hook,
		Priority: nftables.ChainPriorityRef(nftables.ChainPriority(ch.Priority)),
		Policy:   &policy,
	}), nil
}

// setElements переводит интервалы в элементы сета.
//
// Диапазон задаётся парой элементов — началом и маркером конца с флагом
// IntervalEnd, — а не полем KeyEnd. KeyEnd проверен на живом ядре 6.12 и
// отвергается с EINVAL, тогда как маркеры принимаются: ядро хранит интервальный
// сет как отсортированный список границ, и второй элемент закрывает диапазон.
//
// Маркер ставится на адрес **следующий** за концом: граница исключающая.
// Для диапазона, упирающегося в 255.255.255.255, маркер не ставится — интервал
// остаётся открытым до верха, что и требуется.
func setElements(ranges []netipx.IPRange) []nftables.SetElement {
	out := make([]nftables.SetElement, 0, len(ranges)*2)
	for _, r := range ranges {
		from := r.From().As4()
		out = append(out, nftables.SetElement{Key: from[:]})

		next := r.To().Next()
		if !next.IsValid() || !next.Is4() {
			continue
		}
		end := next.As4()
		out = append(out, nftables.SetElement{Key: end[:], IntervalEnd: true})
	}
	return out
}

// ruleExprs переводит правило предметной области в выражения nftables.
// Порядок важен только в одном месте: доступ к заголовку транспортного уровня
// идёт после проверки l4proto.
func ruleExprs(r NftRule, sets map[string]*nftables.Set) ([]expr.Any, error) {
	var out []expr.Any

	if r.IIf != "" {
		op := expr.CmpOpEq
		if r.IIfNeq {
			op = expr.CmpOpNeq
		}
		out = append(out,
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{Op: op, Register: 1, Data: ifname(r.IIf)},
		)
	}
	if r.OIf != "" {
		out = append(out,
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname(r.OIf)},
		)
	}
	if r.NFProto != "" {
		proto := byte(unix.NFPROTO_IPV4)
		if r.NFProto == "ipv6" {
			proto = byte(unix.NFPROTO_IPV6)
		}
		out = append(out,
			&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{proto}},
		)
	}
	if r.MarkMatch != 0 {
		mask := binaryutil.NativeEndian.PutUint32(r.MarkMatch)
		out = append(out,
			&expr.Meta{Key: expr.MetaKeyMARK, Register: 1},
			&expr.Bitwise{
				SourceRegister: 1, DestRegister: 1, Len: 4,
				Mask: mask, Xor: []byte{0, 0, 0, 0},
			},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: mask},
		)
	}
	if r.L4Proto != "" {
		proto := byte(unix.IPPROTO_TCP)
		if r.L4Proto == "udp" {
			proto = byte(unix.IPPROTO_UDP)
		}
		out = append(out,
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{proto}},
		)
	}
	if r.DstPrefix.IsValid() {
		out = append(out, dstMatch(r.DstPrefix)...)
	}
	if r.DstSet != "" {
		set, ok := sets[r.DstSet]
		if !ok {
			return nil, fmt.Errorf("правило ссылается на неизвестный сет %s", r.DstSet)
		}
		out = append(out,
			&expr.Payload{
				DestRegister: 1, Base: expr.PayloadBaseNetworkHeader,
				Offset: 16, Len: 4,
			},
			&expr.Lookup{SourceRegister: 1, SetName: set.Name, SetID: set.ID},
		)
	}
	if r.TCPSyn {
		// tcp flags syn / syn,rst — только начало соединения.
		out = append(out,
			&expr.Payload{
				DestRegister: 1, Base: expr.PayloadBaseTransportHeader,
				Offset: 13, Len: 1,
			},
			&expr.Bitwise{
				SourceRegister: 1, DestRegister: 1, Len: 1,
				Mask: []byte{0x06}, Xor: []byte{0x00},
			},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{0x02}},
		)
	}

	action, err := actionExprs(r.Action)
	if err != nil {
		return nil, err
	}
	return append(out, action...), nil
}

// dstMatch — совпадение по адресу назначения IPv4.
func dstMatch(p netip.Prefix) []expr.Any {
	addr := p.Masked().Addr().As4()
	load := &expr.Payload{
		DestRegister: 1, Base: expr.PayloadBaseNetworkHeader,
		Offset: 16, Len: 4,
	}
	if p.Bits() == 32 {
		return []expr.Any{load, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: addr[:]}}
	}
	mask := netip.PrefixFrom(p.Addr(), p.Bits())
	m := prefixMask(mask.Bits())
	return []expr.Any{
		load,
		&expr.Bitwise{
			SourceRegister: 1, DestRegister: 1, Len: 4,
			Mask: m, Xor: []byte{0, 0, 0, 0},
		},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: addr[:]},
	}
}

// prefixMask — маска подсети четырьмя байтами в сетевом порядке.
func prefixMask(bits int) []byte {
	var v uint32
	if bits > 0 {
		v = ^uint32(0) << (32 - bits)
	}
	return binaryutil.BigEndian.PutUint32(v)
}

// actionExprs — выражения действия правила.
func actionExprs(a NftAction) ([]expr.Any, error) {
	switch a {
	case ActionReturn:
		return []expr.Any{&expr.Verdict{Kind: expr.VerdictReturn}}, nil

	case ActionMark:
		return []expr.Any{
			&expr.Immediate{Register: 1, Data: binaryutil.NativeEndian.PutUint32(FwMark)},
			&expr.Meta{Key: expr.MetaKeyMARK, SourceRegister: true, Register: 1},
		}, nil

	case ActionTProxy:
		addr := netip.MustParseAddr(TProxyAddr).As4()
		return []expr.Any{
			&expr.Immediate{Register: 1, Data: addr[:]},
			&expr.Immediate{Register: 2, Data: binaryutil.BigEndian.PutUint16(TProxyPort)},
			&expr.TProxy{
				Family:      byte(unix.NFPROTO_IPV4),
				TableFamily: byte(unix.NFPROTO_INET),
				RegAddr:     1,
				RegPort:     2,
			},
			&expr.Verdict{Kind: expr.VerdictAccept},
		}, nil

	case ActionMasquerade:
		return []expr.Any{&expr.Masq{}}, nil

	case ActionClampMSS:
		// size set rt mtu: MSS берётся из MTU маршрута и пишется в опцию.
		return []expr.Any{
			&expr.Rt{Register: 1, Key: expr.RtTCPMSS},
			&expr.Byteorder{
				SourceRegister: 1, DestRegister: 1,
				Op: expr.ByteorderHton, Len: 2, Size: 2,
			},
			&expr.Exthdr{
				SourceRegister: 1,
				Type:           2, // TCPOPT_MAXSEG
				Offset:         2,
				Len:            2,
				Op:             expr.ExthdrOpTcpopt,
			},
		}, nil

	case ActionRejectICMPv6:
		return []expr.Any{&expr.Reject{
			Type: unix.NFT_REJECT_ICMP_UNREACH,
			Code: icmpv6AdminProhibited,
		}}, nil
	}
	return nil, fmt.Errorf("неизвестное действие правила %q", a)
}

// ifname — имя интерфейса так, как его сравнивает ядро: строка с нулём.
func ifname(name string) []byte {
	return append([]byte(name), 0)
}
