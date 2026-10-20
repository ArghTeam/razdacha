//go:build !linux

package restorestate

import "errors"

func CurrentKernelFacts() (KernelFacts, error) {
	return KernelFacts{}, errors.New("controlled restore runtime starts require Linux kernel identity")
}
func ProcessIdentityAlive(int, string) bool { return false }
