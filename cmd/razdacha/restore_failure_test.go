package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/restorestate"
)

func TestRestoreServiceGuardSnapshotAndStartFailuresRetainProtection(t *testing.T) {
	for _, failure := range []string{"inhibitor-reload", "daemon-stop", "runtime-stop", "guard", "snapshot", "daemon-start"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "state.db")
			if e := os.WriteFile(db, stateWithPeer(t, "Synthetic original"), 0o600); e != nil {
				t.Fatal(e)
			}
			for _, name := range []string{networkstate.CommittedFile, networkstate.BootstrapFile} {
				if e := os.WriteFile(filepath.Join(dir, name), []byte("synthetic original authority"), 0o600); e != nil {
					t.Fatal(e)
				}
			}
			var captured restoreOptions
			starts := 0
			opts := restoreOptions{dbPath: db, file: writeFile(t, "incoming.db", stateWithPeer(t, "Synthetic imported")), force: true, unitActive: func(context.Context) bool { return true }}
			opts.systemctl = func(_ context.Context, args ...string) error {
				action := strings.Join(args, " ")
				if strings.HasPrefix(action, "start ") {
					starts++
				}
				if failure == "inhibitor-reload" && action == "daemon-reload" || failure == "daemon-stop" && action == "stop "+packaging.DaemonUnit || failure == "runtime-stop" && action == "stop "+packaging.SingboxUnit || failure == "daemon-start" && action == "start "+packaging.DaemonUnit {
					return errors.New("synthetic service failure")
				}
				return nil
			}
			opts.closeIngress = func() error {
				if failure == "guard" {
					return errors.New("synthetic guard failure")
				}
				if failure == "snapshot" {
					return os.WriteFile(db, []byte("synthetic unreadable SQLite snapshot"), 0o600)
				}
				return nil
			}
			if e := fixtureRestore(t, context.Background(), opts, func(o restoreOptions) { captured = o }); e == nil {
				t.Fatal("injected failure hidden")
			}
			s, e := restorestate.RecoveryState(dir)
			if e != nil || s == nil || !s.Held || !s.RuntimeInhibited {
				t.Fatalf("failure lost restrictive trusted record: %+v %v", s, e)
			}
			if _, e = os.Stat(filepath.Join(dir, restorestate.Runtime)); e != nil {
				t.Fatal("failure removed runtime start inhibition")
			}
			if failure != "daemon-start" {
				if starts != 0 {
					t.Fatal("failure started a service")
				}
				if _, e = os.Stat(filepath.Join(dir, networkstate.CommittedFile)); e != nil {
					t.Fatal("pre-snapshot failure invalidated destination authority")
				}
			}
			if failure != "snapshot" && failure != "daemon-start" {
				if got := peerNames(t, db); got[0] != "Synthetic original" {
					t.Fatal("early failure replaced destination DB")
				}
			}
			if captured.services == nil {
				t.Fatal("contained service adapter missing")
			}
		})
	}
}

func TestRestoreDBSyncAndRenameFailuresRemainRecoverable(t *testing.T) {
	for _, failure := range []string{"file-sync", "rename", "operation-dir-sync", "destination-dir-sync"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "state.db")
			if e := os.WriteFile(db, stateWithPeer(t, "Synthetic original"), 0o600); e != nil {
				t.Fatal(e)
			}
			real := defaultRestoreDBFiles()
			ops := real
			failed := false
			injected := func() error { failed = true; return errors.New("synthetic exact replacement syscall failure") }
			if failure == "file-sync" {
				ops.syncFile = func(string) error { return injected() }
			}
			if failure == "rename" {
				ops.rename = func(string, string) error { return injected() }
			}
			if strings.HasSuffix(failure, "dir-sync") {
				ops.syncDir = func(path string) error {
					if failure == "operation-dir-sync" && path != dir || failure == "destination-dir-sync" && path == dir {
						return injected()
					}
					return real.syncDir(path)
				}
			}
			var captured restoreOptions
			opts := restoreOptions{dbPath: db, file: writeFile(t, "incoming.db", stateWithPeer(t, "Synthetic imported")), force: true, dbFiles: &ops}
			if e := fixtureRestore(t, context.Background(), opts, func(o restoreOptions) { captured = o }); e == nil || !failed {
				t.Fatal("exact syscall failure not exercised")
			}
			s, e := restorestate.Read(dir)
			if e != nil || !s.Held || !s.RuntimeInhibited || s.Phase != "preparing" {
				t.Fatalf("replacement failure lost inhibition: %+v %v", s, e)
			}
			recovery, e := restorestate.ReadRecovery(s.Recovery)
			if e != nil || !recovery.Complete {
				t.Fatal("replacement happened before coherent current-destination recovery")
			}
			expected := "Synthetic original"
			if strings.HasSuffix(failure, "dir-sync") {
				expected = "Synthetic imported"
			}
			if got := peerNames(t, db); len(got) != 1 || got[0] != expected {
				t.Fatalf("unexpected atomic DB side: %v", got)
			}
			captured.dbFiles = nil
			if e = recoverRestore(context.Background(), captured, s.Recovery, false); e != nil {
				t.Fatalf("explicit rollback after I/O error: %v", e)
			}
			if got := peerNames(t, db); got[0] != "Synthetic original" {
				t.Fatal("I/O failure rollback used imported state")
			}
		})
	}
}

func TestConcurrentActualRestoresUseOneOperationDirectory(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "state.db")
	incoming := writeFile(t, "incoming.db", stateWithPeer(t, "Synthetic serialized incoming"))
	reached, release := make(chan struct{}), make(chan struct{})
	opts := restoreOptions{dbPath: db, file: incoming, afterDurable: func(cut string) error {
		if cut == "inhibitors-installed" {
			close(reached)
			<-release
		}
		return nil
	}}
	result := make(chan error, 1)
	go func() { result <- fixtureRestore(t, context.Background(), opts) }()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("first restore did not acquire lifecycle")
	}
	secondCalls := 0
	second := restoreOptions{dbPath: db, file: incoming, systemctl: func(context.Context, ...string) error { secondCalls++; return nil }}
	e := fixtureRestore(t, context.Background(), second)
	close(release)
	if e == nil || secondCalls != 0 {
		t.Fatal("concurrent restore reached managed services")
	}
	select {
	case e = <-result:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first restore did not complete after release")
	}
	operations, e := filepath.Glob(filepath.Join(dir, ".restore-*"))
	if e != nil || len(operations) != 1 {
		t.Fatalf("concurrent restore created racing operation storage: %v %v", operations, e)
	}
	if got := peerNames(t, db); len(got) != 1 || got[0] != "Synthetic serialized incoming" {
		t.Fatal("positive serialized restore missing")
	}
}
