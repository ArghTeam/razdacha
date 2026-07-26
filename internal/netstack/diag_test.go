package netstack

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// TestDiagWGState — состояние собирается из двух источников: netlink отдаёт
// факт, флаг и MTU, wgctrl — порт и число пиров.
func TestDiagWGState(t *testing.T) {
	link := &fakeWGLink{state: DiagLinkState{Exists: true, Up: true, MTU: 1280, Type: "wireguard"}}
	device := &fakeWGDevice{dev: wgtypes.Device{
		ListenPort: 51820,
		Peers:      []wgtypes.Peer{{}, {}},
	}}
	m := newWGManager(WGConfig{
		Address:    netip.MustParsePrefix("10.8.0.1/24"),
		ListenPort: 51820,
		MTU:        1280,
	}, &fakeWGKeys{}, link, device, nil)

	got, err := m.DiagState(context.Background())
	if err != nil {
		t.Fatalf("DiagState: %v", err)
	}
	want := DiagWGState{Name: "wg0", Exists: true, Up: true, MTU: 1280, ListenPort: 51820, Peers: 2}
	if got != want {
		t.Errorf("состояние %+v, ожидалось %+v", got, want)
	}
}

// TestDiagWGStateNoLink — интерфейса нет: это диагноз, а не ошибка чтения,
// иначе «интерфейс не поднят» показывается как «проверить нечем».
func TestDiagWGStateNoLink(t *testing.T) {
	m := newWGManager(WGConfig{}, &fakeWGKeys{}, &fakeWGLink{}, &fakeWGDevice{}, nil)

	got, err := m.DiagState(context.Background())
	if err != nil {
		t.Fatalf("DiagState: %v", err)
	}
	if got.Exists || got.Up {
		t.Errorf("состояние %+v, ожидалось отсутствие интерфейса", got)
	}
}

// TestDiagWGStateForeignLink — интерфейс занят под другой тип: молча считать
// его нашим нельзя, MTU и порт там чужие.
func TestDiagWGStateForeignLink(t *testing.T) {
	link := &fakeWGLink{state: DiagLinkState{Exists: true, Up: true, MTU: 1500, Type: "dummy"}}
	m := newWGManager(WGConfig{}, &fakeWGKeys{}, link, &fakeWGDevice{}, nil)

	if _, err := m.DiagState(context.Background()); !errors.Is(err, ErrWGForeignLink) {
		t.Errorf("ошибка %v, ожидалась ErrWGForeignLink", err)
	}
}

// TestDiagWGStateLinkError — сбой netlink доходит до вызывающего, а не
// превращается в «интерфейса нет».
func TestDiagWGStateLinkError(t *testing.T) {
	boom := errors.New("netlink недоступен")
	m := newWGManager(WGConfig{}, &fakeWGKeys{}, &fakeWGLink{stateErr: boom}, &fakeWGDevice{}, nil)

	if _, err := m.DiagState(context.Background()); !errors.Is(err, boom) {
		t.Errorf("ошибка %v, ожидалась %v", err, boom)
	}
}

// TestDiagIPForward — разбор значения sysctl без procfs.
func TestDiagIPForward(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		body    string
		want    bool
		wantErr bool
	}{
		{name: "включён", body: "1\n", want: true},
		{name: "выключен", body: "0\n", want: false},
		{name: "мусор", body: "yes\n", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("запись файла: %v", err)
			}
			got, err := diagIPForward(path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("значение %q принято без ошибки", tc.body)
				}
				return
			}
			if err != nil {
				t.Fatalf("diagIPForward: %v", err)
			}
			if got != tc.want {
				t.Errorf("%q разобрано как %v", tc.body, got)
			}
		})
	}
}

// TestDiagIPForwardMissing — файла нет: ошибка называет путь, чтобы «unknown»
// в панели было объяснимым.
func TestDiagIPForwardMissing(t *testing.T) {
	_, err := diagIPForward(filepath.Join(t.TempDir(), "нет-такого"))
	if err == nil {
		t.Fatal("отсутствие файла принято без ошибки")
	}
}

// TestDiagMissing — недостача считается по тем же константам, из которых
// строится набор правил.
func TestDiagMissing(t *testing.T) {
	full := DiagNftState{
		Chains: []string{ChainMangle, ChainProxy, ChainForward, ChainPostrouting},
		Sets:   []string{SetLocalV4, SetSubnets},
	}
	if chains, sets := full.DiagMissing(); len(chains) > 0 || len(sets) > 0 {
		t.Errorf("полный набор объявлен неполным: цепочки %v, сеты %v", chains, sets)
	}

	partial := DiagNftState{Chains: []string{ChainMangle}, Sets: []string{SetLocalV4}}
	chains, sets := partial.DiagMissing()
	if len(chains) != 3 || len(sets) != 1 {
		t.Errorf("недостача %v / %v, ожидались три цепочки и один сет", chains, sets)
	}
}
