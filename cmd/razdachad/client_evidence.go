package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/ingressguard"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// ClientEvidenceAuthority reads only private publication metadata and narrow live
// eligibility. All runtime/network source reads happen after this lock is released.
func (g *applyGate) ClientEvidenceAuthority(ctx context.Context, st *store.Store, id, transport string) (api.ClientEvidenceAuthority, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.clientEvidenceAuthority(ctx, st, id, transport)
}

func (g *applyGate) clientEvidenceAuthority(ctx context.Context, st *store.Store, id, transport string) (api.ClientEvidenceAuthority, error) {
	out := api.ClientEvidenceAuthority{State: "unknown", Reason: "authority_unavailable"}
	reason, err := g.explainEligibility(ctx, st, singbox.ExplainRequest{PeerID: id, Transport: transport})
	if err != nil {
		return out, err
	}
	if reason != "" {
		out.Reason = reason
		if reason == "transport_disabled" || reason == "transport_not_admitted" || reason == "client_not_applied" {
			out.State = "disabled"
		}
		return out, nil
	}
	manifest, err := ingressguard.Read(g.manifestPath())
	if err != nil {
		return out, fmt.Errorf("read observation manifest: %w", err)
	}
	if manifest.Guarded || len(manifest.ConfigHashes) != 1 {
		return out, nil
	}
	config, err := ingressguard.ReadConfig(g.configPath())
	if err != nil {
		return out, fmt.Errorf("read observation configuration: %w", err)
	}
	hash := ingressguard.HashConfig(config)
	if hash != manifest.ConfigHashes[0] {
		return out, nil
	}
	live, err := st.RouteEligibility(ctx, id)
	if err != nil {
		return out, err
	}
	if g.evidenceBoot == "" {
		var nonce [32]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return out, fmt.Errorf("initialize observation lifetime: %w", err)
		}
		g.evidenceBoot = hex.EncodeToString(nonce[:])
	}
	source, err := json.Marshal(struct {
		Boot   string
		Epoch  uint64
		Config string
	}{g.evidenceBoot, g.activityEpoch, hash})
	if err != nil {
		return out, fmt.Errorf("encode observation source: %w", err)
	}
	sum := sha256.Sum256(source)
	out.SourceEpoch = hex.EncodeToString(sum[:])
	data, err := json.Marshal(struct {
		Source, Transport string
		Live              store.RouteEligibility
	}{out.SourceEpoch, transport, live})
	if err != nil {
		return out, fmt.Errorf("encode observation authority: %w", err)
	}
	sum = sha256.Sum256(data)
	out.Epoch = hex.EncodeToString(sum[:])
	out.Peer = live.Peer
	out.State = "observed"
	out.Reason = "admitted"
	return out, nil
}

// ClientExitPlan compiles conditional applied evidence only for an explicit exit
// request, never each passive activity poll. Outbound tags stay private to the API.
func (g *applyGate) ClientExitPlan(ctx context.Context, st *store.Store, req singbox.ExplainRequest) (api.ClientExitPlan, error) {
	before, err := g.ClientEvidenceAuthority(ctx, st, req.PeerID, req.Transport)
	out := api.ClientExitPlan{Authority: before, Policy: singbox.UnknownExplanation(req, before.Reason)}
	if err != nil || before.Epoch == "" {
		return out, err
	}
	policy, err := g.ExplainRoute(ctx, st, req)
	if err != nil {
		return out, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	after, err := g.clientEvidenceAuthority(ctx, st, req.PeerID, req.Transport)
	if err != nil {
		return out, err
	}
	if after.Epoch != before.Epoch {
		out.Authority = api.ClientEvidenceAuthority{State: "unknown", Reason: "authority_changed"}
		out.Policy = singbox.UnknownExplanation(req, "authority_changed")
		return out, nil
	}
	out.Policy = policy
	if policy.State != "conditional" || policy.Routing.State != "conditional" || policy.GatewayA.State != "conditional" || policy.GatewayA.Action == "unknown" || policy.GatewayA.Action == "reject" {
		return out, nil
	}
	switch policy.Routing.Action {
	case "direct":
		out.Tag = singbox.TagDirect
	case "tunnel":
		first, via := policy.Routing.TunnelID, policy.Routing.ViaTunnelID
		if first == "" {
			return out, nil
		}
		out.Tag = singbox.TunnelTag(first)
		if via != "" {
			out.Tag = singbox.ChainTag(via, first)
		} else {
			for _, t := range g.base.Tunnels {
				if t.ID == first {
					out.Pool = t.Source == store.SourcePool
					break
				}
			}
		}
	default:
		return out, nil
	}
	// Require the exact selected outbound in the released checked runtime image.
	// This never converts absent/uncertain chain metadata into a direct fallback.
	config, err := ingressguard.ReadConfig(g.configPath())
	if err != nil || !evidenceOutboundPresent(config, out.Tag) {
		out.Tag = ""
		out.Pool = false
	}
	return out, nil
}

func evidenceOutboundPresent(config []byte, tag string) bool {
	type named struct {
		Tag string `json:"tag"`
	}
	var doc struct {
		Outbounds []named `json:"outbounds"`
		Endpoints []named `json:"endpoints"`
	}
	if json.Unmarshal(config, &doc) != nil {
		return false
	}
	for _, o := range append(doc.Outbounds, doc.Endpoints...) {
		if o.Tag == tag {
			return true
		}
	}
	return false
}
