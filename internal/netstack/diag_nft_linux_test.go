package netstack

import (
	"errors"
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
	if st.SubnetIntervals != 2 {
		t.Errorf("подсетей %d, ожидалось 2: маркеры конца интервала не считаются",
			st.SubnetIntervals)
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
