package main

import (
	"context"
	"fmt"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/store"
)

type realityPolicyIndex struct {
	admissions map[string]store.RealityAdmission
	peers      map[string]store.Peer
	valid      bool
}

func indexRealityPolicy(base store.Snapshot) realityPolicyIndex {
	out := realityPolicyIndex{admissions: make(map[string]store.RealityAdmission), peers: make(map[string]store.Peer), valid: true}
	for _, a := range base.RealityAdmissions {
		if _, exists := out.admissions[a.PeerID]; exists || a.PeerID == "" || a.CredentialID == "" || a.Revision < 1 {
			out.valid = false
		}
		out.admissions[a.PeerID] = a
	}
	for _, p := range base.Peers {
		out.peers[p.ID] = p
	}
	return out
}

func (idx realityPolicyIndex) admitted(p store.Peer) bool {
	a, ok := idx.admissions[p.ID]
	c := idx.peers[p.ID]
	return idx.valid && ok && c.Enabled && c.RealityEnabled && a.CredentialID == p.RealityCredentialID && a.Revision == p.RealityRevision
}

// realityDeliveryPlan is shared by boolean eligibility and explicit serialization.
// It never constructs a URI, a client configuration, or runtime admission.
func (g *applyGate) realityDeliveryPlan(idx realityPolicyIndex, settings store.Settings, p store.Peer, identity, publicKey, shortID string, credential bool) (store.Settings, string, error) {
	if !p.Enabled || !p.RealityEnabled || !settings.RealityEnabled || !credential || p.RealityRevision < 1 || p.RealityCredentialID == "" || identity == "" || publicKey == "" || shortID == "" {
		return settings, "", fmt.Errorf("%w: REALITY profile material is unavailable", store.ErrInvalid)
	}
	state := "pending"
	if g.base.Settings.RealityEnabled {
		if identity != g.base.RealityIdentityID || !idx.valid {
			return settings, "", api.ErrNetworkGuarded
		}
		if idx.admitted(p) {
			settings, state = g.base.Settings, "available"
		}
	}
	if err := settings.ValidateRealityDelivery(); err != nil {
		return settings, "", err
	}
	if settings.ClientMTU < store.MinClientMTU || settings.ClientMTU > store.MaxClientMTU {
		return settings, "", fmt.Errorf("%w: invalid configured client MTU", store.ErrInvalid)
	}
	return settings, state, nil
}

func (g *applyGate) RealityInventory(ctx context.Context, st *store.Store, requested []store.Peer) map[string]api.RealityInventoryEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]api.RealityInventoryEntry, len(requested))
	guarded := !g.network.initialized || g.network.uncertain || g.publicGuarded()
	for _, p := range requested {
		status := "pending"
		if !p.Enabled || !p.RealityEnabled {
			status = "disabled"
		} else if guarded {
			status = "guarded"
		}
		out[p.ID] = api.RealityInventoryEntry{Status: status, Revision: p.RealityRevision}
	}
	if guarded {
		return out
	}
	live, err := st.RealityInventory(ctx)
	if err != nil {
		for id, e := range out {
			if e.Status != "disabled" {
				e.Status = "guarded"
				out[id] = e
			}
		}
		return out
	}
	idx := indexRealityPolicy(g.base)
	peers := make(map[string]store.Peer, len(live.Peers))
	for _, p := range live.Peers {
		peers[p.ID] = p
	}
	for _, requestedPeer := range requested {
		e := out[requestedPeer.ID]
		if e.Status == "disabled" {
			continue
		}
		p, exists := peers[requestedPeer.ID]
		if !exists || !p.Enabled || !p.RealityEnabled || p.RealityRevision != requestedPeer.RealityRevision || p.RealityCredentialID != requestedPeer.RealityCredentialID {
			continue
		}
		a, ok := live.Credentials[p.ID]
		credential := ok && a.Revision == p.RealityRevision && a.CredentialID == p.RealityCredentialID
		if g.base.Settings.RealityEnabled && live.Settings.RealityEnabled {
			if !idx.valid || live.IdentityID != g.base.RealityIdentityID {
				e.Status = "guarded"
			} else if credential && idx.admitted(p) {
				e.Status = "available"
			}
		}
		_, _, err := g.realityDeliveryPlan(idx, live.Settings, p, live.IdentityID, live.PublicKey, live.ShortID, credential)
		e.Downloadable = err == nil
		out[p.ID] = e
	}
	return out
}
