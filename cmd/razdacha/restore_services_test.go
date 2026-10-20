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
func fixtureRestore(t *testing.T, ctx context.Context, opts restoreOptions) error {
	t.Helper()
	dir := filepath.Dir(opts.dbPath)
	if e := os.Chmod(dir, 0o700); e != nil {
		return e
	}
	root := t.TempDir()
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
	opts.attest = func(context.Context, string) ([]byte, error) {
		return []byte("synthetic-capability-and-file-identity"), nil
	}
	opts.checklist = true
	if opts.closeIngress == nil {
		opts.closeIngress = func() error { return nil }
	}
	return restore(ctx, opts)
}
