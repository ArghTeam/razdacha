package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestPendingUsesPersistedPolicyRepresentation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parsed json.RawMessage
	}{
		{name: "nil parsed JSON"},
		{name: "indented parsed JSON", parsed: json.RawMessage("{\n  \"server\": \"127.0.0.1\",\n  \"server_port\": 19080,\n  \"version\": \"5\"\n}")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, _ := openStore(t)
			target, err := st.CreateTunnel(ctx, store.Tunnel{Name: "Synthetic exit", Type: store.TunnelSOCKS, Source: store.SourceURL, Raw: "synthetic", Parsed: tc.parsed})
			if err != nil {
				t.Fatal(err)
			}
			peer, err := api.CreatePeer(ctx, st, "Synthetic client", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			rule, err := st.CreateRule(ctx, store.Rule{Name: "Synthetic rule", Action: store.ActionBlock, Domains: []string{"blocked.example"}, PeerScope: store.ScopeAll, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			f := newPublicationFixture(t, nil)
			check := func(want bool) {
				t.Helper()
				got, err := f.g.Pending(ctx, st)
				if err != nil || got != want {
					t.Fatalf("pending=%v want=%v err=%v", got, want, err)
				}
			}
			check(true)
			if _, err := f.g.ApplyCurrent(ctx, st); err != nil {
				t.Fatal(err)
			}
			check(false)
			// A new gate must recover the durable generation without inheriting
			// an in-memory representation from the successful Apply.
			restored := newPublicationFixture(t, nil)
			restored.g.network.path = f.g.network.path
			f = restored
			if err := f.g.recover(ctx, filepath.Join(filepath.Dir(f.g.network.path), "absent-seed")); err != nil {
				t.Fatal(err)
			}
			check(false)
			f.g.network.uncertain = true
			check(true)
			f.g.network.uncertain = false
			f.g.network.initialized = false
			check(true)
			f.g.network.initialized = true
			peer.EgressTunnelID = &target.ID
			if err := st.UpdatePeer(ctx, peer); err != nil {
				t.Fatal(err)
			}
			check(true)
			peer.EgressTunnelID = nil
			if err := st.UpdatePeer(ctx, peer); err != nil {
				t.Fatal(err)
			}
			check(false)
			rule.Domains = []string{"changed.example"}
			if err := st.UpdateRule(ctx, rule); err != nil {
				t.Fatal(err)
			}
			check(true)
			if _, err := f.g.ApplyCurrent(ctx, st); err != nil {
				t.Fatal(err)
			}
			check(false)
		})
	}
}

func TestPendingReturnsPolicyEncodingErrors(t *testing.T) {
	for _, side := range []string{"applied", "editable"} {
		t.Run(side, func(t *testing.T) {
			ctx := context.Background()
			st, _ := openStore(t)
			tunnel, err := st.CreateTunnel(ctx, store.Tunnel{Name: "Synthetic exit", Type: store.TunnelSOCKS, Source: store.SourceURL, Raw: "synthetic"})
			if err != nil {
				t.Fatal(err)
			}
			f := newPublicationFixture(t, nil)
			if _, err := f.g.ApplyCurrent(ctx, st); err != nil {
				t.Fatal(err)
			}
			if side == "applied" {
				f.g.base.Tunnels[0].Parsed = json.RawMessage(`{`)
			} else {
				// Simulate corrupt persisted data; normal store writes reject it.
				if _, err := st.DB().ExecContext(ctx, "UPDATE tunnels SET parsed = ? WHERE id = ?", "{", tunnel.ID); err != nil {
					t.Fatal(err)
				}
			}
			_, err = f.g.Pending(ctx, st)
			var marshalErr *json.MarshalerError
			if !errors.As(err, &marshalErr) || !strings.Contains(err.Error(), "encode "+side+" policy") {
				t.Fatalf("expected wrapped %s policy encoding error, got %v", side, err)
			}
		})
	}
}
