// Package ingressguard provides private startup authority for public client ingress.
// It does not start a runtime or change application authentication state.
package ingressguard

import (
	"fmt"
	"net/netip"
	"sort"
)

const maxEndpoints = 64

// Endpoint identifies a local TCP listener. An empty address or :: conservatively
// covers both address families, including a dual-stack IPv6 wildcard socket.
type Endpoint struct {
	Address string `json:"address"`
	Port    uint16 `json:"port"`
}

// NormalizeEndpoints validates, canonicalizes, sorts and deduplicates endpoints.
// Addresses are IP literals, never names whose resolution could change coverage.
func NormalizeEndpoints(endpoints []Endpoint) ([]Endpoint, error) {
	if len(endpoints) > maxEndpoints {
		return nil, fmt.Errorf("too many public ingress endpoints")
	}
	out := make([]Endpoint, 0, len(endpoints))
	seen := make(map[Endpoint]bool)
	for _, endpoint := range endpoints {
		if endpoint.Port == 0 {
			return nil, fmt.Errorf("public ingress port must be nonzero")
		}
		if endpoint.Address == "" {
			endpoint.Address = "::"
		}
		address, err := netip.ParseAddr(endpoint.Address)
		if err != nil || address.Zone() != "" {
			return nil, fmt.Errorf("public ingress address must be an unzoned IP literal")
		}
		endpoint.Address = address.Unmap().String()
		if !seen[endpoint] {
			seen[endpoint] = true
			out = append(out, endpoint)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Address != out[j].Address {
			return out[i].Address < out[j].Address
		}
		return out[i].Port < out[j].Port
	})
	return out, nil
}

// Union retains previous and candidate coverage throughout publication.
func Union(groups ...[]Endpoint) ([]Endpoint, error) {
	var endpoints []Endpoint
	for _, group := range groups {
		endpoints = append(endpoints, group...)
	}
	return NormalizeEndpoints(endpoints)
}

func covers(have, want Endpoint) bool {
	if have.Port != want.Port {
		return false
	}
	a, b := netip.MustParseAddr(have.Address), netip.MustParseAddr(want.Address)
	if a.IsUnspecified() {
		return a.Is6() || b.Is4()
	}
	return a == b
}
