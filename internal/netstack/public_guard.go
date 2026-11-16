package netstack

import (
	"net/netip"

	"github.com/ArghTeam/razdacha/internal/ingressguard"
)

// PublicClientGuardTable belongs only to the public client-listener barrier.
// It is independent of both the WG guard and replaceable routing tables.
const PublicClientGuardTable = "razdacha_public_guard"

type publicGuardRule struct {
	family  uint8
	address netip.Addr
	port    uint16
}

func publicGuardPlan(endpoints []ingressguard.Endpoint) ([]publicGuardRule, error) {
	endpoints, err := ingressguard.NormalizeEndpoints(endpoints)
	if err != nil {
		return nil, err
	}
	var rules []publicGuardRule
	for _, endpoint := range endpoints {
		address := netip.MustParseAddr(endpoint.Address)
		if address.IsUnspecified() {
			rules = append(rules, publicGuardRule{family: 4, port: endpoint.Port})
			if address.Is6() {
				rules = append(rules, publicGuardRule{family: 6, port: endpoint.Port})
			}
			continue
		}
		family := uint8(6)
		if address.Is4() {
			family = 4
		}
		rules = append(rules, publicGuardRule{family: family, address: address, port: endpoint.Port})
	}
	return rules, nil
}
