package netstack

import (
	"fmt"
	"net"
	"net/netip"
	"sort"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/ArghTeam/razdacha/internal/store"
)

// wgDesiredPeer — пир, каким он должен быть на интерфейсе. Разобранное
// представление записи БД: ключи уже проверены, адрес превращён в /32.
type wgDesiredPeer struct {
	PublicKey    wgtypes.Key
	PresharedKey wgtypes.Key
	AllowedIPs   []net.IPNet
}

// WGPeerChanges — результат сравнения БД с интерфейсом. Configs уходит в wgctrl
// как есть; счётчики нужны логам и вызывающему, чтобы отличить «ничего не
// поменялось» от «переписан весь набор».
type WGPeerChanges struct {
	Configs []wgtypes.PeerConfig
	Added   int
	Updated int
	Removed int
}

// Empty — синхронизировать нечего.
func (c WGPeerChanges) Empty() bool { return len(c.Configs) == 0 }

// wgDesiredPeers разбирает записи БД в набор для интерфейса.
//
// Выключенный пир в набор не попадает: он обязан исчезнуть с wg0, оставшись в
// БД со всеми ключами, чтобы включение не требовало перевыпуска конфига.
func wgDesiredPeers(peers []store.Peer) ([]wgDesiredPeer, error) {
	out := make([]wgDesiredPeer, 0, len(peers))
	for _, p := range peers {
		if !p.Enabled {
			continue
		}
		pub, err := wgtypes.ParseKey(p.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("%w: публичный ключ пира %q не разобран: %w",
				ErrWGConfig, p.Name, err)
		}
		psk, err := wgtypes.ParseKey(p.PresharedKey)
		if err != nil {
			return nil, fmt.Errorf("%w: pre-shared ключ пира %q не разобран: %w",
				ErrWGConfig, p.Name, err)
		}
		addr, err := netip.ParseAddr(p.Address)
		if err != nil || !addr.Is4() {
			return nil, fmt.Errorf("%w: адрес пира %q (%s) не IPv4", ErrWGConfig, p.Name, p.Address)
		}
		out = append(out, wgDesiredPeer{
			PublicKey:    pub,
			PresharedKey: psk,
			// Ровно один /32: сервер маршрутизирует в туннель только адрес
			// самого клиента, иначе два пира спорят за один префикс.
			AllowedIPs: []net.IPNet{{
				IP:   net.IP(addr.AsSlice()),
				Mask: net.CIDRMask(32, 32),
			}},
		})
	}
	return out, nil
}

// wgDiffPeers считает минимальный набор изменений: что добавить, что переписать,
// что снять. Пир, у которого ничего не изменилось, в результат не попадает —
// повторная запись того же пира сбрасывает его сессию.
//
// Pre-shared ключ читается из дампа устройства наравне с остальным: ядро отдаёт
// его root'у, а демон работает только от root.
func wgDiffPeers(desired []wgDesiredPeer, current []wgtypes.Peer) WGPeerChanges {
	have := make(map[wgtypes.Key]wgtypes.Peer, len(current))
	for _, p := range current {
		have[p.PublicKey] = p
	}

	var changes WGPeerChanges
	want := make(map[wgtypes.Key]struct{}, len(desired))
	for _, d := range desired {
		want[d.PublicKey] = struct{}{}

		existing, ok := have[d.PublicKey]
		if !ok {
			psk := d.PresharedKey
			changes.Configs = append(changes.Configs, wgtypes.PeerConfig{
				PublicKey:         d.PublicKey,
				PresharedKey:      &psk,
				ReplaceAllowedIPs: true,
				AllowedIPs:        d.AllowedIPs,
			})
			changes.Added++
			continue
		}
		if !wgPeerChanged(d, existing) {
			continue
		}
		psk := d.PresharedKey
		changes.Configs = append(changes.Configs, wgtypes.PeerConfig{
			PublicKey: d.PublicKey,
			// UpdateOnly: пир уже есть, и если он исчез между дампом и записью,
			// его не надо заводить заново вслепую.
			UpdateOnly:        true,
			PresharedKey:      &psk,
			ReplaceAllowedIPs: true,
			AllowedIPs:        d.AllowedIPs,
		})
		changes.Updated++
	}

	for _, p := range current {
		if _, ok := want[p.PublicKey]; ok {
			continue
		}
		changes.Configs = append(changes.Configs, wgtypes.PeerConfig{
			PublicKey: p.PublicKey,
			Remove:    true,
		})
		changes.Removed++
	}

	// Порядок записи роли не играет, но детерминированный результат читается в
	// логах и сравнивается в тестах.
	sort.SliceStable(changes.Configs, func(i, j int) bool {
		return changes.Configs[i].PublicKey.String() < changes.Configs[j].PublicKey.String()
	})
	return changes
}

// wgPeerChanged — разошлись ли параметры пира на интерфейсе с ожидаемыми.
func wgPeerChanged(want wgDesiredPeer, have wgtypes.Peer) bool {
	if want.PresharedKey != have.PresharedKey {
		return true
	}
	return !wgSameNets(want.AllowedIPs, have.AllowedIPs)
}

// wgSameNets сравнивает наборы подсетей как множества: порядок в дампе ядра не
// обещан, а разный порядок — не изменение.
func wgSameNets(a, b []net.IPNet) bool {
	if len(a) != len(b) {
		return false
	}
	left, right := wgNetStrings(a), wgNetStrings(b)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// wgNetStrings — подсети в текстовом виде, отсортированные для сравнения.
func wgNetStrings(nets []net.IPNet) []string {
	out := make([]string, 0, len(nets))
	for _, n := range nets {
		out = append(out, n.String())
	}
	sort.Strings(out)
	return out
}
