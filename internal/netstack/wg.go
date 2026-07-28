// Package netstack — слой сетевого состояния razdacha: интерфейс wg0, nft-правила
// и маршрутизация, всё через netlink.
//
// Бинарники `wg`, `wg-quick`, `ip` и `nft` не вызываются: демон обязан работать на
// широкой матрице ОС, где набор и версии этих утилит не совпадают (конституция,
// раздел «Инварианты»).
package netstack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/ArghTeam/razdacha/internal/store"
)

// Ошибки слоя, по которым вызывающий принимает решения. Тексты доходят до UI как есть.
var (
	// ErrWGUnsupported — интерфейс WireGuard на этой ОС не поднимается.
	// Демон целевой платформы — Linux; сборка под остальные существует только
	// ради тестов и локальной разработки.
	ErrWGUnsupported = errors.New("управление интерфейсом WireGuard доступно только на Linux")
	// ErrWGConfig — параметры интерфейса не разобрались из настроек.
	ErrWGConfig = errors.New("некорректные параметры интерфейса WireGuard")
	// ErrWGForeignLink — интерфейс с таким именем есть, но он не WireGuard.
	ErrWGForeignLink = errors.New("интерфейс занят под другой тип")
	// ErrWGNoDevice — устройства WireGuard нет, интерфейс ещё не поднят.
	ErrWGNoDevice = errors.New("интерфейс WireGuard не поднят")
)

// DefaultWGInterface — имя интерфейса клиентов. Одно на демон и не настраивается:
// на него завязаны nft-правила (docs/03-networking.md).
const DefaultWGInterface = "wg0"

// wgLinkTypeName — тип интерфейса, каким его называет ядро.
const wgLinkTypeName = "wireguard"

// wgHandshakeTimeout — после какого молчания пир считается офлайн. Клиенты шлют
// keepalive раз в 25 секунд, поэтому три минуты — заведомо не флап.
const wgHandshakeTimeout = 3 * time.Minute

// WGConfig — параметры интерфейса wg0. Собирается из настроек демона
// [WGConfigFromSettings], руками не заполняется нигде, кроме тестов.
type WGConfig struct {
	// Name — имя интерфейса, пустое означает [DefaultWGInterface].
	Name string
	// Address — адрес сервера внутри туннеля вместе с маской пула, 10.8.0.1/24.
	Address netip.Prefix
	// ListenPort — UDP-порт WireGuard.
	ListenPort int
	// MTU — обязан совпадать с MTU клиентов (ADR 0004).
	MTU int
}

// wgLinkOps — операции над сетевым интерфейсом: netlink и root.
// За интерфейсом они спрятаны, чтобы остальная логика проверялась без привилегий.
type wgLinkOps interface {
	// Ensure создаёт интерфейс типа wireguard, если его нет, и выставляет MTU.
	Ensure(name string, mtu int) error
	// EnsureAddr оставляет на интерфейсе ровно один IPv4-адрес addr.
	EnsureAddr(name string, addr netip.Prefix) error
	// Up поднимает интерфейс.
	Up(name string) error
	// Delete снимает интерфейс. Отсутствие интерфейса ошибкой не считается.
	Delete(name string) error
	// DiagState читает состояние интерфейса, ничего не меняя. Отсутствие
	// интерфейса — нулевое состояние, а не ошибка.
	DiagState(name string) (DiagLinkState, error)
}

// wgDeviceOps — операции wgctrl над устройством WireGuard.
type wgDeviceOps interface {
	Device(name string) (*wgtypes.Device, error)
	ConfigureDevice(name string, cfg wgtypes.Config) error
	Close() error
}

// WGManager владеет интерфейсом wg0: поднимает его, синхронизирует пиров из
// состояния БД и читает статистику.
//
// Состояние в памяти не кэшируется: источник правды по пирам — БД, по факту на
// интерфейсе — сам интерфейс. Разница между ними и есть то, что применяется.
type WGManager struct {
	cfg    WGConfig
	keys   WGKeyStore
	link   wgLinkOps
	device wgDeviceOps
	log    *slog.Logger
}

