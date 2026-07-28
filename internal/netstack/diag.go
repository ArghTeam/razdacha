package netstack

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Файл отдаёт диагностике снимки состояния системы: что видно об интерфейсе
// wg0, что залито в nftables и включён ли форвардинг. Читающие функции ничего
// не меняют и не требуют прав сверх тех, что у демона уже есть.

// diagIPForwardPath — sysctl net.ipv4.ip_forward в procfs. Читается файлом, а
// не вызовом sysctl: бинарники слой не запускает (конституция, «Инварианты»).
const diagIPForwardPath = "/proc/sys/net/ipv4/ip_forward"

// DiagWGState — состояние интерфейса клиентов глазами диагностики.
//
// Exists отделено от остальных полей нарочно: «интерфейса нет» и «интерфейс
// есть, но лежит» — разные диагнозы, и второй нельзя показать как первый.
type DiagWGState struct {
	// Name — имя интерфейса, состояние которого прочитано.
	Name string
	// Exists — интерфейс в системе есть.
	Exists bool
	// Up — интерфейс поднят (IFF_UP).
	Up bool
	// MTU — текущий MTU интерфейса. Обязан быть равен MTU клиентов (ADR 0004).
	MTU int
	// ListenPort — UDP-порт, который слушает устройство WireGuard.
	ListenPort int
	// Peers — сколько пиров сейчас на интерфейсе.
	Peers int
}

// DiagLinkState — то, что об интерфейсе известно на уровне netlink, без wgctrl.
type DiagLinkState struct {
	// Exists — интерфейс найден.
	Exists bool
	// Up — выставлен флаг IFF_UP.
	Up bool
	// MTU — текущий MTU.
	MTU int
	// Type — тип интерфейса по версии ядра, для wg0 это "wireguard".
	Type string
}

// DiagNftState — состояние таблицы `inet razdacha`.
type DiagNftState struct {
	// Table — имя таблицы, которую искали.
	Table string
	// Exists — таблица в ядре есть.
	Exists bool
	// Chains, Sets — имена найденных цепочек и сетов таблицы.
	Chains []string
	Sets   []string
	// Masquerade — в цепочке postrouting есть правило masquerade.
	Masquerade bool
	// MasqueradeOIf — интерфейс, на который это правило смотрит. Пустая строка
	// означает правило без сравнения по oifname.
	MasqueradeOIf string
	// SubnetIntervals — сколько интервалов лежит в сете razdacha_subnets.
	SubnetIntervals int
}

// DiagMissing — каких цепочек и сетов не хватает по сравнению с тем, что
// заливает [BuildNftRuleset]. Имена берутся из тех же констант, что и заливка:
// проверка не может разойтись с набором правил незаметно.
func (s DiagNftState) DiagMissing() (chains, sets []string) {
	have := func(list []string, name string) bool {
		for _, v := range list {
			if v == name {
				return true
			}
		}
		return false
	}
	for _, name := range []string{ChainMangle, ChainProxy, ChainDNSNat, ChainForward, ChainPostrouting} {
		if !have(s.Chains, name) {
			chains = append(chains, name)
		}
	}
	for _, name := range []string{SetLocalV4, SetSubnets} {
		if !have(s.Sets, name) {
			sets = append(sets, name)
		}
	}
	return chains, sets
}

// DiagState читает состояние интерфейса: netlink отдаёт факт существования,
// флаг и MTU, wgctrl — порт и число пиров.
//
// Отсутствие интерфейса ошибкой не считается: это диагноз, а не сбой чтения, и
// вызывающему он нужен отличимым от «прочитать не удалось».
func (m *WGManager) DiagState(_ context.Context) (DiagWGState, error) {
	out := DiagWGState{Name: m.cfg.Name}

	link, err := m.link.DiagState(m.cfg.Name)
	if err != nil {
		return DiagWGState{}, err
	}
	out.Exists, out.Up, out.MTU = link.Exists, link.Up, link.MTU
	if !link.Exists {
		return out, nil
	}
	if link.Type != "" && link.Type != wgLinkTypeName {
		return DiagWGState{}, fmt.Errorf("%w: %s имеет тип %q",
			ErrWGForeignLink, m.cfg.Name, link.Type)
	}

	dev, err := m.device.Device(m.cfg.Name)
	if err != nil {
		return DiagWGState{}, fmt.Errorf("чтение устройства %s: %w", m.cfg.Name, err)
	}
	out.ListenPort = dev.ListenPort
	out.Peers = len(dev.Peers)
	return out, nil
}

// DiagIPForward читает net.ipv4.ip_forward. Без него трафик клиентов не уходит
// с wg0 никуда, какими бы правильными ни были nft-правила.
func DiagIPForward(_ context.Context) (bool, error) {
	return diagIPForward(diagIPForwardPath)
}

// diagIPForward — чтение с явным путём: подменяемый файл делает разбор
// проверяемым без procfs и без Linux.
func diagIPForward(path string) (bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("чтение %s: %w", path, err)
	}
	switch value := strings.TrimSpace(string(body)); value {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("значение %s не разобрано: %q", path, value)
	}
}
