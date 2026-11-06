package singbox

import (
	"fmt"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// compiledDestination shares the original compiler's source, action and exact
// projections; preflight never reads a list or reinterprets tunnel availability.
type compiledDestination struct {
	tunnelID, viaTunnelID string
	denied                bool
	route                 option.Rule
	dnsTags               []string
	addressCapable        bool
	id, name              string
	domains               []string
	native                bool
}

// Bound both matcher nodes and referenced strings before producing a candidate.
// This comfortably admits 100 clients and 100 rules without per-user expansion.
const realityDenialBudget = 1_000_000

func denialLeaf(raw option.RawDefaultRule) option.Rule {
	return option.Rule{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultRule{RawDefaultRule: raw}}
}

func denialLogical(mode string, invert bool, children ...option.Rule) option.Rule {
	return option.Rule{Type: C.RuleTypeLogical, LogicalOptions: option.LogicalRule{RawLogicalRule: option.RawLogicalRule{Mode: mode, Invert: invert, Rules: children}}}
}

func realityDenialPreflight(plan []compiledDestination, selected func([]string) []string) ([]option.Rule, error) {
	type positiveScope struct {
		auth    []string
		tags    []string
		address bool
	}
	var positives []positiveScope
	groups := map[string]int{}
	var out []option.Rule
	cost := 0
	charge := func(n int) error {
		cost += n
		if cost > realityDenialBudget {
			return fmt.Errorf("REALITY domain-denial preflight exceeds construction budget (%d)", realityDenialBudget)
		}
		return nil
	}
	// An inverted IP-version OR excludes numeric destinations even if a reverse
	// mapping or a later sniffer supplies a domain. FakeIP has already become FQDN.
	hostname := denialLogical(C.LogicalTypeOr, true,
		denialLeaf(option.RawDefaultRule{IPVersion: 4}),
		denialLeaf(option.RawDefaultRule{IPVersion: 6}))
	for _, p := range plan {
		d := p.route.DefaultOptions
		auth := selected(d.SourceIPCIDR)
		if len(auth) == 0 {
			continue
		}
		source := denialLeaf(option.RawDefaultRule{AuthUser: auth})
		if d.Action == C.RuleActionTypeReject {
			if len(p.dnsTags) == 0 {
				continue
			}
			children := []option.Rule{denialLeaf(option.RawDefaultRule{Inbound: []string{TagRealityIn}}), source, hostname, denialLeaf(option.RawDefaultRule{RuleSet: p.dnsTags})}
			n := 8 + len(auth) + len(p.dnsTags)
			if len(positives) > 0 {
				// Snapshot each grouped projection so later policies cannot change
				// an earlier rejection.
				shadow := make([]option.Rule, 0, len(positives))
				for _, group := range positives {
					predicate := denialLeaf(option.RawDefaultRule{AuthUser: group.auth})
					if !group.address {
						tags := append([]string(nil), group.tags...)
						predicate = denialLogical(C.LogicalTypeAnd, false, predicate, denialLeaf(option.RawDefaultRule{RuleSet: tags}))
					}
					shadow = append(shadow, predicate)
				}
				children = append(children, denialLogical(C.LogicalTypeOr, true, shadow...))
				n++
				for _, r := range shadow {
					n += denialMatcherCost(r)
				}
			}
			if err := charge(n); err != nil {
				return nil, err
			}
			rule := denialLogical(C.LogicalTypeAnd, false, children...)
			rule.LogicalOptions.RuleAction = option.RuleAction{Action: C.RuleActionTypeReject}
			out = append(out, rule)
		} else if d.Action == C.RuleActionTypeRoute {
			if !p.addressCapable && len(p.dnsTags) == 0 {
				continue
			}
			// A shared source predicate needs only one shadow: rule-set tags form an
			// exact domain OR. Any address branch conservatively shadows the scope.
			key := strings.Join(auth, "\x00")
			index, exists := groups[key]
			if !exists {
				if err := charge(1 + len(auth)); err != nil {
					return nil, err
				}
				index = len(positives)
				groups[key] = index
				positives = append(positives, positiveScope{auth: auth})
			}
			group := &positives[index]
			if !group.address {
				if p.addressCapable {
					group.address = true
					group.tags = nil
				} else {
					if err := charge(len(p.dnsTags)); err != nil {
						return nil, err
					}
					group.tags = append(group.tags, p.dnsTags...)
				}
			}
		}
	}
	return out, nil
}

func denialMatcherCost(r option.Rule) int {
	if r.Type == C.RuleTypeDefault {
		return 1 + len(r.DefaultOptions.AuthUser) + len(r.DefaultOptions.RuleSet)
	}
	n := 1
	for _, child := range r.LogicalOptions.Rules {
		n += denialMatcherCost(child)
	}
	return n
}
