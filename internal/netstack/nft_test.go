//go:build linux

package netstack

import (
	"errors"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

// fakeNftConn подменяет netlink-соединение: записывает пакет изменений, но
// никуда его не отправляет. Так состав таблицы и атомарность заливки
// проверяются без root и без ядра.
type fakeNftConn struct {
	tables   []*nftables.Table
	ops      []string
	chains   []*nftables.Chain
	sets     map[string][]nftables.SetElement
	setList  []*nftables.Set
	rules    []*nftables.Rule
	flushes  int
	flushErr error
	listErr  error
	readErr  error
}

func newFakeNftConn(existing ...string) *fakeNftConn {
	f := &fakeNftConn{sets: map[string][]nftables.SetElement{}}
	for _, name := range existing {
		f.tables = append(f.tables, &nftables.Table{
			Family: nftables.TableFamilyINet, Name: name,
		})
	}
	return f
}

func (f *fakeNftConn) ListTablesOfFamily(nftables.TableFamily) ([]*nftables.Table, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.tables, nil
}

func (f *fakeNftConn) DelTable(t *nftables.Table) {
	f.ops = append(f.ops, "del-table:"+t.Name)
}

func (f *fakeNftConn) AddTable(t *nftables.Table) *nftables.Table {
	f.ops = append(f.ops, "add-table:"+t.Name)
	return t
}

func (f *fakeNftConn) AddChain(c *nftables.Chain) *nftables.Chain {
	f.ops = append(f.ops, "add-chain:"+c.Name)
	f.chains = append(f.chains, c)
	return c
}

func (f *fakeNftConn) AddSet(s *nftables.Set, vals []nftables.SetElement) error {
	f.ops = append(f.ops, "add-set:"+s.Name)
	f.sets[s.Name] = vals
	f.setList = append(f.setList, s)
	return nil
}

func (f *fakeNftConn) AddRule(r *nftables.Rule) *nftables.Rule {
	f.ops = append(f.ops, "add-rule:"+r.Chain.Name)
	f.rules = append(f.rules, r)
	return r
}

// Чтение состояния идёт по тому же, что записала заливка: так диагностика
// проверяется на настоящем составе таблицы, а не на выдуманном.

func (f *fakeNftConn) ListChainsOfTableFamily(nftables.TableFamily) ([]*nftables.Chain, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.chains, nil
}

func (f *fakeNftConn) GetSets(*nftables.Table) ([]*nftables.Set, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.setList, nil
}

func (f *fakeNftConn) GetSetElements(s *nftables.Set) ([]nftables.SetElement, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.sets[s.Name], nil
}

func (f *fakeNftConn) GetRules(_ *nftables.Table, c *nftables.Chain) ([]*nftables.Rule, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	var out []*nftables.Rule
	for _, r := range f.rules {
		if r.Chain != nil && r.Chain.Name == c.Name {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeNftConn) Flush() error {
	f.flushes++
	return f.flushErr
}

func applyFake(t *testing.T, f *fakeNftConn, cfg NftConfig) NftRuleset {
	t.Helper()
	n := &Nft{conn: f}
	rs, err := n.Apply(cfg)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return rs
}

// Всё изменение уходит одним пакетом: единственный Flush, и удаление прежней
// таблицы стоит в нём же — окна без правил не возникает.
func TestNftApplyAtomic(t *testing.T) {
	f := newFakeNftConn(NftTable, "filter")
	applyFake(t, f, NftConfig{WANInterface: "eth0"})

	if f.flushes != 1 {
		t.Errorf("Flush вызван %d раз, ожидался один пакет изменений", f.flushes)
	}
	if len(f.ops) < 2 || f.ops[0] != "del-table:"+NftTable || f.ops[1] != "add-table:"+NftTable {
		t.Errorf("начало пакета = %v, ожидались удаление и создание таблицы", f.ops)
	}
}

// Таблицы ещё нет — удалять нечего: в одном пакете удаление отсутствующей
// таблицы отменило бы и создание новой.
func TestNftApplyFirstRun(t *testing.T) {
	f := newFakeNftConn("filter")
	applyFake(t, f, NftConfig{WANInterface: "eth0"})

	for _, op := range f.ops {
		if op == "del-table:"+NftTable {
			t.Fatalf("удаление несуществующей таблицы в пакете: %v", f.ops)
		}
	}
	if f.ops[0] != "add-table:"+NftTable {
		t.Errorf("первый шаг = %s, ожидалось создание таблицы", f.ops[0])
	}
}

func TestNftApplyObjects(t *testing.T) {
	f := newFakeNftConn()
	applyFake(t, f, NftConfig{WANInterface: "eth0", Subnets: []string{"149.154.160.0/20"}})

	if len(f.chains) != 5 {
		t.Fatalf("цепочек %d, ожидалось 5", len(f.chains))
	}
	nat := map[string]bool{ChainDNSNat: true, ChainPostrouting: true}
	for _, c := range f.chains {
		if c.Hooknum == nil || c.Policy == nil {
			t.Errorf("цепочка %s без хука или политики", c.Name)
		}
		want := nftables.ChainTypeFilter
		if nat[c.Name] {
			want = nftables.ChainTypeNAT
		}
		if c.Type != want {
			t.Errorf("цепочка %s типа %s, ожидался %s", c.Name, c.Type, want)
		}
	}

	if _, ok := f.sets[SetLocalV4]; !ok {
		t.Errorf("сета %s нет: %v", SetLocalV4, f.sets)
	}
	sub, ok := f.sets[SetSubnets]
	if !ok {
		t.Fatalf("сета %s нет: %v", SetSubnets, f.sets)
	}
	// Интервал задаётся парой элементов: начало и маркер конца с IntervalEnd.
	// Поле KeyEnd живое ядро отвергает с EINVAL — проверено на Debian 13 (6.12).
	if len(sub) != 2 {
		t.Fatalf("элементов %s = %d, ожидались начало и маркер конца", SetSubnets, len(sub))
	}
	if got := sub[0].Key; len(got) != 4 || got[0] != 149 || got[3] != 0 {
		t.Errorf("начало интервала = %v", got)
	}
	if sub[0].IntervalEnd {
		t.Error("первый элемент помечен как конец интервала")
	}
	// Маркер ставится на адрес, следующий за концом: 149.154.175.255 + 1.
	if got := sub[1].Key; len(got) != 4 || got[2] != 176 || got[3] != 0 {
		t.Errorf("маркер конца = %v", got)
	}
	if !sub[1].IntervalEnd {
		t.Error("маркер конца не помечен IntervalEnd")
	}
}

func TestNftApplyExprs(t *testing.T) {
	f := newFakeNftConn()
	applyFake(t, f, NftConfig{WANInterface: "eth0"})

	var tproxy, reject, masq, lookups, dnat int
	for _, r := range f.rules {
		for _, e := range r.Exprs {
			switch v := e.(type) {
			case *expr.TProxy:
				tproxy++
				if v.RegPort == 0 {
					t.Errorf("tproxy без порта в регистре")
				}
			case *expr.NAT:
				dnat++
				if v.Type != expr.NATTypeDestNAT || v.RegAddrMin == 0 || v.RegProtoMin == 0 {
					t.Errorf("dnat без адреса или порта в регистрах: %+v", v)
				}
			case *expr.Reject:
				reject++
				if v.Code != icmpv6AdminProhibited && v.Code != icmpAdminProhibited {
					t.Errorf("reject с кодом %d, ожидался admin-prohibited", v.Code)
				}
			case *expr.Masq:
				masq++
			case *expr.Lookup:
				lookups++
				if v.SetName != SetLocalV4 && v.SetName != SetSubnets {
					t.Errorf("правило смотрит в чужой сет %s", v.SetName)
				}
			}
		}
	}

	if tproxy != 2 {
		t.Errorf("правил tproxy %d, ожидалось два (tcp и udp)", tproxy)
	}
	if reject != 3 {
		t.Errorf("правил reject %d, ожидалось три (IPv6 клиентов и 853 tcp/udp)", reject)
	}
	// Перехват DNS: по одному DNAT на tcp и udp (issue #122).
	if dnat != 2 {
		t.Errorf("правил dnat %d, ожидалось два (tcp и udp)", dnat)
	}
	if masq != 1 {
		t.Errorf("правил masquerade %d, ожидалось одно", masq)
	}
	if lookups != 2 {
		t.Errorf("обращений к сетам %d, ожидалось два", lookups)
	}
}

func TestNftApplyBuildError(t *testing.T) {
	f := newFakeNftConn()
	n := &Nft{conn: f}

	if _, err := n.Apply(NftConfig{}); !errors.Is(err, ErrNftNoWAN) {
		t.Errorf("ошибка = %v, ожидалось ErrNftNoWAN", err)
	}
	if f.flushes != 0 || len(f.ops) != 0 {
		t.Errorf("при ошибке сборки в ядро ушло %v", f.ops)
	}
}

func TestNftClear(t *testing.T) {
	f := newFakeNftConn(NftTable)
	n := &Nft{conn: f}

	if err := n.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if len(f.ops) != 1 || f.ops[0] != "del-table:"+NftTable {
		t.Errorf("пакет снятия = %v, ожидалось только удаление таблицы", f.ops)
	}

	// Таблицы нет — снятие не ошибка и в ядро ничего не отправляет.
	empty := newFakeNftConn()
	n2 := &Nft{conn: empty}
	if err := n2.Clear(); err != nil {
		t.Fatalf("Clear на чистой системе: %v", err)
	}
	if empty.flushes != 0 {
		t.Errorf("Flush вызван %d раз, ожидалось 0", empty.flushes)
	}
}
