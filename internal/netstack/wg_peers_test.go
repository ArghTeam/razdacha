package netstack

import (
	"net"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/ArghTeam/razdacha/internal/store"
)

// testPeer собирает запись БД с настоящими ключами: диффу они нужны разобранными.
func testPeer(t *testing.T, name, addr string, enabled bool) store.Peer {
	t.Helper()
	priv, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("генерация ключа: %v", err)
	}
	psk, err := wgtypes.GenerateKey()
	if err != nil {
		t.Fatalf("генерация pre-shared ключа: %v", err)
	}
	return store.Peer{
		ID:           name,
		Name:         name,
		PublicKey:    priv.PublicKey().String(),
		PrivateKey:   priv.String(),
		PresharedKey: psk.String(),
		Address:      addr,
		Enabled:      enabled,
	}
}

// onWire — как пир выглядит в дампе устройства.
func onWire(t *testing.T, p store.Peer) wgtypes.Peer {
	t.Helper()
	pub, err := wgtypes.ParseKey(p.PublicKey)
	if err != nil {
		t.Fatalf("публичный ключ: %v", err)
	}
	psk, err := wgtypes.ParseKey(p.PresharedKey)
	if err != nil {
		t.Fatalf("pre-shared ключ: %v", err)
	}
	return wgtypes.Peer{
		PublicKey:    pub,
		PresharedKey: psk,
		AllowedIPs: []net.IPNet{{
			IP:   net.ParseIP(p.Address).To4(),
			Mask: net.CIDRMask(32, 32),
		}},
	}
}

func desired(t *testing.T, peers ...store.Peer) []wgDesiredPeer {
	t.Helper()
	out, err := wgDesiredPeers(peers)
	if err != nil {
		t.Fatalf("разбор пиров: %v", err)
	}
	return out
}

// TestDiffAddsMissingPeer — пира из БД нет на интерфейсе.
func TestDiffAddsMissingPeer(t *testing.T) {
	p := testPeer(t, "iPhone", "10.8.0.5", true)

	got := wgDiffPeers(desired(t, p), nil)
	if got.Added != 1 || got.Updated != 0 || got.Removed != 0 {
		t.Fatalf("дифф %+v, ожидалось добавление одного пира", got)
	}
	cfg := got.Configs[0]
	if cfg.UpdateOnly {
		t.Error("новый пир заведён с UpdateOnly — он не появится на интерфейсе")
	}
	if cfg.PresharedKey == nil || cfg.PresharedKey.String() != p.PresharedKey {
		t.Error("pre-shared ключ не попал в конфигурацию пира")
	}
	if !cfg.ReplaceAllowedIPs || len(cfg.AllowedIPs) != 1 ||
		cfg.AllowedIPs[0].String() != p.Address+"/32" {
		t.Errorf("AllowedIPs = %v, ожидался ровно %s/32", cfg.AllowedIPs, p.Address)
	}
}

// TestDiffIsEmptyWithoutChanges — повторная синхронизация неизменного состояния
// не пишет ничего: любая запись сбрасывает сессию живого пира.
func TestDiffIsEmptyWithoutChanges(t *testing.T) {
	a := testPeer(t, "iPhone", "10.8.0.5", true)
	b := testPeer(t, "MacBook", "10.8.0.6", true)

	got := wgDiffPeers(desired(t, a, b), []wgtypes.Peer{onWire(t, b), onWire(t, a)})
	if !got.Empty() {
		t.Fatalf("дифф %+v, ожидалось пусто: состояние не менялось", got)
	}
}

// TestDiffUpdatesChangedPeer — у пира сменился адрес.
func TestDiffUpdatesChangedPeer(t *testing.T) {
	p := testPeer(t, "iPhone", "10.8.0.5", true)
	stale := onWire(t, p)
	stale.AllowedIPs = []net.IPNet{{IP: net.ParseIP("10.8.0.9").To4(), Mask: net.CIDRMask(32, 32)}}

	got := wgDiffPeers(desired(t, p), []wgtypes.Peer{stale})
	if got.Updated != 1 || got.Added != 0 || got.Removed != 0 {
		t.Fatalf("дифф %+v, ожидалось изменение одного пира", got)
	}
	if !got.Configs[0].UpdateOnly {
		t.Error("изменение пира без UpdateOnly: исчезнувший пир завёлся бы заново")
	}
	if got.Configs[0].AllowedIPs[0].String() != "10.8.0.5/32" {
		t.Errorf("AllowedIPs = %v, ожидался новый адрес", got.Configs[0].AllowedIPs)
	}
}

// TestDiffUpdatesChangedPresharedKey — смена pre-shared ключа доезжает до интерфейса.
func TestDiffUpdatesChangedPresharedKey(t *testing.T) {
	p := testPeer(t, "iPhone", "10.8.0.5", true)
	stale := onWire(t, p)
	stale.PresharedKey = wgtypes.Key{}

	got := wgDiffPeers(desired(t, p), []wgtypes.Peer{stale})
	if got.Updated != 1 {
		t.Fatalf("дифф %+v, ожидалось изменение пира", got)
	}
}

// TestDiffRemovesUnknownPeer — пира на интерфейсе нет в БД: его удалили.
func TestDiffRemovesUnknownPeer(t *testing.T) {
	kept := testPeer(t, "iPhone", "10.8.0.5", true)
	gone := testPeer(t, "старый", "10.8.0.6", true)

	got := wgDiffPeers(desired(t, kept), []wgtypes.Peer{onWire(t, kept), onWire(t, gone)})
	if got.Removed != 1 || got.Added != 0 || got.Updated != 0 {
		t.Fatalf("дифф %+v, ожидалось снятие одного пира", got)
	}
	if !got.Configs[0].Remove || got.Configs[0].PublicKey.String() != gone.PublicKey {
		t.Errorf("снят не тот пир: %+v", got.Configs[0])
	}
}

// TestDisabledPeerLeavesInterface — выключенный пир исчезает с wg0, но остаётся
// в БД со всеми ключами: включение не должно требовать нового конфига.
func TestDisabledPeerLeavesInterface(t *testing.T) {
	p := testPeer(t, "iPhone", "10.8.0.5", false)

	set := desired(t, p)
	if len(set) != 0 {
		t.Fatalf("выключенный пир попал в набор для интерфейса: %+v", set)
	}

	got := wgDiffPeers(set, []wgtypes.Peer{onWire(t, p)})
	if got.Removed != 1 || !got.Configs[0].Remove {
		t.Fatalf("дифф %+v, выключенный пир обязан сниматься с интерфейса", got)
	}
	if p.PrivateKey == "" || p.PresharedKey == "" {
		t.Error("у выключенного пира стёрлись ключи")
	}
}

// TestDesiredPeersRejectsBadKey — мусор в БД не уезжает в ядро молча.
func TestDesiredPeersRejectsBadKey(t *testing.T) {
	p := testPeer(t, "iPhone", "10.8.0.5", true)
	p.PublicKey = "не ключ"
	if _, err := wgDesiredPeers([]store.Peer{p}); err == nil {
		t.Fatal("пир с нечитаемым ключом принят")
	}
}
