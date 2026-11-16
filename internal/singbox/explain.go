package singbox

import (
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"strings"

	"github.com/ArghTeam/razdacha/internal/store"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/domain"
)

// ExplainRequest deliberately accepts only a domain-bearing web TCP request.
type ExplainRequest struct {
	PeerID    string `json:"peer_id"`
	Transport string `json:"transport"`
	Domain    string `json:"domain"`
}

// NormalizeExplainDomain rejects addresses and non-host input rather than probing it.
func NormalizeExplainDomain(raw string) (string, error) {
	domain := strings.ToLower(strings.TrimSuffix(raw, "."))
	if len(domain) == 0 || len(domain) > 253 || strings.TrimSpace(raw) != raw {
		return "", fmt.Errorf("enter a domain without URL, port, path or IP address")
	}
	if _, err := netip.ParseAddr(domain); err == nil {
		return "", fmt.Errorf("IP addresses are outside domain-only scope")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid domain label")
		}
		for _, c := range label {
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			default:
				return "", fmt.Errorf("enter an ASCII domain without URL, port, path or CIDR")
			}
		}
	}
	return domain, nil
}

// PolicyOutcome is an allowlist, never a raw runtime rule or outbound.
type PolicyOutcome struct {
	State         string `json:"state"`
	Action        string `json:"action"`
	Reason        string `json:"reason"`
	RuleID        string `json:"rule_id,omitempty"`
	Name          string `json:"name,omitempty"`
	TunnelID      string `json:"tunnel_id,omitempty"`
	ViaTunnelID   string `json:"via_tunnel_id,omitempty"`
	TunnelName    string `json:"tunnel_name,omitempty"`
	ViaTunnelName string `json:"via_tunnel_name,omitempty"`
}

// PolicyExplanation separates conditional route and gateway DNS policy.
type PolicyExplanation struct {
	Scope      string          `json:"scope"`
	PeerID     string          `json:"peer_id"`
	Transport  string          `json:"transport"`
	Domain     string          `json:"domain"`
	State      string          `json:"state"`
	Reason     string          `json:"reason,omitempty"`
	Pending    bool            `json:"pending"`
	Marker     string          `json:"marker,omitempty"`
	CheckedAt  string          `json:"checked_at,omitempty"`
	Routing    PolicyOutcome   `json:"routing"`
	GatewayA   PolicyOutcome   `json:"gateway_a"`
	Candidates []PolicyOutcome `json:"candidates"`
	Limits     []string        `json:"limits"`
}

// UnknownExplanation never substitutes a direct route for missing authority.
func UnknownExplanation(req ExplainRequest, reason string) PolicyExplanation {
	unknown := PolicyOutcome{State: "unknown", Action: "unknown", Reason: reason}
	return PolicyExplanation{Scope: "Web policy / TCP 443", PeerID: req.PeerID, Transport: req.Transport, Domain: req.Domain, State: "unknown", Reason: reason, Routing: unknown, GatewayA: unknown, Candidates: []PolicyOutcome{}, Limits: []string{"Conditional domain policy for a public resolved destination; private and gateway guards can reject first.", "Gateway A-query policy is conditional, not an actual DNS query.", "No DNS resolution, dynamic sniffing, client traffic, connection quality, DPI resistance, UDP or media quality is measured."}}
}

const explainBudget = 1_000_000

