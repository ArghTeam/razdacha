//go:build linux

package netstack

import (
	"fmt"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

// ClientGuardTable is separate from the replaceable routing table. It survives
// failed publication and daemon restart until an explicit successful recovery.
const ClientGuardTable = "razdacha_guard"

// SetClientGuard atomically replaces/removes the early client-ingress barrier.
// Each call uses a fresh connection: a failed netlink batch must never be reused.
func SetClientGuard(enabled bool) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open client guard connection: %w", err)
	}
	tables, err := conn.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return fmt.Errorf("read client guard table: %w", err)
	}
	for _, table := range tables {
		if table.Name == ClientGuardTable {
			conn.DelTable(table)
		}
	}
	if enabled {
		table := conn.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: ClientGuardTable})
		chain := conn.AddChain(&nftables.Chain{Table: table, Name: "ingress", Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookPrerouting, Priority: nftables.ChainPriorityRef(-310)})
		name := make([]byte, 16)
		copy(name, DefaultWGInterface)
		conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: name},
			&expr.Verdict{Kind: expr.VerdictDrop},
		}})
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("publish client ingress guard: %w", err)
	}
	return nil
}
