package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migration — один шаг схемы. Версия хранится в PRAGMA user_version, поэтому шаги
// применяются ровно один раз: повторный запуск на актуальной БД ничего не меняет.
type migration struct {
	version int
	stmts   string
}

// migrations — упорядоченный список шагов. Уже выпущенные шаги не редактируются:
// изменение схемы — всегда новый элемент с версией на единицу больше.
var migrations = []migration{
	{
		version: 1,
		stmts: `
CREATE TABLE tunnels (
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, type TEXT NOT NULL,
  source TEXT NOT NULL, raw TEXT NOT NULL, parsed TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL);

CREATE TABLE rules (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, action TEXT NOT NULL,
  tunnel_id TEXT REFERENCES tunnels(id) ON DELETE RESTRICT,
  priority INTEGER NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  community_lists TEXT NOT NULL DEFAULT '[]', domains TEXT NOT NULL DEFAULT '[]',
  subnets TEXT NOT NULL DEFAULT '[]', remote_lists TEXT NOT NULL DEFAULT '[]',
  peer_scope TEXT NOT NULL DEFAULT 'all', peer_ids TEXT NOT NULL DEFAULT '[]',
  resolve_real_ip INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX rules_priority ON rules(priority);

CREATE TABLE peers (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, public_key TEXT NOT NULL UNIQUE,
  private_key TEXT NOT NULL, preshared_key TEXT NOT NULL,
  address TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL);

CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`,
	},
	{
		version: 2,
		stmts: `
CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY, created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL);
CREATE INDEX sessions_expires_at ON sessions(expires_at);
`,
	},
	{
		// Туннель-пул (ADR 0010): серверы снимаются с каталога, поэтому лежат
		// отдельно от parsed — тот у пула пуст, конфиг участников собирается
		// из ссылок при генерации.
		version: 3,
		stmts: `
ALTER TABLE tunnels ADD COLUMN pool TEXT NOT NULL DEFAULT '[]';
ALTER TABLE tunnels ADD COLUMN pool_updated_at INTEGER NOT NULL DEFAULT 0;
`,
	},
}

// schemaVersion — версия схемы, которую ожидает этот код.
func schemaVersion() int {
	return migrations[len(migrations)-1].version
}

// migrate накатывает недостающие шаги. Каждый шаг идёт в своей транзакции вместе с
// обновлением user_version, поэтому прерванная миграция не оставляет полусхему.
func (s *Store) migrate(ctx context.Context) error {
	current, err := s.userVersion(ctx)
	if err != nil {
		return err
	}
	if current > schemaVersion() {
		return fmt.Errorf("%w: БД версии %d новее, чем понимает эта версия razdacha (%d)",
			ErrInvalid, current, schemaVersion())
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		err := s.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.stmts); err != nil {
				return fmt.Errorf("миграция %d: %w", m.version, err)
			}
			// user_version не принимает параметры, версия — литерал из кода.
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
				return fmt.Errorf("миграция %d: запись версии схемы: %w", m.version, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// userVersion читает текущую версию схемы из БД. Пустая БД отдаёт 0.
func (s *Store) userVersion(ctx context.Context) (int, error) {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("чтение версии схемы: %w", err)
	}
	return v, nil
}
