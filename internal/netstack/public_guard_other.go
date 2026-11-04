//go:build !linux

package netstack

import (
	"errors"

	"github.com/ArghTeam/razdacha/internal/ingressguard"
)

// SetPublicClientGuard is unavailable where nftables is not supported.
func SetPublicClientGuard(endpoints []ingressguard.Endpoint, enabled bool) error {
	if _, err := publicGuardPlan(endpoints); err != nil {
		return err
	}
	return errors.New("public client ingress guard requires Linux")
}
