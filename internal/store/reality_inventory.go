package store

import (
	"context"
	"database/sql"
)

// RealityInventoryState is request-local eligibility metadata, never a bearer export.
// Read it under the publication gate. REALITY UUIDs/server private keys are not retained.
// Peer rows still contain private WG key material and must never be serialized.
type RealityInventoryState struct {
	Settings                       Settings
	Peers                          []Peer
	IdentityID, PublicKey, ShortID string
	Credentials                    map[string]RealityAdmission
}

// RealityInventory reads all eligibility metadata in one transaction and four
// bounded queries, independent of the number of clients.
func (s *Store) RealityInventory(ctx context.Context) (out RealityInventoryState, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var e error
		out, e = readRealityInventory(ctx, tx)
		return e
	})
	return
}

func readRealityInventory(ctx context.Context, q querier) (RealityInventoryState, error) {
	var out RealityInventoryState
	var err error
	out.Settings, err = settings(ctx, q)
	if err != nil {
		return out, err
	}
	out.Peers, err = peers(ctx, q)
	if err != nil {
		return out, err
	}
	identity, err := realityIdentity(ctx, q)
	if err != nil {
		return out, err
	}
	if identity != nil {
		out.IdentityID, out.PublicKey, out.ShortID = identity.ID, identity.PublicKey, identity.ShortID
	}
	out.Credentials = make(map[string]RealityAdmission)
	rows, err := q.QueryContext(ctx, `SELECT peer_id, credential_id, revision FROM reality_credentials WHERE uuid <> ''`)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a RealityAdmission
		if err := rows.Scan(&a.PeerID, &a.CredentialID, &a.Revision); err != nil {
			return out, err
		}
		out.Credentials[a.PeerID] = a
	}
	return out, rows.Err()
}
