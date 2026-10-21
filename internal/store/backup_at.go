package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// openReadOnly opens an existing database without changing its journal mode or
// applying migrations. WAL remains visible; immutable=1 would silently ignore it.
func openReadOnly(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	query := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)"}}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open read-only database: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// BackupAt saves a consistent, standalone snapshot of an existing database before
// migrations. It includes committed WAL transactions and preserves the original
// schema and installation metadata without opening a migrating Store.
// The destination must not exist; a failed or cancelled snapshot is removed.
func BackupAt(ctx context.Context, source, destination string) (err error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("backup database: %w", err)
	}
	db, err := openReadOnly(source)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("read backup source: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), dbDirMode); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	// SQLite accepts an empty existing destination. Reserve it before SQLite opens
	// it so private keys are never written with permissions inherited from umask.
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_RDWR, dbFileMode)
	if err != nil {
		return fmt.Errorf("reserve backup destination: %w", err)
	}
	defer func() {
		closeErr := file.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("close database backup: %w", closeErr)
		}
		if err != nil {
			if removeErr := os.Remove(destination); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove incomplete backup %s: %w", destination, removeErr))
			}
		}
	}()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	checked, err := openReadOnly(destination)
	if err != nil {
		return err
	}
	var integrity string
	checkErr := checked.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity)
	closeErr := checked.Close()
	if checkErr != nil {
		return fmt.Errorf("verify database backup: %w", checkErr)
	}
	if integrity != "ok" {
		return fmt.Errorf("database backup integrity check failed: %s", integrity)
	}
	if closeErr != nil {
		return fmt.Errorf("close backup verification: %w", closeErr)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("backup database: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync database backup: %w", err)
	}
	return nil
}
