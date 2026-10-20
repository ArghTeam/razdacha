package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
)

// AdmitRestoreDestination validates the private canonical DB and every SQLite
// sidecar without opening SQLite or following leaf aliases. Lifecycle callers hold
// their installation lease; every later SQLite entry point repeats this admission.
func AdmitRestoreDestination(path string) (bool, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false, errors.New("restore database path must be absolute and clean")
	}
	parent, e := os.Lstat(filepath.Dir(path))
	if e != nil {
		return false, fmt.Errorf("inspect restore database parent: %w", e)
	}
	stat, ok := parent.Sys().(*syscall.Stat_t)
	if !parent.IsDir() || parent.Mode().Perm() != 0o700 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return false, errors.New("restore database parent must be a private owner-controlled nonsymlink directory")
	}
	present := false
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		info, e := os.Lstat(path + suffix)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return false, fmt.Errorf("inspect restore database file: %w", e)
		}
		if e = restoreOwnedRegular(info); e != nil {
			return false, e
		}
		if suffix == "" {
			present = true
		} else if !present {
			return false, errors.New("orphan SQLite sidecar requires explicit local repair")
		}
	}
	return present, nil
}

func restoreOwnedRegular(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return errors.New("restore database and journals must be private owner-controlled regular files without aliases")
	}
	return nil
}

type schemaObject struct{ Type, Name, Table, SQL string }

func restoreDB(ctx context.Context, path string) (*sql.DB, error) {
	if present, e := AdmitRestoreDestination(path); e != nil {
		return nil, e
	} else if !present {
		return nil, errors.New("restore database is missing")
	}
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = "mode=ro&_pragma=trusted_schema(OFF)&_pragma=foreign_keys(1)"
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	if e = db.PingContext(ctx); e != nil {
		_ = db.Close()
		return nil, e
	}
	return db, nil
}

func schemaObjects(ctx context.Context, db *sql.DB) ([]schemaObject, error) {
	rows, e := db.QueryContext(ctx, `SELECT type,name,tbl_name,coalesce(sql,'') FROM sqlite_schema ORDER BY type,name`)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	var out []schemaObject
	for rows.Next() {
		var o schemaObject
		if e = rows.Scan(&o.Type, &o.Name, &o.Table, &o.SQL); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ValidateRestoreSchema admits only schema objects produced by this release's migrations.
// It runs read-only BEFORE Open: user triggers must not execute during migration/pause.
func ValidateRestoreSchema(ctx context.Context, path string) error {
	db, e := restoreDB(ctx, path)
	if e != nil {
		return fmt.Errorf("open restore schema: %w", e)
	}
	defer func() { _ = db.Close() }()
	var applicationID int
	if e = db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); e != nil {
		return e
	}
	if applicationID != 0 {
		return errors.New("unsupported restore application_id")
	}
	var version int
	if e = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil {
		return e
	}
	if version < 1 || version > schemaVersion() {
		return fmt.Errorf("unsupported restore schema %d (supported 1..%d)", version, schemaVersion())
	}
	actual, e := schemaObjects(ctx, db)
	if e != nil {
		return e
	}
	reference, e := sql.Open("sqlite", ":memory:")
	if e != nil {
		return e
	}
	defer func() { _ = reference.Close() }()
	reference.SetMaxOpenConns(1)
	for _, m := range migrations {
		if m.version > version {
			break
		}
		if _, e = reference.ExecContext(ctx, m.stmts); e != nil {
			return fmt.Errorf("construct supported schema: %w", e)
		}
	}
	expected, e := schemaObjects(ctx, reference)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("restore schema contains unsupported or modified objects")
	}
	var integrity string
	if e = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); e != nil {
		return e
	}
	if integrity != "ok" {
		return errors.New("restore database integrity check failed")
	}
	rows, e := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		return errors.New("restore database has broken references")
	}
	return rows.Err()
}

type restoreRows map[string][][]any

func allRestoreRows(ctx context.Context, db *sql.DB) (restoreRows, error) {
	objects, e := schemaObjects(ctx, db)
	if e != nil {
		return nil, e
	}
	out := make(restoreRows)
	for _, o := range objects {
		if o.Type != "table" {
			continue
		}
		name := `"` + strings.ReplaceAll(o.Name, `"`, `""`) + `"`
		data, e := restoreTableRows(ctx, db, name)
		if e != nil {
			return nil, e
		}
		out[o.Name] = data
	}
	return out, nil
}

func restoreTableRows(ctx context.Context, db *sql.DB, quotedTable string) ([][]any, error) {
	rows, e := db.QueryContext(ctx, "SELECT * FROM "+quotedTable+" ORDER BY rowid")
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	cols, e := rows.Columns()
	if e != nil {
		return nil, e
	}
	var data [][]any
	for rows.Next() {
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}
		if e = rows.Scan(pointers...); e != nil {
			return nil, e
		}
		for i, value := range values {
			if blob, ok := value.([]byte); ok {
				values[i] = append([]byte(nil), blob...)
			}
		}
		data = append(data, values)
	}
	return data, rows.Err()
}

