//go:build linux

package main

import (
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

type fixture struct {
	table *nftables.Table
	chain *nftables.Chain
	rules []*nftables.Rule
	sets  []*nftables.Set
}

func (f *fixture) ListTablesOfFamily(nftables.TableFamily) ([]*nftables.Table, error) {
	if f.table == nil {
		return nil, nil
	}
	return []*nftables.Table{f.table}, nil
}

func (f *fixture) ListChainsOfTableFamily(nftables.TableFamily) ([]*nftables.Chain, error) {
	return []*nftables.Chain{f.chain}, nil
}

func (f *fixture) GetRules(*nftables.Table, *nftables.Chain) ([]*nftables.Rule, error) {
	return f.rules, nil
}
func (f *fixture) GetSets(*nftables.Table) ([]*nftables.Set, error) { return f.sets, nil }
func (f *fixture) DelTable(*nftables.Table)                         {}
func (f *fixture) Flush() error                                     { return nil }
func exactFixture() *fixture {
	table := &nftables.Table{Name: tableName, Family: nftables.TableFamilyINet}
	chain := &nftables.Chain{Name: chainName, Table: table, Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookPrerouting, Priority: nftables.ChainPriorityRef(-310)}
	name := make([]byte, 16)
	copy(name, wgName)
	rule := &nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: name}, &expr.Verdict{Kind: expr.VerdictDrop}}}
	return &fixture{table: table, chain: chain, rules: []*nftables.Rule{rule}}
}

func TestGuardInspectionRefusesForeignShape(t *testing.T) {
	for name, mutate := range map[string]func(*fixture){
		"priority":        func(f *fixture) { f.chain.Priority = nftables.ChainPriorityRef(-300) },
		"extra rule":      func(f *fixture) { f.rules = append(f.rules, f.rules[0]) },
		"foreign set":     func(f *fixture) { f.sets = []*nftables.Set{{Name: "foreign"}} },
		"source register": func(f *fixture) { f.rules[0].Exprs[0].(*expr.Meta).SourceRegister = true },
	} {
		t.Run(name, func(t *testing.T) {
			f := exactFixture()
			mutate(f)
			_, got, err := inspect(f)
			if err != nil || got.ExactShape {
				t.Fatalf("foreign guard accepted: %+v %v", got, err)
			}
		})
	}
}

func TestGuardInspectionAcceptsOnlyExactOrAbsent(t *testing.T) {
	for _, f := range []*fixture{exactFixture(), {}} {
		_, got, err := inspect(f)
		if err != nil || !got.ExactShape {
			t.Fatalf("inspection: %+v %v", got, err)
		}
	}
	f := exactFixture()
	policy := nftables.ChainPolicyAccept
	f.chain.Policy = &policy
	_, got, err := inspect(f)
	if err != nil || !got.ExactShape {
		t.Fatalf("kernel-normalized default policy: %+v %v", got, err)
	}
}