// NewWGManager собирает управление интерфейсом с настоящими netlink и wgctrl.
func NewWGManager(cfg WGConfig, keys WGKeyStore, log *slog.Logger) (*WGManager, error) {
	client, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("клиент wgctrl: %w", err)
	}
	return newWGManager(cfg, keys, newWGLink(), client, log), nil
}

// newWGManager — конструктор с подменяемыми зависимостями, для тестов.
func newWGManager(cfg WGConfig, keys WGKeyStore, link wgLinkOps, device wgDeviceOps,
	log *slog.Logger,
) *WGManager {
	if cfg.Name == "" {
		cfg.Name = DefaultWGInterface
	}
	if log == nil {
		log = slog.Default()
	}
	return &WGManager{cfg: cfg, keys: keys, link: link, device: device, log: log}
}

// Name — имя интерфейса, которым управляет менеджер.
func (m *WGManager) Name() string { return m.cfg.Name }

// Close отпускает клиент wgctrl.
func (m *WGManager) Close() error {
	if err := m.device.Close(); err != nil {
		return fmt.Errorf("закрытие клиента wgctrl: %w", err)
	}
	return nil
}

// Up приводит интерфейс к описанному в настройках виду и поднимает его.
//
// Вызов идемпотентен: на уже поднятом интерфейсе с теми же параметрами не
// меняется ничего. Приватный ключ и порт переписываются только при расхождении —
// перезапись ключа заставляет пиров передоговариваться заново.
func (m *WGManager) Up(ctx context.Context) error {
	if err := m.cfg.validate(); err != nil {
		return err
	}
	key, err := EnsureWGServerKey(ctx, m.keys)
	if err != nil {
		return err
	}

	if err := m.link.Ensure(m.cfg.Name, m.cfg.MTU); err != nil {
		return err
	}
	if err := m.link.EnsureAddr(m.cfg.Name, m.cfg.Address); err != nil {
		return err
	}

	dev, err := m.device.Device(m.cfg.Name)
	if err != nil {
		return fmt.Errorf("чтение устройства %s: %w", m.cfg.Name, err)
	}
	if cfg, changed := wgDeviceConfig(dev, key, m.cfg.ListenPort); changed {
		if err := m.device.ConfigureDevice(m.cfg.Name, cfg); err != nil {
			return fmt.Errorf("настройка устройства %s: %w", m.cfg.Name, err)
		}
	}
	if err := m.link.Up(m.cfg.Name); err != nil {
		return err
	}
	m.log.Info("интерфейс поднят",
		"интерфейс", m.cfg.Name, "адрес", m.cfg.Address.String(),
		"порт", m.cfg.ListenPort, "mtu", m.cfg.MTU)
	return nil
}

// Down снимает интерфейс целиком.
//
// Рестарт демона его не вызывает: [WGManager.Up] идемпотентен и переживает
// рестарт без разрыва клиентских сессий. Down — это удаление razdacha и
// `--reset-network`, то есть осознанный возврат системы в исходное состояние.
func (m *WGManager) Down(_ context.Context) error {
	if err := m.link.Delete(m.cfg.Name); err != nil {
		return err
	}
	m.log.Info("интерфейс снят", "интерфейс", m.cfg.Name)
	return nil
}

// SyncPeers приводит набор пиров на интерфейсе к состоянию БД.
//
// Синхронизация идёт диффом, а не перезаливкой: перезалив снимает и заводит
// пиров заново, а это рвёт живые хендшейки у тех, у кого ничего не менялось.
// Выключенный пир снимается с интерфейса и остаётся в БД.
func (m *WGManager) SyncPeers(_ context.Context, peers []store.Peer) (WGPeerChanges, error) {
	desired, err := wgDesiredPeers(peers)
	if err != nil {
		return WGPeerChanges{}, err
	}
	dev, err := m.device.Device(m.cfg.Name)
	if err != nil {
		return WGPeerChanges{}, fmt.Errorf("чтение устройства %s: %w", m.cfg.Name, err)
	}

	changes := wgDiffPeers(desired, dev.Peers)
	if changes.Empty() {
		return changes, nil
	}
	if err := m.device.ConfigureDevice(m.cfg.Name, wgtypes.Config{Peers: changes.Configs}); err != nil {
		return WGPeerChanges{}, fmt.Errorf("синхронизация пиров на %s: %w", m.cfg.Name, err)
	}
	m.log.Info("пиры синхронизированы", "интерфейс", m.cfg.Name,
		"добавлено", changes.Added, "изменено", changes.Updated, "снято", changes.Removed)
	return changes, nil
}

