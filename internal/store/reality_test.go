package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRealityAdmissionDoesNotRestoreRevokedAuthentication(t *testing.T) {
	ctx := context.Background()
	st := openAt(t, t.TempDir()+"/state.db")
	settings := DefaultSettings()
	settings.RealityEnabled = true
	settings.RealityServerName = "fixture.example"
	settings.RealityHandshakeServer = "127.0.0.1"
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	p := samplePeer("Client", "10.8.0.2")
	p.RealityEnabled = true
	p, err := st.CreatePeer(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := st.RealityRuntime(ctx, before)
	if err != nil || len(runtime.Users) != 1 {
		t.Fatalf("initial live admission: %v", err)
	}
	secret := runtime.Users[0].UUID
	key := runtime.Identity.PrivateKey
	record, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{secret, key} {
		if strings.Contains(string(record), private) {
			t.Fatal("private Reality material escaped into authority")
		}
	}
	next, err := st.RotateReality(ctx, p.ID, p.RealityRevision)
	if err != nil {
		t.Fatal(err)
	}
	if next.RealityRevision != p.RealityRevision+1 {
		t.Fatal("rotation did not advance revision")
	}
	if _, err := st.RotateReality(ctx, p.ID, p.RealityRevision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale rotation accepted")
	}
	runtime, err = st.RealityRuntime(ctx, before)
	if err != nil || len(runtime.Users) != 0 {
		t.Fatal("historical policy restored rotated credential", err)
	}
	applied, _ := st.Snapshot(ctx)
	runtime, err = st.RealityRuntime(ctx, applied)
	if err != nil || len(runtime.Users) != 1 || runtime.Users[0].UUID == secret {
		t.Fatal("fresh Apply admission lost replacement", err)
	}
	disabled := false
	p, err = st.PatchPeer(ctx, p.ID, PeerPatch{RealityEnabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	_, err = st.PatchPeer(ctx, p.ID, PeerPatch{RealityEnabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = st.RealityRuntime(ctx, applied)
	if err != nil || len(runtime.Users) != 0 {
		t.Fatal("re-enable resurrected prior admission", err)
	}
	fresh, _ := st.Snapshot(ctx)
	settings.RealityEnabled = false
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	settings.RealityEnabled = true
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	runtime, err = st.RealityRuntime(ctx, fresh)
	if err != nil || len(runtime.Users) != 0 {
		t.Fatal("global re-enable resurrected prior admission", err)
	}
}

func TestRestoreInvalidatesOnlyRealityIdentity(t *testing.T) {
	ctx := context.Background()
	st := openAt(t, t.TempDir()+"/state.db")
	settings := DefaultSettings()
	settings.RealityEnabled = true
	settings.RealityServerName = "fixture.example"
	settings.RealityHandshakeServer = "127.0.0.1"
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	p := samplePeer("Client", "10.8.0.2")
	p.RealityEnabled = true
	p, err := st.CreatePeer(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := st.RealityIdentity(ctx)
	if err := st.ResetRealityAfterRestore(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.Peer(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PrivateKey != p.PrivateKey || got.PublicKey != p.PublicKey || got.PresharedKey != p.PresharedKey || got.Address != p.Address || got.Enabled != p.Enabled || got.WireGuardDisabled != p.WireGuardDisabled {
		t.Fatal("restore changed WG identity")
	}
	if got.RealityEnabled || got.RealityRevision != 0 || got.RealityCredentialID != "" {
		t.Fatal("restore retained public admission")
	}
	identity, err := st.RealityIdentity(ctx)
	if err != nil || identity != nil {
		t.Fatal("restore retained copied server identity")
	}
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	identity, err = st.RealityIdentity(ctx)
	if err != nil || identity == nil || identity.PrivateKey == before.PrivateKey {
		t.Fatal("explicit reconfiguration reused cloned identity")
	}
}

func TestLegacyFullPeerUpdateCannotBypassRealityLifecycle(t *testing.T) {
	ctx := context.Background()
	st := openAt(t, t.TempDir()+"/state.db")
	p := samplePeer("Client", "10.8.0.2")
	p.RealityEnabled = true
	p, err := st.CreatePeer(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Peer){func(p *Peer) { p.RealityEnabled = false }, func(p *Peer) { p.Enabled = false }, func(p *Peer) { p.WireGuardDisabled = true }} {
		next := p
		change(&next)
		if err := st.UpdatePeer(ctx, next); !errors.Is(err, ErrInvalid) {
			t.Fatal("full update bypassed public auth lifecycle", err)
		}
	}
	got, err := st.Peer(ctx, p.ID)
	if err != nil || got.RealityRevision != p.RealityRevision || !got.RealityEnabled || !got.Enabled {
		t.Fatal("rejected full write changed authentication")
	}
	p.Name = "Renamed"
	if err := st.UpdatePeer(ctx, p); err != nil {
		t.Fatal("ordinary rename was blocked", err)
	}
	legacy := samplePeer("Legacy", "10.8.0.3")
	legacy, err = st.CreatePeer(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Enabled = false
	if err := st.UpdatePeer(ctx, legacy); err != nil {
		t.Fatal("legacy WG-only write changed behavior", err)
	}
}
