package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/restorestate"
)

func recoveryFixture(t *testing.T, priorHold, active bool) (restoreOptions, string, *[]string) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	db := filepath.Join(dir, "state.db")
	if e := os.WriteFile(db, stateWithPeer(t, "Synthetic original destination"), 0o600); e != nil {
		t.Fatal(e)
	}
	if priorHold {
		old := restorestate.State{Version: restorestate.Protocol, Operation: "previous", Recovery: filepath.Join(dir, "previous"), Phase: "complete", Held: true, RuntimeInhibited: true}
		if e := restorestate.WritePrivate(filepath.Join(dir, restorestate.Runtime), []byte("1\n")); e != nil {
			t.Fatal(e)
		}
		if e := restorestate.Save(dir, old); e != nil {
			t.Fatal(e)
		}
	}
	calls := []string{}
	var captured restoreOptions
	opts := restoreOptions{dbPath: db, file: writeFile(t, "incoming.db", stateWithPeer(t, "Synthetic imported destination")), force: true, unitActive: func(context.Context) bool { return active }, systemctl: func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}}
	if e := fixtureRestore(t, context.Background(), opts, func(opts restoreOptions) { captured = opts }); e != nil {
		t.Fatal(e)
	}
	s, e := restorestate.Read(dir)
	if e != nil {
		t.Fatal(e)
	}
	calls = nil
	return captured, s.Recovery, &calls
}

func TestRestoreRecoveryIsPassiveAndRequiresSeparateActivation(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, active := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{true: "prior-held", false: "prior-resumed"}[held], map[bool]string{true: "active", false: "inactive"}[active]}, "-"), func(t *testing.T) {
				opts, op, calls := recoveryFixture(t, held, active)
				ctx := context.Background()
				dir := filepath.Dir(opts.dbPath)
				if e := recoverRestore(ctx, opts, op, true); e == nil {
					t.Fatal("imported state activated without passive rollback")
				}
				if e := recoverRestore(ctx, opts, op, false); e != nil {
					t.Fatal(e)
				}
				s, e := restorestate.Read(dir)
				if e != nil {
					t.Fatal(e)
				}
				if s.Phase != "recovered" || !s.Held || !s.RuntimeInhibited || s.RecoveryActivated {
					t.Fatalf("passive recovery granted authority: %+v", s)
				}
				if got := peerNames(t, opts.dbPath); len(got) != 1 || got[0] != "Synthetic original destination" {
					t.Fatalf("recovered imported/original migration archive: %v", got)
				}
				for _, call := range *calls {
					if strings.HasPrefix(call, "start ") {
						t.Fatal("passive recovery started a service")
					}
				}
				// Byte equality is a consistency check, never sufficient activation authority.
				if e := recoverRestore(ctx, opts, op, false); e != nil {
					t.Fatalf("passive retry: %v", e)
				}
				*calls = nil
				if e := recoverRestore(ctx, opts, op, true); e != nil {
					t.Fatal(e)
				}
				s, e = restorestate.Read(dir)
				if e != nil {
					t.Fatal(e)
				}
				if s.Held != held || !s.RuntimeInhibited || !s.RecoveryActivated || s.Phase != "complete" {
					t.Fatalf("activation lost original intent: %+v", s)
				}
				daemonStarted := false
				for _, call := range *calls {
					if call == "start "+packaging.SingboxUnit {
						t.Fatal("recovery activated runtime automatically")
					}
					if call == "start "+packaging.DaemonUnit {
						daemonStarted = true
					}
				}
				if daemonStarted != active {
					t.Fatalf("original active intent %v, started %v", active, daemonStarted)
				}
			})
		}
	}
}

func TestRestoreRecoveryFailureCannotAuthorizeActivation(t *testing.T) {
	opts, op, _ := recoveryFixture(t, false, true)
	run := opts.services.Run
	opts.services.Run = func(ctx context.Context, args ...string) error {
		if len(args) > 0 && args[0] == "stop" {
			return errors.New("synthetic stop failure")
		}
		return run(ctx, args...)
	}
	if e := recoverRestore(context.Background(), opts, op, false); e == nil {
		t.Fatal("stop failure hidden")
	}
	s, e := restorestate.RecoveryState(filepath.Dir(opts.dbPath))
	if e != nil {
		t.Fatal(e)
	}
	if s.Phase != "recovering" || !s.Held || !s.RuntimeInhibited {
		t.Fatalf("failure granted startup: %+v", s)
	}
	opts.services.Run = run
	if e := recoverRestore(context.Background(), opts, op, true); e == nil {
		t.Fatal("partial recovering phase admitted activation")
	}
	if e := recoverRestore(context.Background(), opts, op, false); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(opts.dbPath, stateWithPeer(t, "Synthetic changed after rollback"), 0o600); e != nil {
		t.Fatal(e)
	}
	if e := recoverRestore(context.Background(), opts, op, true); e == nil {
		t.Fatal("changed recovered database admitted")
	}
}

