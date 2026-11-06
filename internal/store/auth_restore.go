package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
)

// ResetSessionsAfterRestore removes transient authentication from a staged backup.
// The restored password remains valid, but saved sessions and verification state do not.
func (s *Store) ResetSessionsAfterRestore(ctx context.Context) error {
	generation := make([]byte, 32)
	if _, err := rand.Read(generation); err != nil {
		return fmt.Errorf("generate restored authentication state: %w", err)
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
			return fmt.Errorf("delete restored sessions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, keyPasswordGeneration, hex.EncodeToString(generation)); err != nil {
			return fmt.Errorf("rotate restored authentication generation: %w", err)
		}
		return nil
	})
}
