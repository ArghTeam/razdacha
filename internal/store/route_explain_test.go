package store

import (
	"context"
	"errors"
	"testing"
)

func TestRouteEligibilityDoesNotSelectPrivateColumns(t *testing.T) {
	ctx := context.Background()
	st := openAt(t, t.TempDir()+"/state.db")
	cfg := DefaultSettings()
	cfg.RealityEnabled = true
	cfg.RealityServerName = "fixture.example"
	cfg.RealityHandshakeServer = "127.0.0.1"
	if err := st.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	p := samplePeer("Synthetic client", "10.8.0.2")
	p.RealityEnabled = true
	p, err := st.CreatePeer(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.db.ExecContext(ctx, `UPDATE reality_identity SET private_key='invalid-private-material'`); err != nil {
		t.Fatal(err)
	}
	out, err := st.RouteEligibility(ctx, p.ID)
	if err != nil || !out.HasCredential || out.IdentityID == "" || out.Peer.PrivateKey != "" || out.Peer.PresharedKey != "" {
		t.Fatalf("metadata=%+v error=%v", out, err)
	}
	// Removing these columns in this disposable database makes accidental reads
	// fail at SQLite preparation, even if callers would later discard the values.
	for _, statement := range []string{`ALTER TABLE peers DROP COLUMN private_key`, `ALTER TABLE peers DROP COLUMN preshared_key`, `ALTER TABLE reality_identity DROP COLUMN private_key`, `ALTER TABLE reality_identity DROP COLUMN short_id`} {
		if _, err = st.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	out, err = st.RouteEligibility(ctx, p.ID)
	if err != nil || out.Credential.CredentialID != p.RealityCredentialID {
		t.Fatalf("public-column metadata failed: %v", err)
	}
	_, err = st.RouteEligibility(ctx, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
