//go:build !linux

package netstack

import (
	"fmt"
	"net/netip"
)

// unsupportedWGLink — заглушка для платформ без netlink. Демон работает только
// на Linux (docs/07-platform-support.md); сборка под остальные нужна ради
// тестов и локальной разработки, и молчать она не должна.
type unsupportedWGLink struct{}

// newWGLink — реализация для платформ, где интерфейс не поднять.
func newWGLink() wgLinkOps { return unsupportedWGLink{} }

func (unsupportedWGLink) Ensure(name string, _ int) error {
	return fmt.Errorf("%w: интерфейс %s", ErrWGUnsupported, name)
}

func (unsupportedWGLink) EnsureAddr(name string, _ netip.Prefix) error {
	return fmt.Errorf("%w: интерфейс %s", ErrWGUnsupported, name)
}

func (unsupportedWGLink) Up(name string) error {
	return fmt.Errorf("%w: интерфейс %s", ErrWGUnsupported, name)
}

func (unsupportedWGLink) Delete(name string) error {
	return fmt.Errorf("%w: интерфейс %s", ErrWGUnsupported, name)
}
