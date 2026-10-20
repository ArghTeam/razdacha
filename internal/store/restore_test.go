package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func sqlOpenFixture(path string) (*sql.DB, error) { return sql.Open("sqlite", dsn(path)) }

func restoreFixture(t *testing.T) *Store {
	t.Helper()
	st, e := Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestRestoreSchemaRejectsExecutableAndUnknownObjects(t *testing.T) {
	for _, ddl := range []string{
		`CREATE TRIGGER enable_after_settings AFTER INSERT ON settings BEGIN UPDATE tunnels SET enabled=1; END`,
		`CREATE VIEW extra AS SELECT * FROM tunnels`,
		`CREATE TABLE extra(value TEXT)`,
		`CREATE INDEX extra ON tunnels(enabled)`,
		`PRAGMA application_id=123`,
		`ALTER TABLE tunnels ADD COLUMN extra TEXT`,
		`CREATE VIRTUAL TABLE extra USING fts5(value)`,
	} {
		t.Run(ddl, func(t *testing.T) {
			st := restoreFixture(t)
			if _, e := st.DB().Exec(ddl); e != nil {
				if ddl == `CREATE VIRTUAL TABLE extra USING fts5(value)` {
					t.Skip("SQLite build has no FTS5")
				}
				t.Fatal(e)
			}
			path := dbPathForTest(t, st)
			if e := ValidateRestoreSchema(context.Background(), path); e == nil {
				t.Fatal("unsupported schema admitted")
			}
			var count int
			if e := st.DB().QueryRow("SELECT count(*) FROM settings").Scan(&count); e != nil || count != 0 {
				t.Fatal("schema inspection wrote settings")
			}
		})
	}
}

func dbPathForTest(t *testing.T, st *Store) string {
	t.Helper()
	var seq int
	var name, path string
	if e := st.DB().QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); e != nil {
		t.Fatal(e)
	}
	return path
}

func TestRestoreSchemaSupportsEveryReleasedVersion(t *testing.T) {
	ctx := context.Background()
	for version := 1; version <= schemaVersion(); version++ {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "old.db")
			if e := prepareFile(path); e != nil {
				t.Fatal(e)
			}
			db, e := Open(ctx, path)
			if e != nil {
				t.Fatal(e)
			}
			_ = db.Close()
			// Build historical schema directly from released statements, not a handwritten approximation.
			if e = os.Remove(path); e != nil {
				t.Fatal(e)
			}
			raw, e := sqlOpenFixture(path)
			if e != nil {
				t.Fatal(e)
			}
			for _, m := range migrations {
				if m.version > version {
					break
				}
				if _, e = raw.Exec(m.stmts); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = raw.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); e != nil {
				t.Fatal(e)
			}
			_ = raw.Close()
			if e = ValidateRestoreSchema(ctx, path); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestPrepareRestoreChangesOnlySelectedEnabledFlags(t *testing.T) {
	ctx := context.Background()
	st := restoreFixture(t)
	path := dbPathForTest(t, st)
	for _, id := range []string{"wg", "other", "disabled"} {
		enabled := 1
		if id == "disabled" {
			enabled = 0
		}
		if _, e := st.DB().Exec(`INSERT INTO tunnels(id,name,type,source,raw,parsed,enabled,created_at) VALUES(?,?,'wireguard','json','fixture','{}',?,1)`, id, id, enabled); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := st.DB().Exec(`INSERT INTO settings VALUES('private_fixture','exact-secret'); INSERT INTO warp_registrations VALUES('wg','fixture-device','fixture-token',1)`); e != nil {
		t.Fatal(e)
	}
	if e := st.Close(); e != nil {
		t.Fatal(e)
	}
	ids, e := PrepareRestore(ctx, path, func([]Tunnel) ([]string, error) { return []string{"wg", "disabled"}, nil })
	if e != nil {
		t.Fatal(e)
	}
	if len(ids) != 2 {
		t.Fatal(ids)
	}
	reopened, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = reopened.Close() }()
	var enabled int
	if e = reopened.DB().QueryRow(`SELECT enabled FROM tunnels WHERE id='wg'`).Scan(&enabled); e != nil || enabled != 0 {
		t.Fatal("WG not paused")
	}
	if e = reopened.DB().QueryRow(`SELECT enabled FROM tunnels WHERE id='other'`).Scan(&enabled); e != nil || enabled != 1 {
		t.Fatal("nonselected intent changed")
	}
	var token string
	if e = reopened.DB().QueryRow(`SELECT access_token FROM warp_registrations`).Scan(&token); e != nil || token != "fixture-token" {
		t.Fatal("registration changed")
	}
}

func TestRestoreCurrentDestinationSnapshotIncludesCommittedWAL(t *testing.T) {
	ctx := context.Background()
	st := restoreFixture(t)
	if _, e := st.DB().ExecContext(ctx, "PRAGMA wal_autocheckpoint=0"); e != nil {
		t.Fatal(e)
	}
	peer, e := st.CreatePeer(ctx, Peer{Name: "Synthetic committed WAL peer", Address: "10.8.0.2/32", PublicKey: "synthetic-public", PrivateKey: "synthetic-private", PresharedKey: "synthetic-psk", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	path := dbPathForTest(t, st)
	info, e := os.Stat(path + "-wal")
	if e != nil || info.Size() <= 32 {
		t.Fatal("positive committed WAL fixture missing")
	}
	snapshot := filepath.Join(t.TempDir(), "current-destination.db")
	if e = BackupAt(ctx, path, snapshot); e != nil {
		t.Fatal(e)
	}
	recovered, e := OpenRestoreCurrent(ctx, snapshot)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = recovered.Close() }()
	peers, e := recovered.Peers(ctx)
	if e != nil || len(peers) != 1 || peers[0].ID != peer.ID || peers[0].PrivateKey != "synthetic-private" || peers[0].PresharedKey != "synthetic-psk" {
		t.Fatalf("snapshot lost committed WAL identity: %+v %v", peers, e)
	}
}
