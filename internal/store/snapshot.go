package store

import (
	"context"
	"database/sql"
	"errors"
)

// Snapshot читает полное состояние в одной транзакции. Конфиг sing-box генерируется
// целиком и никогда не патчится частично — значит и читать состояние надо целиком,
// иначе в конфиг попадёт правило на туннель, удалённый между двумя запросами.
func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	var snap Snapshot
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		if snap.Settings, err = settings(ctx, tx); err != nil {
			return err
		}
		if snap.Tunnels, err = tunnels(ctx, tx); err != nil {
			return err
		}
		if snap.Rules, err = rules(ctx, tx); err != nil {
			return err
		}
		snap.Peers, err = peers(ctx, tx)
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `SELECT identity_id FROM reality_identity WHERE singleton=1`).Scan(&snap.RealityIdentityID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		for _, p := range snap.Peers {
			if p.RealityEnabled && p.RealityCredentialID != "" {
				snap.RealityAdmissions = append(snap.RealityAdmissions, RealityAdmission{PeerID: p.ID, CredentialID: p.RealityCredentialID, Revision: p.RealityRevision})
			}
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}
