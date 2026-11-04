//go:build linux

package netstack

import (
	"encoding/binary"
	"fmt"

	"github.com/ArghTeam/razdacha/internal/ingressguard"
	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// SetPublicClientGuard atomically replaces/removes the public TCP input barrier.
// INPUT scopes matches to local delivery; forwarded traffic is never captured.
// An early hook also covers established packets before ordinary accept rules.
// Callers serialize this and manifest publication with ingressguard.WithLock.
func SetPublicClientGuard(endpoints []ingressguard.Endpoint, enabled bool) error {
	rules, err := publicGuardPlan(endpoints)
	if err != nil {
		return err
	}
	connection, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open public ingress guard connection: %w", err)
	}
	tables, err := connection.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return fmt.Errorf("read public ingress guard table: %w", err)
	}
	for _, table := range tables {
		if table.Name == PublicClientGuardTable {
			connection.DelTable(table)
		}
	}
	if enabled && len(rules) > 0 {
		table := connection.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: PublicClientGuardTable})
		chain := connection.AddChain(&nftables.Chain{Table: table, Name: "input", Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookInput, Priority: nftables.ChainPriorityRef(-310)})
		for _, rule := range rules {
			connection.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: publicGuardExpressions(rule)})
		}
	}
	if err := connection.Flush(); err != nil {
		return fmt.Errorf("publish public ingress guard: %w", err)
	}
	return nil
}

func publicGuardExpressions(rule publicGuardRule) []expr.Any {
	family := byte(unix.NFPROTO_IPV4)
	if rule.family == 6 {
		family = byte(unix.NFPROTO_IPV6)
	}
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, rule.port)
	expressions := []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{family}},
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: port},
	}
	if rule.address.IsValid() {
		offset, length := uint32(16), uint32(4)
		if rule.family == 6 {
			offset, length = 24, 16
		}
		expressions = append(expressions,
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: length},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: rule.address.AsSlice()},
		)
	}
	return append(expressions, &expr.Verdict{Kind: expr.VerdictDrop})
}