// PrepareRestore migrates an admitted private copy and changes only selected WG flags.
// classify must be pure/offline. Exact row comparison survives close/reopen and covers
// tables not exposed by Snapshot, including authentication and WARP registrations.
func PrepareRestore(ctx context.Context, path string, classify func([]Tunnel) ([]string, error)) ([]string, error) {
	if e := ValidateRestoreSchema(ctx, path); e != nil {
		return nil, e
	}
	st, e := Open(ctx, path)
	if e != nil {
		return nil, e
	}
	closed := false
	defer func() {
		if !closed {
			_ = st.Close()
		}
	}()
	snap, e := st.Snapshot(ctx)
	if e != nil {
		return nil, e
	}
	for _, t := range snap.Tunnels {
		if e := t.validate(); e != nil {
			return nil, fmt.Errorf("restore tunnel %s: %w", t.ID, e)
		}
	}
	for _, p := range snap.Peers {
		if e := p.validate(); e != nil {
			return nil, e
		}
	}
	if e := snap.Settings.validate(); e != nil {
		return nil, e
	}
	ids, e := classify(snap.Tunnels)
	if e != nil {
		return nil, e
	}
	sort.Strings(ids)
	for _, r := range snap.Rules {
		if e := r.validate(); e != nil {
			return nil, fmt.Errorf("restore rule %s: %w", r.ID, e)
		}
	}
	before, e := allRestoreRows(ctx, st.db)
	if e != nil {
		return nil, e
	}
	pause := make(map[string]bool)
	for _, id := range ids {
		pause[id] = true
	}
	cols, e := func() ([]string, error) {
		rows, e := st.db.QueryContext(ctx, "SELECT * FROM tunnels LIMIT 0")
		if e != nil {
			return nil, e
		}
		defer func() { _ = rows.Close() }()
		return rows.Columns()
	}()
	if e != nil {
		return nil, e
	}
	idCol, enabledCol := -1, -1
	for i, c := range cols {
		if c == "id" {
			idCol = i
		}
		if c == "enabled" {
			enabledCol = i
		}
	}
	if idCol < 0 || enabledCol < 0 {
		return nil, errors.New("missing tunnel identity columns")
	}
	for _, row := range before["tunnels"] {
		if pause[fmt.Sprint(row[idCol])] {
			row[enabledCol] = int64(0)
		}
	}
	e = st.tx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, "UPDATE tunnels SET enabled = 0 WHERE id = ?", id); err != nil {
				return err
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if _, e = st.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); e != nil {
		return nil, e
	}
	e = st.Close()
	closed = true
	if e != nil {
		return nil, e
	}
	if e = ValidateRestoreSchema(ctx, path); e != nil {
		return nil, e
	}
	db, e := restoreDB(ctx, path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = db.Close() }()
	after, e := allRestoreRows(ctx, db)
	if e != nil {
		return nil, e
	}
	if !reflect.DeepEqual(before, after) {
		return nil, errors.New("restore changed rows beyond permitted WireGuard pause")
	}
	return ids, nil
}

// BackupAt preserves committed WAL contents without migrating the destination.
func BackupAt(ctx context.Context, path, dst string) error {
	if present, e := AdmitRestoreDestination(path); e != nil {
		return e
	} else if !present {
		return errors.New("current destination database is missing")
	}
	if present, e := AdmitRestoreDestination(dst); e != nil {
		return e
	} else if present {
		return errors.New("restore snapshot target already exists")
	}
	db, e := restoreDB(ctx, path)
	if e != nil {
		return e
	}
	defer func() { _ = db.Close() }()
	f, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if _, e = db.ExecContext(ctx, "VACUUM INTO ?", dst); e != nil {
		return fmt.Errorf("snapshot current destination: %w", e)
	}
	return nil
}

// CheckpointAt drains journals without migrating or changing logical destination rows.
func CheckpointAt(ctx context.Context, path string) error {
	if present, e := AdmitRestoreDestination(path); e != nil {
		return e
	} else if !present {
		return errors.New("cannot checkpoint missing destination database")
	}
	db, e := sql.Open("sqlite", dsn(path))
	if e != nil {
		return e
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	var busy, log, checkpointed int
	if e = db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); e != nil {
		return e
	}
	if busy != 0 {
		return errors.New("destination database still has active writers")
	}
	return nil
}

// RestoreSummary reads a destination without journal-mode changes or creation.
func RestoreSummary(ctx context.Context, path string) (StateSummary, error) {
	present, e := AdmitRestoreDestination(path)
	if e != nil {
		return StateSummary{}, e
	}
	if !present {
		return StateSummary{}, nil
	}

	db, e := restoreDB(ctx, path)
	if e != nil {
		return StateSummary{}, e
	}
	defer func() { _ = db.Close() }()
	var out StateSummary
	if e = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&out.SchemaVersion); e != nil {
		return out, e
	}
	for _, v := range []struct {
		name  string
		count *int
	}{{"peers", &out.Peers}, {"tunnels", &out.Tunnels}, {"rules", &out.Rules}} {
		if e = db.QueryRowContext(ctx, "SELECT count(*) FROM "+v.name).Scan(v.count); e != nil {
			return out, e
		}
	}
	return out, nil
}

// OpenRestoreCurrent refuses to create or migrate live DBs owned by trusted restore.
func OpenRestoreCurrent(ctx context.Context, path string) (*Store, error) {
	if present, e := AdmitRestoreDestination(path); e != nil {
		return nil, e
	} else if !present {
		return nil, errors.New("trusted restore database is missing")
	}
	if e := ValidateRestoreSchema(ctx, path); e != nil {
		return nil, e
	}
	summary, e := RestoreSummary(ctx, path)
	if e != nil {
		return nil, e
	}
	if summary.SchemaVersion != schemaVersion() {
		return nil, errors.New("trusted restored database needs explicit recovery, not startup migration")
	}
	return Open(ctx, path)
}
