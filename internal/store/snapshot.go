package store

import (
	"context"
	"database/sql"
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
		return err
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}
