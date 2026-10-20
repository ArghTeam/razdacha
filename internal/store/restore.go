package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
)

type schemaObject struct{ Type, Name, Table, SQL string }

func restoreDB(ctx context.Context, path string) (*sql.DB, error) {
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
		rows, e := db.QueryContext(ctx, "SELECT * FROM "+name+" ORDER BY rowid")
		if e != nil {
			return nil, e
		}
		cols, e := rows.Columns()
		if e != nil {
			_ = rows.Close()
			return nil, e
		}
		var data [][]any
		for rows.Next() {
			v := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range v {
				ptr[i] = &v[i]
			}
			if e = rows.Scan(ptr...); e != nil {
				_ = rows.Close()
				return nil, e
			}
			for i, x := range v {
				if b, ok := x.([]byte); ok {
					v[i] = append([]byte(nil), b...)
				}
			}
			data = append(data, v)
		}
		e = rows.Err()
		_ = rows.Close()
		if e != nil {
			return nil, e
		}
		out[o.Name] = data
	}
	return out, nil
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
	rows, e := st.db.QueryContext(ctx, "SELECT * FROM tunnels LIMIT 0")
	if e != nil {
		return nil, e
	}
	cols, e := rows.Columns()
	_ = rows.Close()
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
