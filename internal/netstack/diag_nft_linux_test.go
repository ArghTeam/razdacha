package netstack

import (
	"errors"
	"net/netip"
	"slices"
	"sort"
	"testing"

	"github.com/google/nftables"
)

// diagFakeNft заливает набор правил в подменённое соединение и объявляет
// таблицу существующей: дальше чтение видит ровно то, что записала заливка.
func diagFakeNft(t *testing.T, cfg NftConfig) *Nft {
	t.Helper()
	f := newFakeNftConn()
	n := &Nft{conn: f}
	if _, err := n.Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	f.tables = append(f.tables, &nftables.Table{
		Family: nftables.TableFamilyINet, Name: NftTable,
	})
	return n
}

// TestDiagStateFullTable — на залитой таблице проверка видит все цепочки, оба
// сета, masquerade с интерфейсом и число подсетей.
func TestDiagStateFullTable(t *testing.T) {
	n := diagFakeNft(t, NftConfig{
		WANInterface: "eth0",
		Subnets:      []string{"203.0.113.0/24", "198.51.100.7"},
	})

	st, err := n.DiagState()
	if err != nil {
		t.Fatalf("DiagState: %v", err)
	}
	if !st.Exists {
		t.Fatal("таблица объявлена отсутствующей")
	}
	if chains, sets := st.DiagMissing(); len(chains) > 0 || len(sets) > 0 {
		t.Errorf("недостача на полной таблице: цепочки %v, сеты %v", chains, sets)
	}
	if !st.Masquerade || st.MasqueradeOIf != "eth0" {
		t.Errorf("masquerade=%v на %q, ожидался eth0", st.Masquerade, st.MasqueradeOIf)
	}
	if len(st.Subnets) != 2 {
		t.Fatalf("интервалов %d, ожидалось 2: маркеры конца — границы, а не элементы",
			len(st.Subnets))
	}
	// Интервалы читаются содержимым: сверять сет с правилами по числу
	// бесполезно (issue #123).
	want := []string{"203.0.113.0-203.0.113.255", "198.51.100.7-198.51.100.7"}
	var got []string
	for _, r := range st.Subnets {
		got = append(got, r.String())
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("интервалы сета %v, ожидались %v", got, want)
	}
	if missing := st.SubnetIndex().Missing([]string{
		"203.0.113.128/25", "198.51.100.7", "192.0.2.0/24", "2001:db8::/32",
	}); !slices.Equal(missing, []string{"192.0.2.0/24"}) {
		t.Errorf("недостача %v, ожидалась только 192.0.2.0/24: покрытие считается "+
			"по вхождению, IPv6 в сет не заливается", missing)
	}
}

// diagKey4 — ключ элемента сета: четыре байта адреса, как их отдаёт ядро.
func diagKey4(t *testing.T, s string) []byte {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() {
		t.Fatalf("адрес %q не разобран: %v", s, err)
	}
	b := a.As4()
	return b[:]
}

// TestDiagRangesFromKernel — разбор сета в том виде, в каком его отдаёт живое
// ядро, а не в том, в каком его писала наша же заливка.
//
// Роундтрип через подменённое соединение проверял симметрию собственной записи
// и был зелёным при сломанном разборе: ядро 6.12 отдаёт пары «конец, начало»
// (issue #123). Разбор по порядку склеивал соседние интервалы в один, сводка
// показывала «подсетей в сете 1», а проверка покрытия проходила всегда — то
// есть задача считалась сделанной ровно тогда, когда не работала.
//
// Здесь элементы заданы вручную: пары в обратном порядке, сами пары вразнобой,
// плюс интервал одним элементом через KeyEnd и незакрытый хвост.
func TestDiagRangesFromKernel(t *testing.T) {
	elements := []nftables.SetElement{
		// 31.13.24.0/21 — маркер конца приходит раньше начала.
		{Key: diagKey4(t, "31.13.32.0"), IntervalEnd: true},
		{Key: diagKey4(t, "31.13.24.0")},
		// 5.28.192.0/18 — пара идёт после пары со старшим адресом.
		{Key: diagKey4(t, "5.29.0.0"), IntervalEnd: true},
		{Key: diagKey4(t, "5.28.192.0")},
		// Диапазон, не выражаемый префиксом.
		{Key: diagKey4(t, "57.141.7.0"), IntervalEnd: true},
		{Key: diagKey4(t, "57.141.2.0")},
		// Один адрес.
		{Key: diagKey4(t, "198.51.100.8"), IntervalEnd: true},
		{Key: diagKey4(t, "198.51.100.7")},
		// Интервал одним элементом: граница в KeyEnd, конец включающий.
		{Key: diagKey4(t, "203.0.113.0"), KeyEnd: diagKey4(t, "203.0.113.255")},
		// Хвост до верха адресного пространства: маркера у него нет.
		{Key: diagKey4(t, "250.0.0.0")},
	}

	var got []string
	for _, r := range diagRanges(elements) {
		got = append(got, r.String())
	}
	want := []string{
		"5.28.192.0-5.28.255.255",
		"31.13.24.0-31.13.31.255",
		"57.141.2.0-57.141.6.255",
		"198.51.100.7-198.51.100.7",
		"203.0.113.0-203.0.113.255",
		"250.0.0.0-255.255.255.255",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("интервалы сета:\n  получено %v\n  ожидалось %v", got, want)
	}

	// Непокрытая подсеть — единственная причина, по которой проверка заводилась.
	index := DiagNftState{Subnets: diagRanges(elements)}.SubnetIndex()
	if missing := index.Missing([]string{
		"5.28.192.0/18", "57.141.3.0/24", "192.0.2.0/24", "57.141.6.0/23",
	}); !slices.Equal(missing, []string{"192.0.2.0/24", "57.141.6.0/23"}) {
		t.Errorf("недостача %v, ожидались 192.0.2.0/24 (сета нет вовсе) и "+
			"57.141.6.0/23 (покрыта лишь наполовину)", missing)
	}
}

// TestDiagStateNoTable — таблицы нет: это диагноз, а не ошибка чтения.
func TestDiagStateNoTable(t *testing.T) {
	n := &Nft{conn: newFakeNftConn()}

	st, err := n.DiagState()
	if err != nil {
		t.Fatalf("DiagState: %v", err)
	}
	if st.Exists {
		t.Error("таблица найдена там, где её нет")
	}
	if st.Table != NftTable {
		t.Errorf("имя таблицы %q, ожидалось %q", st.Table, NftTable)
	}
}

// TestDiagStateReadError — сбой netlink доходит до вызывающего: показывать его
// как «правил нет» нельзя, это разные диагнозы.
func TestDiagStateReadError(t *testing.T) {
	f := newFakeNftConn(NftTable)
	f.readErr = errors.New("netlink недоступен")

	if _, err := (&Nft{conn: f}).DiagState(); err == nil {
		t.Fatal("сбой чтения принят за состояние")
	}
}
