package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPeerEgressReferencesAndBackup(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	target, err := s.CreateTunnel(ctx, sampleTunnel("Test exit"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePeer(ctx, samplePeer("Client", "10.8.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	if p.EgressTunnelID != nil {
		t.Fatal("new peer did not default to shared rules")
	}
	p.EgressTunnelID = &target.ID
	if err := s.UpdatePeer(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.Peer(ctx, p.ID)
	if err != nil || got.EgressTunnelID == nil || *got.EgressTunnelID != target.ID {
		t.Fatalf("choice not persisted: %v", err)
	}
	if err := s.DeleteTunnel(ctx, target.ID); !errors.Is(err, ErrInUse) {
		t.Fatalf("referenced tunnel deletion: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tunnels WHERE id = ?`, target.ID); err == nil {
		t.Fatal("FK did not restrict direct deletion")
	}
	target.Enabled = false
	if err := s.UpdateTunnel(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePeer(ctx, p); err != nil {
		t.Fatal("disabled choice must remain valid", err)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	restored := openAt(t, backup)
	got, err = restored.Peer(ctx, p.ID)
	if err != nil || got.EgressTunnelID == nil || *got.EgressTunnelID != target.ID {
		t.Fatal("backup lost choice", err)
	}
	missing := "missing"
	p.EgressTunnelID = &missing
	if err := s.UpdatePeer(ctx, p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown target: %v", err)
	}
	empty := ""
	p.EgressTunnelID = &empty
	if err := s.UpdatePeer(ctx, p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty target: %v", err)
	}
	p.EgressTunnelID = nil
	if err := s.UpdatePeer(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTunnel(ctx, target.ID); err != nil {
		t.Fatal("cleared target still referenced", err)
	}
	p.EgressTunnelID = &target.ID
	if _, err := s.CreatePeer(ctx, p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("stale create accepted deleted target: %v", err)
	}
}

func TestPeerEgressMigrationPreservesExistingClient(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s := openAt(t, path)
	p, err := s.CreatePeer(ctx, samplePeer("Legacy client", "10.8.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.ExecContext(ctx, `DROP INDEX peers_egress_tunnel; ALTER TABLE peers DROP COLUMN egress_tunnel_id; PRAGMA user_version = 11;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openAt(t, path)
	got, err := s.Peer(ctx, p.ID)
	if err != nil || got.EgressTunnelID != nil || got.PrivateKey != p.PrivateKey || got.Address != p.Address || got.Enabled != p.Enabled {
		t.Fatalf("migration changed client: %+v %v", got, err)
	}
}

func TestPeerPatchOmittedRoutingPreservesConcurrentSelection(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	target, err := s.CreateTunnel(ctx, sampleTunnel("Concurrent exit"))
	if err != nil {
		t.Fatal(err)
	}
	peer, err := s.CreatePeer(ctx, samplePeer("Before", "10.8.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	// A metadata request is prepared before a concurrent routing write commits.
	name := "After"
	enabled := false
	patch := PeerPatch{Name: &name, Enabled: &enabled}
	held, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	waits := s.db.Stats().WaitCount
	done := make(chan error, 1)
	go func() { _, err := s.PatchPeer(ctx, peer.ID, patch); done <- err }()
	deadline := time.Now().Add(time.Second)
	for s.db.Stats().WaitCount == waits && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.db.Stats().WaitCount == waits {
		t.Fatal("metadata patch did not wait behind routing transaction")
	}
	if _, err := held.ExecContext(ctx, `UPDATE peers SET egress_tunnel_id = ? WHERE id = ?`, target.ID, peer.ID); err != nil {
		t.Fatal(err)
	}
	if err := held.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := s.Peer(ctx, peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EgressTunnelID == nil || *got.EgressTunnelID != target.ID || got.Name != name || got.Enabled {
		t.Fatal("omitted metadata patch overwrote concurrent routing selection")
	}
	cleared, err := s.PatchPeer(ctx, peer.ID, PeerPatch{SetEgressTunnel: true})
	if err != nil || cleared.EgressTunnelID != nil {
		t.Fatal("explicit shared mode not preserved", err)
	}
}
