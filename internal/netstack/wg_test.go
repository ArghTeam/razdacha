package netstack

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/ArghTeam/razdacha/internal/store"
)

// fakeWGLink — интерфейс без netlink и без root: записывает, что от него хотели.
type fakeWGLink struct {
	mtu     int
	addr    netip.Prefix
	up      bool
	deleted bool
	ensured int
}

func (f *fakeWGLink) Ensure(_ string, mtu int) error {
	f.mtu = mtu
	f.ensured++
	return nil
}

func (f *fakeWGLink) EnsureAddr(_ string, addr netip.Prefix) error {
	f.addr = addr
	return nil
}

func (f *fakeWGLink) Up(string) error { f.up = true; return nil }

func (f *fakeWGLink) Delete(string) error { f.deleted = true; return nil }

// fakeWGDevice — устройство WireGuard в памяти. Применяет к себе то, что ему
// записали, чтобы повторная синхронизация видела уже применённое состояние.
type fakeWGDevice struct {
	dev    wgtypes.Device
	writes []wgtypes.Config
}

func (f *fakeWGDevice) Device(string) (*wgtypes.Device, error) {
	out := f.dev
	return &out, nil
}

func (f *fakeWGDevice) ConfigureDevice(_ string, cfg wgtypes.Config) error {
	f.writes = append(f.writes, cfg)
	if cfg.PrivateKey != nil {
		f.dev.PrivateKey = *cfg.PrivateKey
		f.dev.PublicKey = cfg.PrivateKey.PublicKey()
	}
	if cfg.ListenPort != nil {
		f.dev.ListenPort = *cfg.ListenPort
	}
	for _, p := range cfg.Peers {
		f.applyPeer(p)
	}
	return nil
}

func (f *fakeWGDevice) applyPeer(p wgtypes.PeerConfig) {
	kept := make([]wgtypes.Peer, 0, len(f.dev.Peers)+1)
	for _, have := range f.dev.Peers {
		if have.PublicKey != p.PublicKey {
			kept = append(kept, have)
		}
	}
	f.dev.Peers = kept
	if p.Remove {
		return
	}
	added := wgtypes.Peer{PublicKey: p.PublicKey, AllowedIPs: p.AllowedIPs}
	if p.PresharedKey != nil {
		added.PresharedKey = *p.PresharedKey
	}
	f.dev.Peers = append(f.dev.Peers, added)
}

func (f *fakeWGDevice) Close() error { return nil }

// fakeWGKeys — хранилище ключа сервера в памяти.
type fakeWGKeys struct {
	key   string
	saved int
}

func (f *fakeWGKeys) ServerPrivateKey(context.Context) (string, error) { return f.key, nil }

func (f *fakeWGKeys) SetServerPrivateKey(_ context.Context, key string) error {
	f.key = key
	f.saved++
	return nil
}

func newTestWGManager(t *testing.T) (*WGManager, *fakeWGLink, *fakeWGDevice, *fakeWGKeys) {
	t.Helper()
	cfg, err := WGConfigFromSettings(store.DefaultSettings())
	if err != nil {
		t.Fatalf("WGConfigFromSettings: %v", err)
	}
	link, dev, keys := &fakeWGLink{}, &fakeWGDevice{}, &fakeWGKeys{}
	return newWGManager(cfg, keys, link, dev, nil), link, dev, keys
}

// TestUpAppliesInterfaceParameters — интерфейс поднимается с MTU 1280 (ADR 0004),
// адресом из пула и портом из настроек.
func TestUpAppliesInterfaceParameters(t *testing.T) {
	m, link, dev, keys := newTestWGManager(t)

	if err := m.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if link.mtu != 1280 {
		t.Errorf("MTU интерфейса %d, ADR 0004 требует 1280", link.mtu)
	}
	if got := link.addr.String(); got != "10.8.0.1/24" {
		t.Errorf("адрес %s, ожидался 10.8.0.1/24", got)
	}
	if !link.up {
		t.Error("интерфейс не поднят")
	}
	if dev.dev.ListenPort != 51820 {
		t.Errorf("порт %d, ожидался 51820", dev.dev.ListenPort)
	}
	if keys.key == "" || keys.saved != 1 {
		t.Errorf("ключ сервера не сохранён при первом запуске: %+v", keys)
	}
}

// TestUpIsIdempotent — повторный запуск не переписывает приватный ключ:
// перезапись заставляет всех пиров передоговариваться заново.
func TestUpIsIdempotent(t *testing.T) {
	m, _, dev, keys := newTestWGManager(t)
	ctx := context.Background()

	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	before := len(dev.writes)
	if err := m.Up(ctx); err != nil {
		t.Fatalf("повторный Up: %v", err)
	}
	if len(dev.writes) != before {
		t.Errorf("повторный Up переписал устройство: %+v", dev.writes[before:])
	}
	if keys.saved != 1 {
		t.Errorf("ключ сервера сохранён %d раз, ожидался один", keys.saved)
	}
}