// WGPeerStat — замер по одному пиру. Нулевые значения означают «пир на
// интерфейсе есть, но обмена ещё не было», отсутствие ключа в карте — «пира на
// интерфейсе нет вовсе».
type WGPeerStat struct {
	PublicKey     string
	LastHandshake time.Time
	RxBytes       int64
	TxBytes       int64
	Endpoint      string
}

// Online — был ли хендшейк достаточно недавно.
func (s WGPeerStat) Online(now time.Time) bool {
	return !s.LastHandshake.IsZero() && now.Sub(s.LastHandshake) < wgHandshakeTimeout
}

// Stats читает статистику всех пиров интерфейса. Ключ карты — публичный ключ
// пира в том же виде, в каком он лежит в БД (base64).
//
// Интерфейса ещё нет — это [ErrWGNoDevice], а не пустая карта: «данных нет» и
// «все пиры молчат» для UI разные состояния.
func (m *WGManager) Stats(_ context.Context) (map[string]WGPeerStat, error) {
	dev, err := m.device.Device(m.cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("чтение устройства %s: %w", m.cfg.Name, err)
	}
	return wgStats(dev), nil
}

// PublicKey — публичный ключ сервера из хранилища. Пустая строка означает, что
// ключ ещё не заведён: интерфейс ни разу не поднимали.
func (m *WGManager) PublicKey(ctx context.Context) (string, error) {
	return WGServerPublicKey(ctx, m.keys)
}

// wgStats раскладывает пиров устройства по публичным ключам.
func wgStats(dev *wgtypes.Device) map[string]WGPeerStat {
	out := make(map[string]WGPeerStat, len(dev.Peers))
	for _, p := range dev.Peers {
		stat := WGPeerStat{
			PublicKey: p.PublicKey.String(),
			RxBytes:   p.ReceiveBytes,
			TxBytes:   p.TransmitBytes,
		}
		if !p.LastHandshakeTime.IsZero() {
			stat.LastHandshake = p.LastHandshakeTime.UTC()
		}
		if p.Endpoint != nil {
			stat.Endpoint = p.Endpoint.String()
		}
		out[stat.PublicKey] = stat
	}
	return out
}

// wgDeviceConfig считает, что нужно переписать в устройстве. Второе значение —
// нужна ли запись вообще: лишний вызов с тем же приватным ключом заставляет
// пиров передоговариваться.
func wgDeviceConfig(dev *wgtypes.Device, key wgtypes.Key, port int) (wgtypes.Config, bool) {
	var (
		cfg     wgtypes.Config
		changed bool
	)
	if dev == nil || dev.PrivateKey != key {
		cfg.PrivateKey = &key
		changed = true
	}
	if dev == nil || dev.ListenPort != port {
		cfg.ListenPort = &port
		changed = true
	}
	return cfg, changed
}

// validate проверяет параметры интерфейса до обращения к netlink.
func (c WGConfig) validate() error {
	if !c.Address.IsValid() || !c.Address.Addr().Is4() {
		return fmt.Errorf("%w: адрес интерфейса %q не IPv4-подсеть", ErrWGConfig, c.Address)
	}
	if c.ListenPort < 1 || c.ListenPort > 65535 {
		return fmt.Errorf("%w: порт %d вне диапазона 1–65535", ErrWGConfig, c.ListenPort)
	}
	// Границы те же, что у настроек: MTU wg0 обязан совпадать с MTU клиентов
	// (ADR 0004), и два разных диапазона на один параметр означали бы, что
	// значение, отвергнутое панелью, проходит здесь.
	if c.MTU < store.MinClientMTU || c.MTU > store.MaxClientMTU {
		return fmt.Errorf("%w: MTU %d вне диапазона %d–%d",
			ErrWGConfig, c.MTU, store.MinClientMTU, store.MaxClientMTU)
	}
	return nil
}
