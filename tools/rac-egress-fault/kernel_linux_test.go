//go:build linux

package main

import (
	"errors"
	"syscall"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

func TestExactRuleRoundtripOwnershipRejectsChangedTuple(t *testing.T) {
	p := fixturePlan()
	table := faultTable(p)
	chain := faultChain(table)
	for _, mutate := range []func(*nftables.Rule){
		func(r *nftables.Rule) { r.UserData = []byte("foreign") },
		func(r *nftables.Rule) {
			r.Exprs[1] = &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV6}}
		},
		func(r *nftables.Rule) {
			r.Exprs[5] = &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{1, 1, 1, 1}}
		},
		func(r *nftables.Rule) { r.Exprs[9] = &expr.Verdict{Kind: expr.VerdictAccept} },
	} {
		r := faultRule(p, table, chain)
		mutate(r)
		if _, e := verifyRule(p, r); e == nil {
			t.Fatal("changed rule accepted")
		}
	}
	r := faultRule(p, table, chain)
	r.Exprs[8] = &expr.Counter{Packets: 3, Bytes: 180}
	count, e := verifyRule(p, r)
	if e != nil || count.Packets != 3 || count.Bytes != 180 {
		t.Fatal("owned counters not read")
	}
}

func TestKernelBatchUsesExclusiveCreate(t *testing.T) {
	saw := false
	c, e := nftables.New(nftables.WithTestDial(func(messages []netlink.Message) ([]netlink.Message, error) {
		for _, m := range messages {
			if m.Header.Type == netlink.HeaderType((unix.NFNL_SUBSYS_NFTABLES<<8)|unix.NFT_MSG_NEWTABLE) {
				saw = true
				if m.Header.Flags&netlink.Excl == 0 {
					t.Error("table creation is not exclusive")
				}
			}
		}
		return nil, syscall.EEXIST
	}))
	if e != nil {
		t.Fatal(e)
	}
	if e = installKernel(c, fixturePlan()); e == nil || !errors.Is(e, syscall.EEXIST) {
		t.Fatal("exclusive collision was not reported")
	}
	if !saw {
		t.Fatal("table creation was not submitted")
	}
}
