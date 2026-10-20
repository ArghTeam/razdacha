package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/packaging"
)

// fixtureRestore contains ALL service/config effects in an independent temporary root.
// Existing archive/preservation tests exercise the production transaction with this adapter.
func fixtureRestore(t *testing.T, ctx context.Context, opts restoreOptions, capture ...func(restoreOptions)) error {
	t.Helper()
	dir := filepath.Dir(opts.dbPath)
	if e := os.Chmod(dir, 0o700); e != nil {
		return e
	}
	root := t.TempDir()
	cgroup := filepath.Join(root, "sys", "fs", "cgroup")
	if e := os.MkdirAll(cgroup, 0o700); e != nil {
		return e
	}
	if e := os.WriteFile(filepath.Join(cgroup, "cgroup.controllers"), []byte("cpu memory pids\n"), 0o600); e != nil {
		return e
	}
	active := map[string]bool{packaging.DaemonUnit: opts.unitActive != nil && opts.unitActive(ctx), packaging.SingboxUnit: true}
	for _, unit := range []string{packaging.DaemonUnit, packaging.SingboxUnit} {
		var content string
		var e error
		if unit == packaging.DaemonUnit {
			content, e = packaging.RenderDaemonUnit(defaultDaemon)
		} else {
			content, e = packaging.RenderSingboxUnit("/usr/local/bin/sing-box", "/etc/sing-box/config.json")
		}
		if e != nil {
			return e
		}
		path := filepath.Join(root, packaging.DefaultUnitDir, unit)
		if e = os.MkdirAll(filepath.Dir(path), 0o755); e != nil {
			return e
		}
		if e = os.WriteFile(path, []byte(content), 0o600); e != nil {
			return e
		}
		drop := filepath.Join(root, packaging.DefaultUnitDir, unit+".d", packaging.RestoreDropIn)
		if e = os.MkdirAll(filepath.Dir(drop), 0o755); e != nil {
			return e
		}
		if e = os.WriteFile(drop, []byte(packaging.RestoreCondition(unit)), 0o600); e != nil {
			return e
		}
	}
	config := filepath.Join(root, "/etc/sing-box/config.json")
	if e := os.MkdirAll(filepath.Dir(config), 0o700); e != nil {
		return e
	}
	if e := os.WriteFile(config, []byte(`{"fixture":"current-destination-only"}`), 0o600); e != nil {
		return e
	}
	oldRun := opts.systemctl
	services := &packaging.RestoreServices{Root: root}
	services.Run = func(ctx context.Context, args ...string) error {
		if oldRun != nil {
			if e := oldRun(ctx, args...); e != nil {
				return e
			}
		}
		if len(args) == 2 {
			if args[0] == "stop" {
				active[args[1]] = false
			}
			if args[0] == "start" {
				active[args[1]] = true
			}
		}
		return nil
	}
	services.Query = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) < 2 || args[0] != "show" {
			return nil, fmt.Errorf("unsupported fixture service query")
		}
		unit := args[1]
		binary, argv := defaultDaemon, defaultDaemon
		condition := ""
		if unit == packaging.SingboxUnit {
			binary = "/usr/local/bin/sing-box"
			argv = binary + " -D /var/lib/sing-box run -c /etc/sing-box/config.json"
			condition = "{ path=/usr/local/bin/razdacha ; argv[]=/usr/local/bin/razdacha runtime-start-allowed ; ignore_errors=no ; }"
		}
		state, pid := "inactive", "0"
		if active[unit] {
			state = "active"
			pid = "123"
		}
		props := map[string]string{"FragmentPath": filepath.Join(packaging.DefaultUnitDir, unit), "DropInPaths": filepath.Join(packaging.DefaultUnitDir, unit+".d", packaging.RestoreDropIn), "ExecStart": "{ path=" + binary + " ; argv[]=" + argv + " ; ignore_errors=no ; }", "ExecStartPre": "", "ExecStartPost": "", "ExecCondition": condition, "Environment": "", "EnvironmentFiles": "", "ActiveState": state, "SubState": "dead", "UnitFileState": "enabled", "MainPID": pid, "ControlPID": "0", "ControlGroup": ""}
		var b strings.Builder
		for key, value := range props {
			fmt.Fprintf(&b, "%s=%s\n", key, value)
		}
		return []byte(b.String()), nil
	}
	opts.services = services
	if opts.attest == nil {
		opts.attest = func(context.Context, string) ([]byte, error) {
			return []byte("synthetic-capability-and-file-identity"), nil
		}
	}
	opts.checklist = true
	if opts.closeIngress == nil {
		opts.closeIngress = func() error { return nil }
	}
	for _, save := range capture {
		save(opts)
	}
	return restore(ctx, opts)
}

