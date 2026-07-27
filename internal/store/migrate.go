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
	{
		// Встроенная запись: её заводит демон, а не пользователь, поэтому её
		// не удаляют, а выключают. Колонка, а не вывод признака из совпадения
		// raw с каталогом по умолчанию: свой пул по тому же адресу — законный
		// случай, а адрес по умолчанию ещё и меняется между релизами.
		version: 4,
		stmts: `
ALTER TABLE tunnels ADD COLUMN builtin INTEGER NOT NULL DEFAULT 0;
`,
	},
	{
		// Отдельная таблица, а не колонки в tunnels: состояние проверки не
		// часть туннеля, а наблюдение о нём, и в Snapshot оно попадать не
		// должно — оттуда генерируется конфиг sing-box.
		//
		// Задержки здесь нет намеренно: она живёт минуты и после перезапуска
		// sing-box уже ничего не значит, тогда как «был down в 06:40» рядом с
		// временем остаётся фактом (ADR 0011).
		//
		// ON DELETE CASCADE: удалённый туннель не должен держать за собой
		// запись о проверке.
		version: 5,
		stmts: `
CREATE TABLE tunnel_checks (
  tunnel_id TEXT PRIMARY KEY REFERENCES tunnels(id) ON DELETE CASCADE,
  status TEXT NOT NULL,
  checked_at INTEGER NOT NULL);
`,
	},
	{
		// О чём уже сообщили в телеграм. В памяти этого держать нельзя: после
		// перезапуска демона переход объявлялся бы заново, и владелец получал
		// бы «туннель не отвечает» о туннеле, про который ему уже написали.
		//
		// Счётчики подтверждения и окно нестабильности, наоборот, остаются в
		// памяти: после перезапуска туннель должен снова набрать три проверки,
		// и это честнее, чем объявить переход по одному наблюдению.
		version: 6,
		stmts: `
ALTER TABLE tunnel_checks ADD COLUMN notified_status TEXT NOT NULL DEFAULT '';
`,
	},
	{
		// WARP получает свой source (ADR 0012). Схема не меняется — колонка
		// source и так текстовая, — но туннели, заведённые вставленным .conf до
		// этой версии, обязаны получить признак: по нему выбирается второе звено
		// цепи, и без шага миграции пользователю пришлось бы пересохранять
		// туннель руками, чтобы его WARP снова стал WARP'ом.
		//
		// Признак берётся из хоста endpoint — того же, по которому его узнаёт
		// разбор вставленного конфига. Наружу здесь никто не ходит: сравнение
		// идёт по строке, которая уже лежит в БД.
		version: 7,
		stmts: `
UPDATE tunnels SET source = 'warp'
 WHERE source = 'wg_conf' AND type = 'wireguard'
   AND lower(raw) LIKE '%cloudflareclient.com%';
`,
	},
	{
		// Второе звено цепи у правила (ADR 0012): куда трафик уходит после
		// первого туннеля. NULL — цепи нет, и правило работает ровно как до
		// этой версии, поэтому шаг ничего не переписывает.
		//
		// Тот же ON DELETE RESTRICT, что и у первого звена: ссылка на туннель
		// вторым звеном держит его так же крепко, как ссылка первым.
		version: 8,
		stmts: `
ALTER TABLE rules ADD COLUMN via_tunnel_id TEXT REFERENCES tunnels(id) ON DELETE RESTRICT;
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
