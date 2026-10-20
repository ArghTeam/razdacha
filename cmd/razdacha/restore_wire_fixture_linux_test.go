//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/packaging"
)

// TestRestoreWireCLI is a subprocess adapter for the encrypted-wire fixture.
// It calls the actual production transaction with synthetic service metadata.
// All units/configuration and transaction material live beneath the supplied
// private fixture root. No adapter invokes systemctl or a host service socket.
func TestRestoreWireCLI(t *testing.T) {
	if os.Getenv("RAZDACHA_RESTORE_WIRE_CLI") != "1" {
		t.Skip("contained parent fixture only")
	}
	if os.Geteuid() != 0 {
		t.Fatal("fixture requires contained root")
	}
	for _, kind := range []string{"net", "mnt"} {
		actual, err := os.Readlink("/proc/self/ns/" + kind)
		if err != nil {
			t.Fatal(err)
		}
		original := os.Getenv("RAZDACHA_RW_HOST_" + strings.ToUpper(kind))
		if original == "" || actual == original {
			t.Fatal("CLI refuses unverified isolation")
		}
	}
	root := os.Getenv("RAZDACHA_RW_ROOT")
	if !filepath.IsAbs(root) || !strings.HasPrefix(root, "/var/lib/restore-wire-") {
		t.Fatal("invalid private fixture root")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rwCLIActual(t, ctx, root)
}

// BEGIN FIXED CLI ADAPTER
func rwCLIActual(t *testing.T, ctx context.Context, root string) {
	t.Helper()
	// Model unified cgroup-v2 evidence inside the private service root. This
	// never reads or writes a host cgroup control file.
	controllers := filepath.Join(root, "sys/fs/cgroup/cgroup.controllers")
	if err := os.MkdirAll(filepath.Dir(controllers), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(controllers, []byte("cpu memory pids\n"), 0600); err != nil {
		t.Fatal(err)
	}
	services := &packaging.RestoreServices{Root: root}
	active := map[string]bool{}
	services.Run = func(_ context.Context, args ...string) error {
		if len(args) == 1 && args[0] == "daemon-reload" {
			return nil
		}
		if len(args) == 2 && (args[0] == "stop" || args[0] == "start") {
			active[args[1]] = args[0] == "start"
			return nil
		}
		return fmt.Errorf("unsupported contained service action %q", args)
	}
	services.Query = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) < 2 || args[0] != "show" {
			return nil, fmt.Errorf("unsupported contained query")
		}
		unit := args[1]
		if unit != packaging.DaemonUnit && unit != packaging.SingboxUnit {
			return nil, fmt.Errorf("unknown contained unit")
		}
		binary, argv := "/usr/local/bin/razdachad", "/usr/local/bin/razdachad"
		condition := ""
		if unit == packaging.SingboxUnit {
			binary = "/usr/local/bin/sing-box"
			argv = binary + " -D /var/lib/sing-box run -c /etc/sing-box/config.json"
			condition = "{ path=/usr/local/bin/razdacha ; argv[]=/usr/local/bin/razdacha runtime-start-allowed ; ignore_errors=no ; }"
		}
		state, pid := "inactive", "0"
		if active[unit] {
			state, pid = "active", "123"
		}
		props := map[string]string{"FragmentPath": filepath.Join(packaging.DefaultUnitDir, unit), "DropInPaths": filepath.Join(packaging.DefaultUnitDir, unit+".d", packaging.RestoreDropIn), "ExecStart": "{ path=" + binary + " ; argv[]=" + argv + " ; ignore_errors=no ; }", "ExecCondition": condition, "ExecStartPre": "", "ExecStartPost": "", "Environment": "", "EnvironmentFiles": "", "ActiveState": state, "SubState": "dead", "UnitFileState": "enabled", "MainPID": pid, "ControlPID": "0", "ControlGroup": ""}
		var b strings.Builder
		for k, v := range props {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
		return []byte(b.String()), nil
	}
	for _, unit := range []string{packaging.DaemonUnit, packaging.SingboxUnit} {
		var data string
		var err error
		if unit == packaging.DaemonUnit {
			data, err = packaging.RenderDaemonUnit("/usr/local/bin/razdachad")
		} else {
			data, err = packaging.RenderSingboxUnit("/usr/local/bin/sing-box", "/etc/sing-box/config.json")
		}
		if err != nil {
			t.Fatal(err)
		}
		for path, body := range map[string]string{filepath.Join(root, packaging.DefaultUnitDir, unit): data, filepath.Join(root, packaging.DefaultUnitDir, unit+".d", packaging.RestoreDropIn): packaging.RestoreCondition(unit)} {
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	opts := restoreOptions{dbPath: filepath.Join(root, "state.db"), file: filepath.Join(root, "archive.db"), force: true, services: services, checklist: true, closeIngress: func() error { return netstack.SetClientGuard(true) }, attest: func(context.Context, string) ([]byte, error) {
		return []byte("contained-synthetic-capability-adapter"), nil
	}}
	if err := restore(ctx, opts); err != nil {
		t.Fatal(err)
	}
}

// END FIXED CLI ADAPTER

// Fixture-only adapter for pinned main 0591236: extract this comment into the
// fixed CLI adapter section. Imports and every production file stay unchanged.
// This calls the genuine old restore(), not a reimplementation of its DB copy.
// BEGIN BASELINE CLI ADAPTER
/*
func rwCLIActual(t *testing.T,ctx context.Context,root string) {
 t.Helper()
 opts:=restoreOptions{
  dbPath:filepath.Join(root,"state.db"),file:filepath.Join(root,"archive.db"),force:true,
  unitActive:func(context.Context)bool{return false},
  systemctl:func(_ context.Context,args ...string)error{
   if len(args)==2 && args[1]==packaging.DaemonUnit && (args[0]=="stop"||args[0]=="start"){return nil}
   return fmt.Errorf("unexpected old restore service action %q",args)
  },
  closeIngress:func()error{return netstack.SetClientGuard(true)},
 }
 if err:=restore(ctx,opts);err!=nil{t.Fatal(err)}
}
*/
// END BASELINE CLI ADAPTER
