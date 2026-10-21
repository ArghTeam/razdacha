package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func legacyBackupSource(t *testing.T, wal bool) (string, *sql.DB) {
	t.Helper()
	// URI metacharacters must remain part of the filename, not connection options.
	path := filepath.Join(t.TempDir(), "legacy #? database.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	statements := []string{
		"PRAGMA user_version = 1",
		"PRAGMA application_id = 12345",
		"CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)",
		"CREATE TABLE legacy_peers (private_key TEXT NOT NULL)",
	}
	if wal {
		statements = append(statements, "PRAGMA journal_mode = WAL", "PRAGMA wal_autocheckpoint = 0", "PRAGMA wal_checkpoint(TRUNCATE)")
	}
	statements = append(statements,
		"INSERT INTO settings VALUES ('installed_version', 'old-release')",
		"INSERT INTO legacy_peers VALUES ('synthetic-private-key')")
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return path, db
}

func readBackupFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBackupAtIncludesCommittedWALWithoutMigratingSource(t *testing.T) {
	source, writer := legacyBackupSource(t, true)
	beforeMain := readBackupFile(t, source)
	beforeWAL := readBackupFile(t, source+"-wal")
	if len(beforeWAL) <= 32 {
		t.Fatal("fixture must have uncheckpointed transactions")
	}
	destination := filepath.Join(t.TempDir(), "private", "backup.db")
	ctx := context.Background()
	version, err := InstalledVersionAt(ctx, source)
	if err != nil || version != "old-release" {
		t.Fatalf("installed version = %q, error = %v", version, err)
	}
	if err := BackupAt(ctx, source, destination); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeMain, readBackupFile(t, source)) || !bytes.Equal(beforeWAL, readBackupFile(t, source+"-wal")) {
		t.Fatal("backup or version inspection modified source main/WAL bytes")
	}
	backup, err := openReadOnly(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backup.Close() }()
	for query, want := range map[string]string{
		"PRAGMA user_version":                                        "1",
		"PRAGMA application_id":                                      "12345",
		"PRAGMA integrity_check":                                     "ok",
		"PRAGMA journal_mode":                                        "delete",
		"SELECT private_key FROM legacy_peers":                       "synthetic-private-key",
		"SELECT value FROM settings WHERE key = 'installed_version'": "old-release",
	} {
		var got string
		if err := backup.QueryRowContext(ctx, query).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: got %q, want %q, error = %v", query, got, want, err)
		}
	}
	for path, want := range map[string]os.FileMode{destination: 0o600, filepath.Dir(destination): 0o700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("permissions for %s: %v, error = %v", path, info, err)
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(destination + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("snapshot has sidecar %s: %v", suffix, err)
		}
	}
	// It is an independent snapshot, not a link to changing source data.
	if _, err := writer.Exec("UPDATE legacy_peers SET private_key = 'later-key'"); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := backup.QueryRowContext(ctx, "SELECT private_key FROM legacy_peers").Scan(&key); err != nil || key != "synthetic-private-key" {
		t.Fatalf("backup changed with source: %q, %v", key, err)
	}
}

func TestInstalledVersionAtDoesNotChangeDeleteJournal(t *testing.T) {
	source, db := legacyBackupSource(t, false)
	before := readBackupFile(t, source)
	if _, err := InstalledVersionAt(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("journal mode = %q, error = %v", mode, err)
	}
	if !bytes.Equal(before, readBackupFile(t, source)) {
		t.Fatal("version inspection changed database bytes")
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, err := InstalledVersionAt(context.Background(), missing); err == nil {
		t.Fatal("missing source accepted")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source was created: %v", err)
	}
}

func TestBackupAtRejectsExistingFileAndSymlink(t *testing.T) {
	source, _ := legacyBackupSource(t, false)
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "symlink"}[symlink], func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			destination := target
			if symlink {
				destination = target + "-link"
				if err := os.Symlink(target, destination); err != nil {
					t.Fatal(err)
				}
			}
			if err := BackupAt(context.Background(), source, destination); err == nil {
				t.Fatal("existing destination accepted")
			}
			if got := string(readBackupFile(t, target)); got != "preserve" {
				t.Fatalf("existing destination overwritten: %q", got)
			}
		})
	}
}

func TestBackupAtFailureAndCancellationLeaveNoSnapshot(t *testing.T) {
	source, _ := legacyBackupSource(t, false)
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		destination := filepath.Join(t.TempDir(), "backup.db")
		if err := BackupAt(ctx, source, destination); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation: %v", err)
		}
		if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancelled backup exists: %v", err)
		}
	})
	t.Run("invalid database", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "invalid.db")
		if err := os.WriteFile(source, []byte("not SQLite"), 0o600); err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(t.TempDir(), "backup.db")
		if err := BackupAt(context.Background(), source, destination); err == nil {
			t.Fatal("invalid source accepted")
		}
		if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed backup exists: %v", err)
		}
	})
}