func TestRestoreRefusesMissingOrOldInstalledHelperBeforeMutation(t *testing.T) {
	for _, name := range []string{"missing", "old"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "state.db")
			if e := os.WriteFile(dbPath, stateWithPeer(t, "Fixture destination"), 0o600); e != nil {
				t.Fatal(e)
			}
			file := writeFile(t, "incoming.db", stateWithPeer(t, "Fixture incoming"))
			mutations := 0
			opts := restoreOptions{dbPath: dbPath, file: file, force: true, systemctl: func(context.Context, ...string) error { mutations++; return nil }, closeIngress: func() error { mutations++; return nil }}
			opts.attest = func(_ context.Context, path string) ([]byte, error) {
				if filepath.Base(path) == "razdacha" {
					if name == "missing" {
						return nil, os.ErrNotExist
					}
					return nil, fmt.Errorf("old helper has no restore protocol")
				}
				return []byte("synthetic-daemon"), nil
			}
			if e := fixtureRestore(t, context.Background(), opts); e == nil {
				t.Fatal("unsupported installed helper admitted")
			}
			if mutations != 0 {
				t.Fatal("helper rejection followed service/guard mutation")
			}
			if got := peerNames(t, dbPath); len(got) != 1 || got[0] != "Fixture destination" {
				t.Fatal("helper rejection changed destination DB")
			}
		})
	}
}

func TestRestoreHelperIdentityChangeKeepsDaemonBootInhibited(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	file := writeFile(t, "incoming.db", stateWithPeer(t, "Fixture incoming"))
	helperCalls, starts := 0, 0
	opts := restoreOptions{dbPath: dbPath, file: file, unitActive: func(context.Context) bool { return true }, systemctl: func(_ context.Context, args ...string) error {
		if len(args) > 0 && args[0] == "start" {
			starts++
		}
		return nil
	}}
	opts.attest = func(_ context.Context, path string) ([]byte, error) {
		if filepath.Base(path) == "razdacha" {
			helperCalls++
			if helperCalls > 1 {
				return []byte("changed-helper"), nil
			}
		}
		return []byte("synthetic-initial-identity"), nil
	}
	if e := fixtureRestore(t, context.Background(), opts); e == nil {
		t.Fatal("changed helper accepted before daemon startup")
	}
	if starts != 0 {
		t.Fatal("daemon started after helper identity changed")
	}
	if _, e := os.Stat(filepath.Join(dir, "restore-incomplete")); e != nil {
		t.Fatal("helper mismatch removed durable daemon boot inhibition")
	}
}

func TestRestoreRequiresUnifiedCgroupEvidenceBeforeMutation(t *testing.T) {
	for _, kind := range []string{"missing", "unreadable", "nonregular"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "unreadable" && os.Geteuid() == 0 {
				t.Skip("root can read mode-000 files; missing/nonregular evidence is separately covered")
			}
			dir := t.TempDir()
			db := filepath.Join(dir, "state.db")
			original := stateWithPeer(t, "Synthetic retained destination")
			if e := os.WriteFile(db, original, 0o600); e != nil {
				t.Fatal(e)
			}
			mutations := 0
			opts := restoreOptions{dbPath: db, file: writeFile(t, "incoming.db", stateWithPeer(t, "Synthetic incoming")), force: true, systemctl: func(context.Context, ...string) error { mutations++; return nil }, closeIngress: func() error { mutations++; return nil }}
			e := fixtureRestore(t, context.Background(), opts, func(opts restoreOptions) {
				path := filepath.Join(opts.services.Root, "sys", "fs", "cgroup", "cgroup.controllers")
				if kind == "unreadable" {
					if e := os.Chmod(path, 0); e != nil {
						t.Fatal(e)
					}
				} else {
					if e := os.Remove(path); e != nil {
						t.Fatal(e)
					}
					if kind == "nonregular" {
						if e := os.Mkdir(path, 0o700); e != nil {
							t.Fatal(e)
						}
					}
				}
			})
			if e == nil {
				t.Fatal("unproven cgroup-v2 topology admitted")
			}
			if mutations != 0 {
				t.Fatal("cgroup rejection followed service/guard mutation")
			}
			for _, name := range []string{"restore-state.json", "restore-incomplete", "restore-runtime-inhibit"} {
				if _, e := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(e) {
					t.Fatal("cgroup rejection installed trusted state/inhibitor")
				}
			}
			b, e := os.ReadFile(db)
			if e != nil || string(b) != string(original) {
				t.Fatal("cgroup rejection modified destination DB")
			}
		})
	}
}
