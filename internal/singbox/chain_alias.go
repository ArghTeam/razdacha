package singbox

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/netip"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// wireGuardParentTag is internal runtime wiring, never a routing policy target.
func wireGuardParentTag(parent string) string {
	return fmt.Sprintf("wg-parent-%x", sha256.Sum256([]byte(parent)))
}

// stabilizeWireGuardDetours breaks the pinned runtime's startup lock cycle when
// a hostname WireGuard endpoint dials through another WireGuard endpoint. A
// singleton selector initializes its parent before endpoint startup, without
// changing the selected exit, peer addresses, DNS or any route (ADR 0030).
func stabilizeWireGuardDetours(endpoints []option.Endpoint, outbounds []option.Outbound) ([]option.Endpoint, []option.Outbound, error) {
	parents := make(map[string]bool)
	used := map[string]bool{TagDirect: true}
	for _, ep := range endpoints {
		used[ep.Tag] = true
		if ep.Type == C.TypeWireGuard {
			parents[ep.Tag] = true
		}
	}
	for _, outbound := range outbounds {
		used[outbound.Tag] = true
	}
	aliases := make(map[string]string)
	result := append([]option.Endpoint(nil), endpoints...)
	for i, ep := range endpoints {
		if ep.Type != C.TypeWireGuard {
			continue
		}
		body, err := json.Marshal(ep.Options)
		if err != nil {
			return nil, nil, fmt.Errorf("encode WireGuard endpoint: %w", err)
		}
		var peer struct {
			Detour string `json:"detour"`
			Peers  []struct {
				Address string `json:"address"`
			} `json:"peers"`
		}
		if err := json.Unmarshal(body, &peer); err != nil {
			return nil, nil, fmt.Errorf("decode WireGuard endpoint: %w", err)
		}
		if !parents[peer.Detour] {
			continue
		}
		hostname := false
		for _, remote := range peer.Peers {
			if remote.Address != "" {
				if _, err := netip.ParseAddr(remote.Address); err != nil {
					hostname = true
				}
			}
		}
		if !hostname {
			continue
		}
		alias, exists := aliases[peer.Detour]
		if !exists {
			alias = wireGuardParentTag(peer.Detour)
			if used[alias] {
				return nil, nil, fmt.Errorf("WireGuard parent alias tag collision")
			}
			used[alias] = true
			aliases[peer.Detour] = alias
			outbounds = append(outbounds, option.Outbound{
				Type: C.TypeSelector, Tag: alias,
				Options: &option.SelectorOutboundOptions{
					Outbounds: []string{peer.Detour}, Default: peer.Detour, InterruptExistConnections: false,
				},
			})
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			return nil, nil, fmt.Errorf("decode WireGuard endpoint fields: %w", err)
		}
		fields["detour"], _ = json.Marshal(alias)
		updated, err := json.Marshal(fields)
		if err != nil {
			return nil, nil, fmt.Errorf("encode WireGuard parent detour: %w", err)
		}
		result[i].Options = json.RawMessage(updated)
	}
	return result, outbounds, nil
}
