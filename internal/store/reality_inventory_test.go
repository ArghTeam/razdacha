package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

type inventoryCountingQuerier struct {
	querier
	queries int
}

func (q *inventoryCountingQuerier) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.queries++
	return q.querier.QueryContext(ctx, query, args...)
}

func (q *inventoryCountingQuerier) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	q.queries++
	return q.querier.QueryRowContext(ctx, query, args...)
}

func TestRealityInventoryUsesFourQueriesForOneAndHundredClients(t *testing.T) {
	for _, count := range []int{1, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx := context.Background()
			st := openAt(t, t.TempDir()+"/state.db")
			cfg := DefaultSettings()
			cfg.RealityEnabled = true
			cfg.RealityServerName = "fixture.example"
			cfg.RealityHandshakeServer = "127.0.0.1"
			if err := st.SaveSettings(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < count; i++ {
				p := samplePeer(fmt.Sprintf("Client %d", i), fmt.Sprintf("10.8.0.%d", i+2))
				p.RealityEnabled = true
				if _, err := st.CreatePeer(ctx, p); err != nil {
					t.Fatal(err)
				}
			}
			err := st.tx(ctx, func(tx *sql.Tx) error {
				q := &inventoryCountingQuerier{querier: tx}
				state, err := readRealityInventory(ctx, q)
				if err != nil {
					return err
				}
				if q.queries != 4 || len(state.Peers) != count || len(state.Credentials) != count {
					t.Fatalf("queries=%d peers=%d credentials=%d", q.queries, len(state.Peers), len(state.Credentials))
				}
				for _, p := range state.Peers {
					a := state.Credentials[p.ID]
					if a.CredentialID != p.RealityCredentialID || a.Revision != p.RealityRevision {
						t.Fatal("incoherent credential revision")
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
