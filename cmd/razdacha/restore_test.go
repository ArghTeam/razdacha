package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ArghTeam/razdacha/internal/store"
)

const restorePhrase = "очень длинная фраза"

// stateWithPeer готовит файл состояния с одним пиром и отдаёт его байты.
func stateWithPeer(t *testing.T, name string) []byte {
	t.Helper()
	ctx := context.Background()

	path := filepath.Join(t.TempDir(), "source.db")
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := st.CreatePeer(ctx, store.Peer{
		Name:         name,
		PublicKey:    "pub-" + name,
		PrivateKey:   "priv-" + name,
		PresharedKey: "psk-" + name,
		Address:      "10.8.0.2/32",
		Enabled:      true,
	}); err != nil {
		t.Fatalf("CreatePeer: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение состояния: %v", err)
	}
	return data
}

// writeFile кладёт байты во временный файл и отдаёт путь.
func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("запись %s: %v", path, err)
	}
	return path
}

// peerNames читает имена пиров из файла состояния.
func peerNames(t *testing.T, dbPath string) []string {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	peers, err := st.Peers(ctx)
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	out := make([]string, 0, len(peers))
	for _, p := range peers {
		out = append(out, p.Name)
	}
	return out
}

// Восстановление на чистой установке: пиры возвращаются с их ключами, и старые
// клиентские конфиги продолжают работать — ради этого копия и снимается целиком.
func TestRestoreOntoCleanInstall(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	file := writeFile(t, "copy.db", stateWithPeer(t, "ноутбук"))

	if err := restore(context.Background(), restoreOptions{dbPath: dbPath, file: file}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := peerNames(t, dbPath); len(got) != 1 || got[0] != "ноутбук" {
		t.Fatalf("пиры после восстановления: %v", got)
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("права состояния %v, ожидались 0600", info.Mode().Perm())
	}
}

// Поверх непустой БД восстановление не работает без явного подтверждения:
// случайный запуск не должен стоить владельцу его пиров.
func TestRestoreRefusesNonEmptyWithoutForce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(dbPath, stateWithPeer(t, "рабочий"), 0o600); err != nil {
		t.Fatalf("подготовка состояния: %v", err)
	}
	file := writeFile(t, "copy.db", stateWithPeer(t, "из копии"))

	err := restore(context.Background(), restoreOptions{dbPath: dbPath, file: file})
	if err == nil {
		t.Fatal("восстановление затёрло непустую БД без -force")
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Errorf("отказ не называет флаг подтверждения: %v", err)
	}
	if got := peerNames(t, dbPath); len(got) != 1 || got[0] != "рабочий" {
		t.Fatalf("рабочее состояние изменилось: %v", got)
	}
}

