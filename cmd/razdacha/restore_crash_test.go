package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/restorestate"
)

func TestRestoreProcessKillAtDurableCutsRequiresExplicitRecovery(t *testing.T) {
	cuts := []string{"recovery-metadata", "preparing-record", "inhibitors-installed", "services-stopped", "ingress-guarded", "recovery-complete", "runtime-quarantined", "authority-invalidated", "database-published", "restore-complete"}
	for _, cut := range cuts {
		t.Run(cut, func(t *testing.T) {
			root := t.TempDir()
			if e := os.Chmod(root, 0o700); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestRestoreCrashHelper$")
			cmd.Env = append(os.Environ(), "RAZDACHA_RESTORE_CRASH_ROOT="+root, "RAZDACHA_RESTORE_CRASH_CUT="+cut)
			out, e := cmd.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			input, e := cmd.StdinPipe()
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = input.Close() }()
			cmd.Stderr = os.Stderr
			if e = cmd.Start(); e != nil {
				t.Fatal(e)
			}
			reaped := false
			defer func() {
				if !reaped {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			ready := make(chan error, 1)
			go func() {
				scanner := bufio.NewScanner(out)
				for scanner.Scan() {
					if scanner.Text() == "restore-cut-ready" {
						ready <- nil
						return
					}
				}
				ready <- fmt.Errorf("durable cut missing: %w", scanner.Err())
			}()
			select {
			case e = <-ready:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("restore child cut deadline")
			}
			if e = cmd.Process.Kill(); e != nil {
				t.Fatal(e)
			}
			if e = cmd.Wait(); e == nil {
				t.Fatal("process kill reported successful restore")
			}
			reaped = true
			// SIGKILL bypasses every Go defer. This proves process-interruption ordering,
			// not physical power loss, kernel reboot, or storage-controller flush behavior.
			db := filepath.Join(root, "destination", "state.db")
			dir := filepath.Dir(db)
			expected := "Synthetic crash original"
			if cut == "database-published" || cut == "restore-complete" {
				expected = "Synthetic crash imported"
			}
			if got := peerNames(t, db); len(got) != 1 || got[0] != expected {
				t.Fatalf("wrong complete DB after kill at %s: %v", cut, got)
			}
			ctl := &restorestate.Controller{Dir: dir}
			if e = ctl.Allowed(); e == nil {
				t.Fatal("interrupted restore allowed callbacks")
			}
			operations, e := filepath.Glob(filepath.Join(dir, ".restore-*"))
			if e != nil || len(operations) != 1 {
				t.Fatalf("unique recovery directory missing: %v", e)
			}
			serviceRoot, e := os.ReadFile(filepath.Join(root, "service-root"))
			if e != nil {
				t.Fatal(e)
			}
			opts := restoreOptions{dbPath: db, services: crashRecoveryServices(string(serviceRoot)), attest: func(context.Context, string) ([]byte, error) {
				return []byte("synthetic-capability-and-file-identity"), nil
			}, closeIngress: func() error { return nil }}
			if e = recoverRestore(context.Background(), opts, operations[0], false); e != nil {
				t.Fatalf("explicit recovery after process kill: %v", e)
			}
			s, e := restorestate.Read(dir)
			if e != nil || s.Phase != "recovered" || !s.Held || !s.RuntimeInhibited {
				t.Fatalf("recovery granted activation: %+v %v", s, e)
			}
			if got := peerNames(t, db); got[0] != "Synthetic crash original" {
				t.Fatal("kill recovery used imported/migration archive")
			}
		})
	}
}

// Test-only child never contacts systemd, runtime sockets or external URLs. All
// artifacts, adapters and archive credentials are newly synthetic and parent-owned.
func TestRestoreCrashHelper(t *testing.T) {
	root := os.Getenv("RAZDACHA_RESTORE_CRASH_ROOT")
	if root == "" {
		return
	}
	info, e := os.Lstat(root)
	if e != nil || !filepath.IsAbs(root) || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatal("invalid private crash fixture root")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		t.Fatal("crash fixture ownership mismatch")
	}
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 0 {
		t.Fatal("crash fixture root must start empty")
	}
	temp := filepath.Join(root, "child-tmp")
	if e = os.Mkdir(temp, 0o700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("TMPDIR", temp)
	dir := filepath.Join(root, "destination")
	if e = os.Mkdir(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	db := filepath.Join(dir, "state.db")
	if e = os.WriteFile(db, stateWithPeer(t, "Synthetic crash original"), 0o600); e != nil {
		t.Fatal(e)
	}
	opts := restoreOptions{dbPath: db, file: writeFile(t, "incoming.db", stateWithPeer(t, "Synthetic crash imported")), force: true}
	cut := os.Getenv("RAZDACHA_RESTORE_CRASH_CUT")
	opts.afterDurable = func(observed string) error {
		if observed == cut {
			fmt.Println("restore-cut-ready")
			_, _ = io.Copy(io.Discard, os.Stdin)
			return fmt.Errorf("crash observer released without kill")
		}
		return nil
	}
	if e = fixtureRestore(t, context.Background(), opts, func(opts restoreOptions) {
		if e := os.WriteFile(filepath.Join(root, "service-root"), []byte(opts.services.Root), 0o600); e != nil {
			t.Fatal(e)
		}
	}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("requested durable cut was not reached")
}

func crashRecoveryServices(root string) *packaging.RestoreServices {
	return &packaging.RestoreServices{Root: root, Run: func(context.Context, ...string) error { return nil }, Query: func(_ context.Context, args ...string) ([]byte, error) {
		unit := args[1]
		binary, argv := defaultDaemon, defaultDaemon
		condition := ""
		if unit == packaging.SingboxUnit {
			binary = "/usr/local/bin/sing-box"
			argv = binary + " -D /var/lib/sing-box run -c /etc/sing-box/config.json"
			condition = "{ path=/usr/local/bin/razdacha ; argv[]=/usr/local/bin/razdacha runtime-start-allowed ; ignore_errors=no ; }"
		}
		properties := map[string]string{"FragmentPath": filepath.Join(packaging.DefaultUnitDir, unit), "DropInPaths": filepath.Join(packaging.DefaultUnitDir, unit+".d", packaging.RestoreDropIn), "ExecStart": "{ path=" + binary + " ; argv[]=" + argv + " ; ignore_errors=no ; }", "ExecCondition": condition, "ExecStartPre": "", "ExecStartPost": "", "Environment": "", "EnvironmentFiles": "", "ActiveState": "inactive", "SubState": "dead", "UnitFileState": "enabled", "MainPID": "0", "ControlPID": "0", "ControlGroup": ""}
		var output strings.Builder
		for key, value := range properties {
			fmt.Fprintf(&output, "%s=%s\n", key, value)
		}
		return []byte(output.String()), nil
	}}
}
