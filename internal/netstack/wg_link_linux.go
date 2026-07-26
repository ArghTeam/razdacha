package netstack

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
)

// netlinkWGLink — операции над wg0 через netlink. Бинарники `ip` и `wg-quick`
// не вызываются: набор и версии утилит на целевой матрице ОС не совпадают.
type netlinkWGLink struct{}

// newWGLink — реализация для целевой платформы.
func newWGLink() wgLinkOps { return netlinkWGLink{} }

// Ensure создаёт интерфейс, если его нет, и приводит MTU к заданному.
func (netlinkWGLink) Ensure(name string, mtu int) error {
	link, err := wgLinkByName(name)
	if err != nil {
		return err
	}
	if link == nil {
		attrs := netlink.NewLinkAttrs()
		attrs.Name = name
		attrs.MTU = mtu
		if err := netlink.LinkAdd(&netlink.Wireguard{LinkAttrs: attrs}); err != nil {
			return fmt.Errorf("создание интерфейса %s: %w", name, err)
		}
		if link, err = wgLinkByName(name); err != nil {
			return err
		}
		if link == nil {
			return fmt.Errorf("создание интерфейса %s: интерфейс не появился", name)
		}
	}
	if link.Type() != wgLinkTypeName {
		return fmt.Errorf("%w: %s имеет тип %q, а не %q",
			ErrWGForeignLink, name, link.Type(), wgLinkTypeName)
	}
	// MTU обязан совпадать с MTU клиентов (ADR 0004), поэтому он выставляется и
	// на уже существующем интерфейсе: значение могли поменять снаружи.
	if link.Attrs().MTU != mtu {
		if err := netlink.LinkSetMTU(link, mtu); err != nil {
			return fmt.Errorf("установка MTU %d на %s: %w", mtu, name, err)
		}
	}
	return nil
}

// EnsureAddr оставляет на интерфейсе ровно один IPv4-адрес. Лишние снимаются:
// смена пула в настройках не должна оставлять на wg0 адрес из прежнего.
func (netlinkWGLink) EnsureAddr(name string, prefix netip.Prefix) error {
	link, err := wgLinkByName(name)
	if err != nil {
		return err
	}
	if link == nil {
		return fmt.Errorf("%w: интерфейса %s нет", ErrWGNoDevice, name)
	}

	want := &netlink.Addr{IPNet: &net.IPNet{
		IP:   net.IP(prefix.Addr().AsSlice()),
		Mask: net.CIDRMask(prefix.Bits(), 32),
	}}
	have, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("чтение адресов %s: %w", name, err)
	}

	found := false
	for i := range have {
		if have[i].Equal(*want) {
			found = true
			continue
		}
		if err := netlink.AddrDel(link, &have[i]); err != nil {
			return fmt.Errorf("снятие адреса %s с %s: %w", have[i].IPNet, name, err)
		}
	}
	if found {
		return nil
	}
	if err := netlink.AddrAdd(link, want); err != nil {
		return fmt.Errorf("назначение адреса %s на %s: %w", want.IPNet, name, err)
	}
	return nil
}

// Up поднимает интерфейс.
func (netlinkWGLink) Up(name string) error {
	link, err := wgLinkByName(name)
	if err != nil {
		return err
	}
	if link == nil {
		return fmt.Errorf("%w: интерфейса %s нет", ErrWGNoDevice, name)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("поднятие интерфейса %s: %w", name, err)
	}
	return nil
}

// Delete снимает интерфейс. Отсутствие интерфейса ошибкой не считается:
// удаление обязано доводиться до конца с любого места.
func (netlinkWGLink) Delete(name string) error {
	link, err := wgLinkByName(name)
	if err != nil || link == nil {
		return err
	}
	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("удаление интерфейса %s: %w", name, err)
	}
	return nil
}

// DiagState читает интерфейс, ничего не меняя: диагностике нужны факт
// существования, флаг IFF_UP, MTU и тип.
func (netlinkWGLink) DiagState(name string) (DiagLinkState, error) {
	link, err := wgLinkByName(name)
	if err != nil {
		return DiagLinkState{}, err
	}
	if link == nil {
		return DiagLinkState{}, nil
	}
	attrs := link.Attrs()
	return DiagLinkState{
		Exists: true,
		Up:     attrs.Flags&net.FlagUp != 0,
		MTU:    attrs.MTU,
		Type:   link.Type(),
	}, nil
}

// wgLinkByName отличает «интерфейса нет» (nil, nil) от ошибки netlink.
func wgLinkByName(name string) (netlink.Link, error) {
	link, err := netlink.LinkByName(name)
	if err == nil {
		return link, nil
	}
	var notFound netlink.LinkNotFoundError
	if errors.As(err, &notFound) {
		return nil, nil
	}
	return nil, fmt.Errorf("чтение интерфейса %s: %w", name, err)
}
