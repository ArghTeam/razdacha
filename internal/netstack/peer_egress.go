package netstack

import "github.com/ArghTeam/razdacha/internal/store"

// PeerEgressSources preserves disabled/deleted committed source restrictions until Apply.
func PeerEgressSources(peers []store.Peer) []string {
	var out []string
	for _, p := range peers {
		if p.EgressTunnelID != nil {
			out = append(out, p.Address)
		}
	}
	return out
}
