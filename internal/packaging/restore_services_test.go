package packaging

import (
	"context"
	"errors"
	"fmt"
	"github.com/ArghTeam/razdacha/internal/restorestate"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttestInstalledHelperPositiveAndUnsupportedControls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture-helper")
	valid := "#!/bin/sh\n[ \"$1\" = '-restore-capabilities' ] || exit 2\nprintf '%s\\n' '{\"restore_protocol\":1}'\n"
	if e := os.WriteFile(path, []byte(valid), 0o700); e != nil {
		t.Fatal(e)
	}
	initial, e := AttestRestoreBinary(context.Background(), path)
	if e != nil || len(initial) == 0 {
		t.Fatalf("positive harmless helper failed: %v", e)
	}
	if _, e = AttestRestoreBinary(context.Background(), filepath.Join(dir, "missing")); e == nil {
		t.Fatal("missing helper admitted")
	}
	old := filepath.Join(dir, "old-helper")
	if e = os.WriteFile(old, []byte("#!/bin/sh\nexit 2\n"), 0o700); e != nil {
		t.Fatal(e)
	}
	if _, e = AttestRestoreBinary(context.Background(), old); e == nil {
		t.Fatal("old helper admitted")
	}
	alias := filepath.Join(dir, "alias")
	if e = os.Symlink(path, alias); e != nil {
		t.Fatal(e)
	}
	if _, e = AttestRestoreBinary(context.Background(), alias); e == nil {
		t.Fatal("symlink helper admitted")
	}
	if e = os.WriteFile(path, []byte(valid+"# different verified content\n"), 0o700); e != nil {
		t.Fatal(e)
	}
	changed, e := AttestRestoreBinary(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	if string(initial) == string(changed) {
		t.Fatal("helper content mutation did not change attested identity")
	}
}

func TestAttestRejectsAmbiguousCapability(t *testing.T) {
	for _, payload := range []string{`{"restore_protocol":null}`, `{"restore_protocol":0,"restore_protocol":1}`, `{"restore_protocol":1,"unknown":true}`, `{"restore_protocol":1} {}`, `{"restore_protocol":"1"}`} {
		path := filepath.Join(t.TempDir(), "synthetic-helper")
		if e := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+payload+"'\n"), 0o700); e != nil {
			t.Fatal(e)
		}
		if _, e := AttestRestoreBinary(context.Background(), path); e == nil {
			t.Fatalf("ambiguous capability admitted: %s", payload)
		}
	}
}

func TestRestoreInhibitReloadFailureRetainsBothMarkers(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	services := RestoreServices{Root: root, Run: func(context.Context, ...string) error { return errors.New("synthetic reload failure") }}
	if e := services.Inhibit(context.Background(), dir); e == nil {
		t.Fatal("reload failure hidden")
	}
	for _, marker := range []string{restorestate.Incomplete, restorestate.Runtime} {
		b, e := restorestate.ReadPrivate(filepath.Join(dir, marker), 64)
		if e != nil || string(b) != "1\n" {
			t.Fatalf("reload failure omitted durable %s restriction: %v", marker, e)
		}
	}
}

func TestRestoreStopRequiresPositiveServiceAndDescendantEvidence(t *testing.T) {
	for _, kind := range []string{"empty", "removed", "descendant", "missing-process-file", "unknown-path", "query-error", "active", "main-pid", "control-pid"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			calls := 0
			for _, unit := range []string{DaemonUnit, SingboxUnit} {
				group := filepath.Join(root, "sys", "fs", "cgroup", "system.slice", unit)
				if kind != "removed" {
					if e := os.MkdirAll(group, 0o700); e != nil {
						t.Fatal(e)
					}
				}
				if kind != "removed" && kind != "missing-process-file" {
					data := ""
					if kind == "descendant" {
						data = "4242\n"
					}
					if e := os.WriteFile(filepath.Join(group, "cgroup.procs"), []byte(data), 0o600); e != nil {
						t.Fatal(e)
					}
				}
			}
			services := RestoreServices{Root: root, Run: func(context.Context, ...string) error { calls++; return nil }}
			services.Query = func(_ context.Context, args ...string) ([]byte, error) {
				if kind == "query-error" {
					return nil, errors.New("synthetic uncertain service query")
				}
				values := map[string]string{"ActiveState": "inactive", "MainPID": "0", "ControlPID": "0", "ControlGroup": "/system.slice/" + args[1]}
				switch kind {
				case "unknown-path":
					values["ControlGroup"] = "/unexpected.slice/unknown"
				case "active":
					values["ActiveState"] = "active"
				case "main-pid":
					values["MainPID"] = "4242"
				case "control-pid":
					values["ControlPID"] = "4242"
				}
				var out strings.Builder
				for _, arg := range args {
					if name, ok := strings.CutPrefix(arg, "--property="); ok {
						fmt.Fprintf(&out, "%s=%s\n", name, values[name])
					}
				}
				return []byte(out.String()), nil
			}
			e := services.Stop(context.Background())
			positive := kind == "empty" || kind == "removed"
			if positive {
				if e != nil || calls != 2 {
					t.Fatalf("positive complete dual-stop missing: %v %d", e, calls)
				}
			} else if e == nil {
				t.Fatal("uncertain/live descendant state treated as stopped")
			}
		})
	}
}
