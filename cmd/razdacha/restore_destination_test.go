package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

func TestRestoreRejectsDestinationAliasesBeforeAnySQLiteOrServiceMutation(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		for _, kind := range []string{"symlink", "hardlink", "directory", "fifo", "shared-mode"} {
			t.Run(strings.TrimPrefix(suffix, "-")+"-"+kind, func(t *testing.T) {
				dir := t.TempDir()
				db := filepath.Join(dir, "state.db")
				original := stateWithPeer(t, "Synthetic canonical destination")
				if e := os.WriteFile(db, original, 0o600); e != nil {
					t.Fatal(e)
				}
				outside := writeFile(t, "sibling.db", stateWithPeer(t, "Synthetic sibling sentinel"))
				before, e := os.ReadFile(outside)
				if e != nil {
					t.Fatal(e)
				}
				target := db + suffix
				if suffix == "" {
					if e = os.Remove(target); e != nil {
						t.Fatal(e)
					}
				}
				switch kind {
				case "symlink":
					e = os.Symlink(outside, target)
				case "hardlink":
					e = os.Link(outside, target)
				case "directory":
					e = os.Mkdir(target, 0o700)
				case "fifo":
					e = syscall.Mkfifo(target, 0o600)
				case "shared-mode":
					e = os.WriteFile(target, original, 0o640)
					if e == nil {
						e = os.Chmod(target, 0o640)
					}
				}
				if e != nil {
					t.Fatal(e)
				}
				mutations := 0
				opts := restoreOptions{dbPath: db, file: writeFile(t, "incoming.db", stateWithPeer(t, "Synthetic incoming")), force: true, systemctl: func(context.Context, ...string) error { mutations++; return nil }, closeIngress: func() error { mutations++; return nil }}
				if e = fixtureRestore(t, context.Background(), opts); e == nil {
					t.Fatal("unsupported destination DB/journal topology admitted")
				}
				if mutations != 0 {
					t.Fatal("destination rejection followed service/ingress mutation")
				}
				after, e := os.ReadFile(outside)
				if e != nil || string(after) != string(before) {
					t.Fatal("rejected destination touched sibling target")
				}
				for _, name := range []string{"restore-state.json", "restore-incomplete", "restore-runtime-inhibit"} {
					if _, e = os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(e) {
						t.Fatal("destination rejection followed initial safety mutation")
					}
				}
				if kind == "symlink" {
					if info, e := os.Lstat(target); e != nil || info.Mode()&os.ModeSymlink == 0 {
						t.Fatal("unsupported alias was replaced")
					}
				}
			})
		}
	}
}

func TestRecoveryAndReplacementRejectDestinationAliasBeforeMutation(t *testing.T) {
	opts, op, calls := recoveryFixture(t, false, false)
	outside := writeFile(t, "sibling.db", stateWithPeer(t, "Synthetic sibling destination"))
	before, e := os.ReadFile(outside)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(opts.dbPath); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, opts.dbPath); e != nil {
		t.Fatal(e)
	}
	guarded := 0
	opts.closeIngress = func() error { guarded++; return nil }
	for _, activate := range []bool{false, true} {
		if e = recoverRestore(context.Background(), opts, op, activate); e == nil {
			t.Fatal("recovery followed a canonical DB alias")
		}
	}
	if len(*calls) != 0 || guarded != 0 {
		t.Fatal("recovery alias rejection followed service/guard mutation")
	}
	staged := writeFile(t, "incoming.db", stateWithPeer(t, "Synthetic replacement"))
	if e = replaceDBUsing(staged, opts.dbPath, nil); e == nil {
		t.Fatal("replacement followed a canonical DB alias")
	}
	for _, operation := range []func() error{
		func() error { _, e := store.RestoreSummary(context.Background(), opts.dbPath); return e },
		func() error {
			return store.BackupAt(context.Background(), opts.dbPath, filepath.Join(filepath.Dir(staged), "snapshot.db"))
		},
		func() error { return store.CheckpointAt(context.Background(), opts.dbPath) },
		func() error {
			st, e := store.OpenRestoreCurrent(context.Background(), opts.dbPath)
			if st != nil {
				_ = st.Close()
			}
			return e
		},
	} {
		if e = operation(); e == nil {
			t.Fatal("shared SQLite entry point followed alias")
		}
	}
	after, e := os.ReadFile(outside)
	if e != nil || string(after) != string(before) {
		t.Fatal("recovery/replacement/SQLite entry touched sibling")
	}
	if info, e := os.Lstat(opts.dbPath); e != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("recovery/replacement changed the alias")
	}
}
