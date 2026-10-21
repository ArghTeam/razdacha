//go:build linux

package main

import (
	"errors"
	"fmt"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

const (
	tableName = "razdacha_guard"
	chainName = "ingress"
	wgName    = "wg0"
)

type nftConn interface {
	ListTablesOfFamily(nftables.TableFamily) ([]*nftables.Table, error)
	ListChainsOfTableFamily(nftables.TableFamily) ([]*nftables.Chain, error)
	GetRules(*nftables.Table, *nftables.Chain) ([]*nftables.Rule, error)
	GetSets(*nftables.Table) ([]*nftables.Set, error)
	DelTable(*nftables.Table)
	Flush() error
}

type report struct {
	Present    bool `json:"present"`
	ExactShape bool `json:"exact_shape"`
	TableCount int  `json:"table_count"`
	ChainCount int  `json:"chain_count"`
	RuleCount  int  `json:"rule_count"`
	SetCount   int  `json:"set_count"`
}

var errUnknownShape = errors.New("guard table has unknown shape; refusing mutation")

func inspect(c nftConn) (*nftables.Table, report, error) {
	var out report
	tables, err := c.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return nil, out, fmt.Errorf("list inet tables: %w", err)
	}
	var found []*nftables.Table
	for _, t := range tables {
		if t.Name == tableName {
			found = append(found, t)
		}
	}
	out.TableCount = len(found)
	out.Present = len(found) != 0
	if !out.Present {
		out.ExactShape = true
		return nil, out, nil
	}
	if len(found) != 1 {
		return found[0], out, nil
	}
	t := found[0]
	if t.Family != nftables.TableFamilyINet || t.Flags != 0 {
		return t, out, nil
	}
	chains, err := c.ListChainsOfTableFamily(nftables.TableFamilyINet)
	if err != nil {
		return t, out, fmt.Errorf("list inet chains: %w", err)
	}
	var owned []*nftables.Chain
	for _, ch := range chains {
		if ch.Table != nil && ch.Table.Name == tableName && ch.Table.Family == nftables.TableFamilyINet {
			owned = append(owned, ch)
		}
	}
	out.ChainCount = len(owned)
	if len(owned) != 1 {
		return t, out, nil
	}
	ch := owned[0]
	if ch.Name != chainName || ch.Type != nftables.ChainTypeFilter || ch.Hooknum == nil || *ch.Hooknum != *nftables.ChainHookPrerouting || ch.Priority == nil || int(*ch.Priority) != -310 || !isDefaultAcceptPolicy(ch.Policy) || ch.Device != "" {
		return t, out, nil
	}
	sets, err := c.GetSets(t)
	if err != nil {
		return t, out, fmt.Errorf("list guard table sets: %w", err)
	}
	out.SetCount = len(sets)
	if len(sets) != 0 {
		return t, out, nil
	}
	rules, err := c.GetRules(t, ch)
	if err != nil {
		return t, out, fmt.Errorf("list ingress rules: %w", err)
	}
	out.RuleCount = len(rules)
	if len(rules) != 1 || !isExpectedRule(rules[0]) {
		return t, out, nil
	}
	out.ExactShape = true
	return t, out, nil
}

func isExpectedRule(rule *nftables.Rule) bool {
	if rule == nil || rule.Table == nil || rule.Table.Name != tableName || rule.Table.Family != nftables.TableFamilyINet || rule.Chain == nil || rule.Chain.Name != chainName || rule.Flags != 0 || len(rule.UserData) != 0 || len(rule.Exprs) != 3 {
		return false
	}
	// nftables' rule decoder omits the chain's table pointer after GetRules(t, c).
	// If a future decoder supplies it, still require that it identifies this table.
	if rule.Chain.Table != nil && (rule.Chain.Table.Name != tableName || rule.Chain.Table.Family != nftables.TableFamilyINet) {
		return false
	}
	meta, ok := rule.Exprs[0].(*expr.Meta)
	if !ok || meta.Key != expr.MetaKeyIIFNAME || meta.Register != 1 || meta.SourceRegister {
		return false
	}
	cmp, ok := rule.Exprs[1].(*expr.Cmp)
	if !ok || cmp.Op != expr.CmpOpEq || cmp.Register != 1 || len(cmp.Data) != 16 {
		return false
	}
	want := make([]byte, 16)
	copy(want, wgName)
	for i := range want {
		if cmp.Data[i] != want[i] {
			return false
		}
	}
	verdict, ok := rule.Exprs[2].(*expr.Verdict)
	return ok && verdict.Kind == expr.VerdictDrop
}

// Some kernels expose an omitted base-chain policy as an explicit ACCEPT on readback.
// That is the same behavior as the guard chain created by the daemon; other policies
// alter packet handling and therefore remain an unknown shape.
func isDefaultAcceptPolicy(policy *nftables.ChainPolicy) bool {
	return policy == nil || *policy == nftables.ChainPolicyAccept
}
