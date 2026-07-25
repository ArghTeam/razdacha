package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// open поднимает БД во временном каталоге: тесты не требуют root и внешних сервисов.
func open(t *testing.T) *Store {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "razdacha.db"))
}

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func TestOpenCreatesSchema(t *testing.T) {
	s := open(t)

	v, err := s.userVersion(context.Background())
	if err != nil {
		t.Fatalf("userVersion: %v", err)
	}
	if v != schemaVersion() {
		t.Fatalf("версия схемы = %d, ожидалась %d", v, schemaVersion())
	}

	for _, table := range []string{"tunnels", "rules", "peers", "settings", "sessions"} {
		var name string
		err := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("таблица %s не создана: %v", table, err)
		}
	}
}

// Повторный прогон миграций на актуальной БД ничего не меняет.
func TestMigrationsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "razdacha.db")

	s := openAt(t, path)
	tunnel, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	before := schemaDump(t, s)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("повторный Open: %v", err)
	}
	defer again.Close()

	if got := schemaDump(t, again); got != before {
		t.Errorf("схема изменилась после повторного открытия:\nбыло:\n%s\nстало:\n%s", before, got)
	}
	if _, err := again.Tunnel(ctx, tunnel.ID); err != nil {
		t.Errorf("данные не пережили повторную миграцию: %v", err)
	}

	// Миграции применены один раз — версия та же, шаг не повторился.
	v, err := again.userVersion(ctx)
	if err != nil {
		t.Fatalf("userVersion: %v", err)
	}
	if v != schemaVersion() {
		t.Errorf("версия схемы = %d, ожидалась %d", v, schemaVersion())
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "razdacha.db")

	s := openAt(t, path)
	if _, err := s.db.ExecContext(ctx, "PRAGMA user_version = 999"); err != nil {
		t.Fatalf("подмена версии схемы: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := Open(ctx, path)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid для БД из будущего, получено: %v", err)
	}
}

// В БД лежат приватные ключи пиров — файл не должен читаться кем попало.
func TestDatabaseFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "razdacha.db")
	openAt(t, path)

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != dbFileMode {
		t.Errorf("права на файл БД = %o, ожидались %o", perm, dbFileMode)
	}

	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat каталога: %v", err)
	}
	if perm := di.Mode().Perm(); perm != dbDirMode {
		t.Errorf("права на каталог = %o, ожидались %o", perm, dbDirMode)
	}
}

// Без включённого foreign_keys ON DELETE RESTRICT не действует.
func TestForeignKeysEnabled(t *testing.T) {
	s := open(t)

	var on int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&on); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if on != 1 {
		t.Fatal("foreign_keys выключен — ON DELETE RESTRICT не работает")
	}
}

func TestSnapshotReadsEverything(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	tunnel, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if _, err := s.CreateRule(ctx, sampleRule("YouTube", tunnel.ID)); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if _, err := s.CreatePeer(ctx, samplePeer("iPhone", "10.8.0.2/32")); err != nil {
		t.Fatalf("CreatePeer: %v", err)
	}

	snap, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tunnels) != 1 || len(snap.Rules) != 1 || len(snap.Peers) != 1 {
		t.Fatalf("снимок неполный: %d туннелей, %d правил, %d пиров",
			len(snap.Tunnels), len(snap.Rules), len(snap.Peers))
	}
	if snap.Settings.ClientMTU != 1280 {
		t.Errorf("MTU в снимке = %d, ожидался 1280", snap.Settings.ClientMTU)
	}
}

// schemaDump собирает DDL всех объектов схемы для сравнения «до/после».
func schemaDump(t *testing.T, s *Store) string {
	t.Helper()
	rows, err := s.db.Query(
		`SELECT COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`,
	)
	if err != nil {
		t.Fatalf("чтение схемы: %v", err)
	}
	defer rows.Close()

	var dump string
	for rows.Next() {
		var sql string
		if err := rows.Scan(&sql); err != nil {
			t.Fatalf("чтение схемы: %v", err)
		}
		dump += sql + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("чтение схемы: %v", err)
	}
	return dump
}
