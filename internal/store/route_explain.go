package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// RouteEligibility contains only current admission metadata. Peer is populated
// from explicit public columns; private WG keys, REALITY UUIDs and server keys
// are neither selected nor derived by this read-only transaction.
type RouteEligibility struct {
	Settings      Settings
	Peer          Peer
	IdentityID    string
	Credential    RealityAdmission
	HasCredential bool
}

// RouteEligibility reads current admission metadata without exporting credentials.
func (s *Store) RouteEligibility(ctx context.Context, id string) (out RouteEligibility, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var e error
		out.Settings, e = settings(ctx, tx)
		if e != nil {
			return e
		}
		p := &out.Peer
		e = tx.QueryRowContext(ctx, `SELECT id,public_key,address,enabled,wireguard_disabled,reality_enabled,reality_credential_id,reality_revision FROM peers WHERE id=?`, id).Scan(&p.ID, &p.PublicKey, &p.Address, &p.Enabled, &p.WireGuardDisabled, &p.RealityEnabled, &p.RealityCredentialID, &p.RealityRevision)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return fmt.Errorf("read route client admission: %w", e)
		}
		e = tx.QueryRowContext(ctx, `SELECT identity_id FROM reality_identity WHERE singleton=1`).Scan(&out.IdentityID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return fmt.Errorf("read route identity metadata: %w", e)
		}
		a := &out.Credential
		e = tx.QueryRowContext(ctx, `SELECT peer_id,credential_id,revision FROM reality_credentials WHERE peer_id=? AND uuid<>''`, id).Scan(&a.PeerID, &a.CredentialID, &a.Revision)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return fmt.Errorf("read route credential metadata: %w", e)
		}
		out.HasCredential = true
		return nil
	})
	return
}
