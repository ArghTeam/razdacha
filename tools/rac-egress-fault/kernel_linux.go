//go:build linux

package main

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

type kernelFirewall struct{}

func newFirewall() (*kernelFirewall, error) { return &kernelFirewall{}, nil }
func (*kernelFirewall) close()              {}
func faultTable(p plan) *nftables.Table {
	return &nftables.Table{Name: p.table(), Family: nftables.TableFamilyINet}
}

func faultChain(t *nftables.Table) *nftables.Chain {
	policy := nftables.ChainPolicyAccept
	return &nftables.Chain{Name: "output", Table: t, Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityRef(-190), Policy: &policy}
}

func faultRule(p plan, t *nftables.Table, c *nftables.Chain) *nftables.Rule {
	return &nftables.Rule{Table: t, Chain: c, UserData: p.marker(), Exprs: []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV4}},
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{165, 22, 196, 130}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(uint16(p.Port))},
		&expr.Counter{}, &expr.Verdict{Kind: expr.VerdictDrop},
	}}
}

func (*kernelFirewall) install(p plan) error {
	c, e := nftables.New()
	if e != nil {
		return e
	}
	return installKernel(c, p)
}

func installKernel(c *nftables.Conn, p plan) error {
	// EXCL prevents a table created after the preceding dump being adopted.
	table := c.CreateTable(faultTable(p))
	chain := c.AddChain(faultChain(table))
	c.AddRule(faultRule(p, table, chain))
	if e := c.Flush(); e != nil {
		return fmt.Errorf("exclusive fixture batch: %w", e)
	}
	return nil
}

func tableNamed(c *nftables.Conn, p plan) (*nftables.Table, error) {
	tables, e := c.ListTablesOfFamily(nftables.TableFamilyINet)
	if e != nil {
		return nil, e
	}
	for _, t := range tables {
		if t.Name == p.table() {
			if t.Flags != 0 {
				return nil, errors.New("fixture table flags differ")
			}
			return t, nil
		}
	}
	return nil, nil
}

func verifyRule(p plan, r *nftables.Rule) (counters, error) {
	if !bytes.Equal(r.UserData, p.marker()) {
		return counters{}, errors.New("fixture rule ownership differs")
	}
	expected := faultRule(p, r.Table, r.Chain).Exprs
	if len(r.Exprs) != len(expected) {
		return counters{}, errors.New("fixture rule expression count differs")
	}
	var out counters
	for i, current := range r.Exprs {
		if count, ok := current.(*expr.Counter); ok {
			if _, wanted := expected[i].(*expr.Counter); !wanted {
				return out, errors.New("fixture counter position differs")
			}
			out = counters{count.Packets, count.Bytes}
			continue
		}
		if !reflect.DeepEqual(current, expected[i]) {
			return out, fmt.Errorf("fixture rule predicate %d differs", i)
		}
	}
	return out, nil
}

func inspectKernel(c *nftables.Conn, p plan) (counters, *nftables.Table, error) {
	t, e := tableNamed(c, p)
	if e != nil || t == nil {
		return counters{}, t, e
	}
	chains, e := c.ListChainsOfTableFamily(nftables.TableFamilyINet)
	if e != nil {
		return counters{}, nil, e
	}
	var owned []*nftables.Chain
	for _, ch := range chains {
		if ch.Table != nil && ch.Table.Name == p.table() {
			owned = append(owned, ch)
		}
	}
	if len(owned) != 1 {
		return counters{}, nil, errors.New("fixture table chain count differs")
	}
	ch := owned[0]
	want := faultChain(t)
	if ch.Name != want.Name || ch.Type != want.Type || ch.Hooknum == nil || *ch.Hooknum != *want.Hooknum || ch.Priority == nil || *ch.Priority != *want.Priority || ch.Policy == nil || *ch.Policy != *want.Policy || ch.Device != "" {
		return counters{}, nil, errors.New("fixture chain schema differs")
	}
	sets, e := c.GetSets(t)
	if e != nil {
		return counters{}, nil, e
	}
	if len(sets) != 0 {
		return counters{}, nil, errors.New("fixture table contains foreign sets")
	}
	objects, e := c.GetObjects(t)
	if e != nil {
		return counters{}, nil, e
	}
	if len(objects) != 0 {
		return counters{}, nil, errors.New("fixture table contains foreign objects")
	}
	flows, e := c.ListFlowtables(t)
	if e != nil {
		return counters{}, nil, e
	}
	if len(flows) != 0 {
		return counters{}, nil, errors.New("fixture table contains foreign flowtables")
	}
	rules, e := c.GetRules(t, ch)
	if e != nil {
		return counters{}, nil, e
	}
	if len(rules) != 1 {
		return counters{}, nil, errors.New("fixture chain rule count differs")
	}
	count, e := verifyRule(p, rules[0])
	return count, t, e
}

func (*kernelFirewall) inspect(p plan) (counters, bool, error) {
	c, e := nftables.New()
	if e != nil {
		return counters{}, false, e
	}
	n, t, e := inspectKernel(c, p)
	return n, t != nil, e
}

func (*kernelFirewall) remove(p plan) error {
	c, e := nftables.New()
	if e != nil {
		return e
	}
	_, t, e := inspectKernel(c, p)
	if e != nil {
		return e
	}
	if t == nil {
		return nil
	}
	// All fixture operations share the journal lock. Never flush an application
	// table or delete an observed table with a mismatched rule/owner/schema.
	c.DelTable(t)
	return c.Flush()
}