// ExplainPolicy consumes evidence from the configuration compiler's one source pass.
// It supplies no live listener context and performs no network or artifact reads.
func ExplainPolicy(snap store.Snapshot, plain PlainLists, req ExplainRequest) (out PolicyExplanation, err error) {
	out = UnknownExplanation(req, "policy_unavailable")
	defer func() {
		names := make(map[string]string, len(snap.Tunnels))
		for _, t := range snap.Tunnels {
			names[t.ID] = t.Name
		}
		decorate := func(p *PolicyOutcome) { p.TunnelName = names[p.TunnelID]; p.ViaTunnelName = names[p.ViaTunnelID] }
		decorate(&out.Routing)
		decorate(&out.GatewayA)
		for i := range out.Candidates {
			decorate(&out.Candidates[i])
		}
	}()

	var peer store.Peer
	for _, p := range snap.Peers {
		if p.ID == req.PeerID {
			peer = p
			break
		}
	}
	if peer.ID == "" {
		return out, nil
	}
	var plan []compiledDestination
	opts, _, err := generatePolicyWithLogger(snap, plain, &plan, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return out, fmt.Errorf("compile explanation: %w", err)
	}
	source, err := cidr(peer.Address)
	if err != nil {
		return out, err
	}
	applies := func(sources []string) bool {
		if len(sources) == 0 {
			return true
		}
		for _, s := range sources {
			if s == source {
				return true
			}
		}
		return false
	}
	if req.Transport == "reality" && strings.EqualFold(req.Domain, snap.Settings.EndpointHost) {
		out.State = "conditional"
		out.Reason = "gateway_domain_guard"
		out.Routing = PolicyOutcome{State: "conditional", Action: "reject", Reason: "gateway_domain_guard"}
		out.GatewayA = explainDNS(opts.DNS.Rules, plan, applies, req.Domain)
		return out, nil
	}
	out.State = "conditional"
	out.Reason = "public_destination_assumed"
	out.Routing = PolicyOutcome{State: "conditional", Action: "direct", Reason: "final_policy"}
	out.GatewayA = explainDNS(opts.DNS.Rules, plan, applies, req.Domain)
	// Whole-client terminal outcomes already emitted by peerEgress precede all
	// destination rules. Ignore its resolve/private guards, described in Limits.
	if peer.EgressTunnelID != nil {
		for _, r := range opts.Route.Rules {
			d := r.DefaultOptions
			if len(d.SourceIPCIDR) == 0 || !applies(d.SourceIPCIDR) || len(d.RuleSet) > 0 || len(d.IPCIDR) > 0 {
				continue
			}
			if d.Action != C.RuleActionTypeRoute && d.Action != C.RuleActionTypeReject {
				continue
			}
			out.Routing = explainAction(d.RuleAction, "whole_client_exit")
			out.Routing.TunnelID = *peer.EgressTunnelID
			return out, nil
		}
		return UnknownExplanation(req, "whole_client_evidence_missing"), nil
	}
	cost := 0
	routeDone := false
	for _, p := range plan {
		cost += 1 + len(p.domains)
		if cost > explainBudget {
			return UnknownExplanation(req, "construction_budget"), nil
		}
		if !applies(p.route.DefaultOptions.SourceIPCIDR) {
			continue
		}
		exact := matchDomainSuffixes(req.Domain, p.domains)
		if !routeDone {
			candidate := explainAction(p.route.DefaultOptions.RuleAction, "domain_suffix")
			candidate.RuleID = p.id
			candidate.Name = p.name
			candidate.TunnelID = p.tunnelID
			candidate.ViaTunnelID = p.viaTunnelID
			if p.denied {
				candidate.Reason = "configured_exit_unavailable"
			}
			if exact {
				out.Routing = candidate
				routeDone = true
				if len(out.Candidates) > 0 {
					out.Routing = PolicyOutcome{State: "unknown", Action: "unknown", Reason: "earlier_unresolved_candidates"}
					out.State = "unknown"
					out.Candidates = append(out.Candidates, candidate)
				}
			} else if p.native || p.addressCapable {
				candidate.State = "unknown"
				candidate.Reason = "unresolved_address_or_native_predicate"
				out.Candidates = append(out.Candidates, candidate)
			}
		}
	}
	if !routeDone && len(out.Candidates) > 0 {
		out.Candidates = append(out.Candidates, out.Routing)
		out.Routing = PolicyOutcome{State: "unknown", Action: "unknown", Reason: "unresolved_candidates"}
		out.State = "unknown"
	}
	return out, nil
}

func explainAction(action option.RuleAction, reason string) PolicyOutcome {
	out := PolicyOutcome{State: "conditional", Action: "tunnel", Reason: reason}
	switch {
	case action.Action == C.RuleActionTypeReject:
		out.Action = "reject"
		out.Reason = "effective_rejection"
	case action.RouteOptions.Outbound == TagDirect:
		out.Action = "direct"
	default:
		out.TunnelID = strings.TrimPrefix(action.RouteOptions.Outbound, "tun-")
	}
	return out
}

// explainDNS follows emitted gateway A-query actions, including safety and
// whole-client rules. The plan contributes only the compiler's exact projections.
func explainDNS(rules []option.DNSRule, plan []compiledDestination, applies func([]string) bool, domain string) PolicyOutcome {
	evidence := make(map[string]compiledDestination)
	for _, p := range plan {
		for _, tag := range p.dnsTags {
			evidence[tag] = p
		}
	}
	for _, r := range rules {
		d := r.DefaultOptions
		if !applies(d.SourceIPCIDR) {
			continue
		}
		if len(d.QueryType) > 0 {
			a := false
			for _, q := range d.QueryType {
				if q == 1 {
					a = true
				}
			}
			if !a {
				continue
			}
		}
		exact, possible := len(d.RuleSet) == 0 && len(d.Domain) == 0 && len(d.DomainSuffix) == 0, false
		var id, name string
		for _, v := range d.Domain {
			if strings.EqualFold(domain, v) {
				exact = true
			}
		}
		for _, v := range d.DomainSuffix {
			if suffixMatch(domain, v) {
				exact = true
			}
		}
		for _, tag := range d.RuleSet {
			p, ok := evidence[tag]
			if !ok {
				possible = true
				continue
			}
			if matchDomainSuffixes(domain, p.domains) {
				exact = true
				id = p.id
				name = p.name
			}
			possible = possible || p.native
		}
		if !exact && !possible {
			continue
		}
		if !exact {
			return PolicyOutcome{State: "unknown", Action: "unknown", Reason: "native_dns_predicate"}
		}
		out := PolicyOutcome{State: "conditional", Action: "upstream", Reason: "generated_dns_action", RuleID: id, Name: name}
		switch {
		case d.Action == C.RuleActionTypeReject:
			out.Action = "reject"
		case d.RouteOptions.Server == TagDNSFakeIP:
			out.Action = "fakeip"
		case strings.HasPrefix(d.RouteOptions.Server, "peer-dns-"):
			out.Action = "same_exit_upstream"
			out.TunnelID = strings.TrimPrefix(d.RouteOptions.Server, "peer-dns-")
		}
		return out
	}
	return PolicyOutcome{State: "conditional", Action: "upstream", Reason: "final_dns_policy"}
}
func suffixMatch(host, suffix string) bool { return matchDomainSuffixes(host, []string{suffix}) }
func matchDomainSuffixes(host string, suffixes []string) bool {
	if len(suffixes) == 0 {
		return false
	}
	for _, s := range suffixes {
		if s == "" {
			return false
		}
	}
	// Exactly the pinned runtime DomainItem matcher, including leading-dot scope.
	return domain.NewMatcher(nil, suffixes, false).Match(strings.ToLower(host))
}