// С флагом восстановление проходит, а прежнее состояние остаётся рядом: путь
// назад обязан быть даже у подтверждённой замены.
func TestRestoreForceKeepsPreviousState(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	if err := os.WriteFile(dbPath, stateWithPeer(t, "рабочий"), 0o600); err != nil {
		t.Fatalf("подготовка состояния: %v", err)
	}
	file := writeFile(t, "copy.db", stateWithPeer(t, "из копии"))

	err := restore(context.Background(), restoreOptions{dbPath: dbPath, file: file, force: true})
	if err != nil {
		t.Fatalf("restore -force: %v", err)
	}
	if got := peerNames(t, dbPath); len(got) != 1 || got[0] != "из копии" {
		t.Fatalf("пиры после восстановления: %v", got)
	}

	saved, err := filepath.Glob(filepath.Join(dir, backupDir, "state-before-restore-*.db"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(saved) != 1 {
		t.Fatalf("копий прежнего состояния: %d, ожидалась одна", len(saved))
	}
	if got := peerNames(t, saved[0]); len(got) != 1 || got[0] != "рабочий" {
		t.Errorf("в сохранённой копии %v, ожидался прежний пир", got)
	}
}

// Копия от более новой версии отвергается тем же механизмом, что у демона при
// старте: своей проверки версий восстановление не заводит.
func TestRestoreRefusesFutureSchema(t *testing.T) {
	data := stateWithPeer(t, "из будущего")
	file := writeFile(t, "future.db", data)
	bumpSchema(t, file, 999)
	// Файл прочитан заново: версию поменяли уже на диске.
	future, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("чтение файла: %v", err)
	}
	file = writeFile(t, "future-copy.db", future)

	dbPath := filepath.Join(t.TempDir(), "state.db")
	err = restore(context.Background(), restoreOptions{dbPath: dbPath, file: file})
	if err == nil {
		t.Fatal("копия из будущего принята")
	}
	if !strings.Contains(err.Error(), "новее") {
		t.Errorf("отказ не объясняет причину: %v", err)
	}
	if _, statErr := os.Stat(dbPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("отказ оставил после себя файл состояния")
	}
}

// Копия от более старой версии прогоняет миграции: откат на предыдущий релиз
// возможен только вместе с восстановлением, и наоборот.
func TestRestoreMigratesOldSchema(t *testing.T) {
	file := writeFile(t, "old.db", oldSchemaState(t))

	dbPath := filepath.Join(t.TempDir(), "state.db")
	if err := restore(context.Background(), restoreOptions{dbPath: dbPath, file: file}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := peerNames(t, dbPath); len(got) != 1 || got[0] != "старый" {
		t.Fatalf("пиры после миграции: %v", got)
	}
	got, err := store.StateSummaryAt(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("StateSummaryAt: %v", err)
	}
	if got.SchemaVersion < 10 {
		t.Errorf("версия схемы после восстановления %d — миграции не прогнались",
			got.SchemaVersion)
	}
}

// Зашифрованная копия расшифровывается фразой из окружения: аргументом
// командной строки фраза не передаётся никогда.
func TestRestoreEncrypted(t *testing.T) {
	enc, err := store.EncryptBackup(stateWithPeer(t, "из чата"), restorePhrase)
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}
	file := writeFile(t, "copy.db.enc", enc)
	t.Setenv(passphraseEnv, restorePhrase)

	dbPath := filepath.Join(t.TempDir(), "state.db")
	if err := restore(context.Background(), restoreOptions{dbPath: dbPath, file: file}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := peerNames(t, dbPath); len(got) != 1 || got[0] != "из чата" {
		t.Fatalf("пиры после восстановления: %v", got)
	}
}

func TestRestoreWrongPassphrase(t *testing.T) {
	enc, err := store.EncryptBackup(stateWithPeer(t, "из чата"), restorePhrase)
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}
	file := writeFile(t, "copy.db.enc", enc)
	t.Setenv(passphraseEnv, "другая фраза совсем")

	dbPath := filepath.Join(t.TempDir(), "state.db")
	err = restore(context.Background(), restoreOptions{dbPath: dbPath, file: file})
	if !errors.Is(err, store.ErrBadPassphrase) {
		t.Fatalf("restore с чужой фразой = %v, ожидалась ErrBadPassphrase", err)
	}
	if strings.Contains(err.Error(), "другая фраза совсем") {
		t.Error("фраза попала в текст ошибки")
	}
}

// Чужой файл отвергается до того, как спросят фразу: спрашивать её у файла,
// который всё равно не подойдёт, незачем.
func TestRestoreRejectsForeignFile(t *testing.T) {
	file := writeFile(t, "чужое.txt", []byte("это не копия состояния"))

	dbPath := filepath.Join(t.TempDir(), "state.db")
	err := restore(context.Background(), restoreOptions{dbPath: dbPath, file: file})
	if !errors.Is(err, store.ErrNotBackup) {
		t.Fatalf("чужой файл = %v, ожидалась ErrNotBackup", err)
	}
}

// Пустая база — не состояние: восстанавливать из неё нечего, и молча ставить её
// на место рабочей нельзя.
func TestRestoreRejectsEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "пустая.db")
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	file := writeFile(t, "copy.db", data)

	dbPath := filepath.Join(t.TempDir(), "state.db")
	if err := restore(context.Background(), restoreOptions{dbPath: dbPath, file: file}); err == nil {
		t.Fatal("пустая база принята за состояние")
	}
}

// bumpSchema выставляет файлу версию схемы — так выглядит копия, снятая более
// новой версией razdacha.
func bumpSchema(t *testing.T, path string, version int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("PRAGMA user_version = " + strconv.Itoa(version)); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
}

// oldSchemaState собирает базу первой версии схемы: четыре таблицы и пир в них.
// Копия, снятая старым релизом, выглядит именно так, и восстановление обязано
// прогнать по ней миграции.
func oldSchemaState(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	const schema = `
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
PRAGMA user_version = 1;
INSERT INTO peers (id, name, public_key, private_key, preshared_key, address, enabled, created_at)
VALUES ('p1', 'старый', 'pub', 'priv', 'psk', '10.8.0.2/32', 1, 0);
`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("схема первой версии: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	return data
}