// TestUpRejectsBadMTU — MTU не берётся «какой есть»: за пределами диапазона Up
// отказывает до обращения к netlink.
func TestUpRejectsBadMTU(t *testing.T) {
	s := store.DefaultSettings()
	s.ClientMTU = 9000
	if _, err := WGConfigFromSettings(s); !errors.Is(err, ErrWGConfig) {
		t.Fatalf("ошибка %v, ожидалась %v", err, ErrWGConfig)
	}
}

// TestSyncPeersWritesOnlyChanges — вторая синхронизация того же состояния не
// пишет в устройство ничего: живые хендшейки не должны рваться.
func TestSyncPeersWritesOnlyChanges(t *testing.T) {
	m, _, dev, _ := newTestWGManager(t)
	ctx := context.Background()
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	peers := []store.Peer{
		testPeer(t, "iPhone", "10.8.0.5", true),
		testPeer(t, "MacBook", "10.8.0.6", true),
	}
	changes, err := m.SyncPeers(ctx, peers)
	if err != nil {
		t.Fatalf("SyncPeers: %v", err)
	}
	if changes.Added != 2 {
		t.Fatalf("добавлено %d пиров, ожидалось 2", changes.Added)
	}

	writes := len(dev.writes)
	changes, err = m.SyncPeers(ctx, peers)
	if err != nil {
		t.Fatalf("повторный SyncPeers: %v", err)
	}
	if !changes.Empty() || len(dev.writes) != writes {
		t.Errorf("повторная синхронизация переписала пиров: %+v", changes)
	}

	// Выключенный пир снимается с интерфейса, оставаясь в БД.
	peers[0].Enabled = false
	changes, err = m.SyncPeers(ctx, peers)
	if err != nil {
		t.Fatalf("SyncPeers после выключения: %v", err)
	}
	if changes.Removed != 1 || len(dev.dev.Peers) != 1 {
		t.Errorf("выключенный пир остался на интерфейсе: %+v", changes)
	}
}

// TestStatsReadsCounters — статистика доезжает до вызывающего в том же виде
// ключа, в каком он лежит в БД.
func TestStatsReadsCounters(t *testing.T) {
	m, _, dev, _ := newTestWGManager(t)
	p := testPeer(t, "iPhone", "10.8.0.5", true)
	wire := onWire(t, p)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	wire.LastHandshakeTime = now.Add(-time.Minute)
	wire.ReceiveBytes = 4096
	wire.TransmitBytes = 2048
	wire.Endpoint = &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 44100}
	dev.dev.Peers = []wgtypes.Peer{wire}

	stats, err := m.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	st, ok := stats[p.PublicKey]
	if !ok {
		t.Fatalf("статистики пира нет: %+v", stats)
	}
	if st.RxBytes != 4096 || st.TxBytes != 2048 {
		t.Errorf("счётчики %d/%d, ожидались 4096/2048", st.RxBytes, st.TxBytes)
	}
	if st.Endpoint != "203.0.113.7:44100" {
		t.Errorf("endpoint %q", st.Endpoint)
	}
	if !st.Online(now) {
		t.Error("пир с хендшейком минуту назад считается офлайн")
	}
	if st.Online(now.Add(time.Hour)) {
		t.Error("пир с хендшейком час назад считается онлайн")
	}
}

// TestDownRemovesInterface — снятие интерфейса доходит до netlink.
func TestDownRemovesInterface(t *testing.T) {
	m, link, _, _ := newTestWGManager(t)
	if err := m.Down(context.Background()); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if !link.deleted {
		t.Error("интерфейс не снят")
	}
}

// TestPublicKeyBeforeUp — ключа ещё нет: API обязан отдать null, а не выдуманное
// значение, и генерации по чтению не происходит.
func TestPublicKeyBeforeUp(t *testing.T) {
	m, _, _, keys := newTestWGManager(t)
	ctx := context.Background()

	key, err := m.PublicKey(ctx)
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if key != "" || keys.saved != 0 {
		t.Fatalf("до поднятия интерфейса выдан ключ %q (сохранений %d)", key, keys.saved)
	}

	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	key, err = m.PublicKey(ctx)
	if err != nil {
		t.Fatalf("PublicKey после Up: %v", err)
	}
	priv, err := wgtypes.ParseKey(keys.key)
	if err != nil {
		t.Fatalf("сохранённый ключ не разобран: %v", err)
	}
	if key != priv.PublicKey().String() {
		t.Errorf("публичный ключ %q не соответствует приватному", key)
	}
}