func TestRecoveryParentRejectsSymlinkBeforeWriting(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(root, "lists")); e != nil {
		t.Fatal(e)
	}
	if e := ensureRecoveryParent(filepath.Join(root, "lists", "native", "artifact"), root); e == nil {
		t.Fatal("symlink parent admitted")
	}
	if _, e := os.Stat(filepath.Join(outside, "native")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("created directory through symlink before validation")
	}
	if e := ensureRecoveryParent(filepath.Join(root, "safe", "nested", "artifact"), root); e != nil {
		t.Fatal(e)
	}
}

func TestRecoveryActivationFailureRetryPreservesDisabledRuntime(t *testing.T) {
	opts, op, calls := recoveryFixture(t, false, false) // independently active runtime, inactive daemon
	r, e := restorestate.ReadRecovery(op)
	if e != nil {
		t.Fatal(e)
	}
	// The runtime was active but disabled before restore. Neither fact grants an
	// automatic runtime start during acknowledged destination recovery.
	r.Services = []byte(`{"razdachad.service":{"Active":"inactive","Enabled":"disabled"},"sing-box.service":{"Active":"active","Enabled":"disabled"}}`)
	if e = restorestate.SaveRecovery(op, r); e != nil {
		t.Fatal(e)
	}
	if e = recoverRestore(context.Background(), opts, op, false); e != nil {
		t.Fatal(e)
	}
	run := opts.services.Run
	failed := false
	opts.services.Run = func(ctx context.Context, args ...string) error {
		if len(args) > 0 && args[0] == "disable" && !failed {
			failed = true
			return errors.New("synthetic enable-intent failure")
		}
		return run(ctx, args...)
	}
	if e = recoverRestore(context.Background(), opts, op, true); e == nil {
		t.Fatal("activation failure hidden")
	}
	s, e := restorestate.Read(filepath.Dir(opts.dbPath))
	if e != nil {
		t.Fatal(e)
	}
	if !s.RuntimeInhibited {
		t.Fatal("activation failure removed runtime inhibition")
	}
	*calls = nil
	if e = recoverRestore(context.Background(), opts, op, true); e != nil {
		t.Fatalf("explicit activation retry: %v", e)
	}
	for _, unit := range []string{packaging.DaemonUnit, packaging.SingboxUnit} {
		found := false
		for _, call := range *calls {
			if strings.HasPrefix(call, "start ") {
				t.Fatal("previously inactive daemon or runtime started")
			}
			if call == "disable "+unit {
				found = true
			}
		}
		if !found {
			t.Fatalf("lost disabled intent for %s", unit)
		}
	}
}

func TestRestoreRecoveryRejectsNativeParentSymlinkBeforeQuarantine(t *testing.T) {
	opts, op, _ := recoveryFixture(t, false, false)
	dir := filepath.Dir(opts.dbPath)
	// Complete snapshot has no native files. Even this case must inspect the
	// intermediate parent before the actual rollback's destructive traversal.
	if e := os.RemoveAll(filepath.Join(dir, "lists")); e != nil {
		t.Fatal(e)
	}
	sibling := t.TempDir()
	native := filepath.Join(sibling, "native")
	if e := os.Mkdir(native, 0o700); e != nil {
		t.Fatal(e)
	}
	sentinel := filepath.Join(native, "external-sentinel")
	if e := os.WriteFile(sentinel, []byte("synthetic sibling must remain unchanged"), 0o600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(sibling, filepath.Join(dir, "lists")); e != nil {
		t.Fatal(e)
	}
	if e := recoverRestore(context.Background(), opts, op, false); e == nil {
		t.Fatal("actual recovery followed external native parent")
	}
	b, e := os.ReadFile(sentinel)
	if e != nil || string(b) != "synthetic sibling must remain unchanged" {
		t.Fatalf("recovery touched sibling sentinel: %v", e)
	}
}
