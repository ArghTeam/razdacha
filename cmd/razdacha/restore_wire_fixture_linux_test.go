//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
	proof, err := rwReadProof("cli")
	if err != nil {
		t.Fatal(err)
	}
	if err = unix.Close(3); err != nil {
		t.Fatal(err)
	}
	if err = unix.Close(4); err != nil {
		t.Fatal(err)
	}
	root := proof.Root
	if os.Getenv("RAZDACHA_RW_ROOT") != root {
		t.Fatal("CLI root does not match owned proof")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rwCLIActual(t, ctx, root)
}

// BEGIN PORTABLE PROVENANCE VALIDATOR
// These facts come from live read-only kernel/filesystem observers, never from
// caller-supplied namespace environment strings.
type rwProof struct {
	OwnerPID, HostPID                                                                         int
	OwnerStart, HostStart, OwnerNet, OwnerMount, HostNet, HostMount, ClientNet, SinkNet, Root string
}
type rwProofFacts struct {
	ParentPID, OuterPID                                                                                        int
	ParentStart, OuterStart, ParentNet, ParentMount, OuterNet, OuterMount, SelfNet, SelfMount, TargetNet, Root string
	Sealed, PrivateTmpfs, PrivateRoot                                                                          bool
}

func rwValidateProof(p rwProof, f rwProofFacts, role string) error {
	if !f.Sealed || p.OwnerPID <= 0 || p.HostPID <= 0 || p.OwnerStart == "" || p.HostStart == "" || p.OwnerNet == "" || p.OwnerMount == "" || p.HostNet == "" || p.HostMount == "" || p.ClientNet == "" || p.SinkNet == "" {
		return fmt.Errorf("missing owned sealed namespace provenance")
	}
	if f.ParentPID != p.OwnerPID || f.ParentStart != p.OwnerStart || f.OuterPID != p.HostPID || f.OuterStart != p.HostStart {
		return fmt.Errorf("owned parent lifetime mismatch")
	}
	if f.ParentNet != p.OwnerNet || f.ParentMount != p.OwnerMount || f.OuterNet != p.HostNet || f.OuterMount != p.HostMount {
		return fmt.Errorf("live parent namespace provenance mismatch")
	}
	if p.ClientNet == p.SinkNet || p.ClientNet == p.HostNet || p.SinkNet == p.HostNet || p.ClientNet == p.OwnerNet || p.SinkNet == p.OwnerNet || p.OwnerNet == p.HostNet || p.OwnerMount == p.HostMount || f.SelfMount != p.OwnerMount || f.SelfNet == p.HostNet {
		return fmt.Errorf("descendant is outside fresh owned namespaces")
	}
	if !f.PrivateTmpfs || !f.PrivateRoot || f.Root != p.Root || filepath.Dir(p.Root) != "/var/lib" || !strings.HasPrefix(filepath.Base(p.Root), "restore-wire-") {
		return fmt.Errorf("private fixture filesystem provenance missing")
	}
	switch role {
	case "enter":
		if f.SelfNet != p.OwnerNet || f.TargetNet == "" || f.TargetNet == p.HostNet || f.TargetNet == p.OwnerNet || (f.TargetNet != p.ClientNet && f.TargetNet != p.SinkNet) {
			return fmt.Errorf("unsafe namespace entry target")
		}
	case "sink", "probe":
		if (role == "sink" && f.TargetNet != p.SinkNet) || (role == "probe" && f.TargetNet != p.ClientNet) {
			return fmt.Errorf("wrong owned namespace role")
		}
		if f.TargetNet == "" || f.SelfNet != f.TargetNet || f.TargetNet == p.OwnerNet || (f.TargetNet != p.ClientNet && f.TargetNet != p.SinkNet) {
			return fmt.Errorf("wrong owned child namespace")
		}
	case "cli":
		if f.SelfNet != p.OwnerNet {
			return fmt.Errorf("CLI escaped owner network namespace")
		}
	default:
		return fmt.Errorf("unknown proof role")
	}
	return nil
}
func TestRestoreWireGuardProvenanceValidation(t *testing.T) {
	p := rwProof{OwnerPID: 20, HostPID: 10, OwnerStart: "200", HostStart: "100", OwnerNet: "net:[20]", OwnerMount: "mnt:[20]", HostNet: "net:[10]", HostMount: "mnt:[10]", ClientNet: "net:[40]", SinkNet: "net:[30]", Root: "/var/lib/restore-wire-fixture"}
	valid := rwProofFacts{ParentPID: 20, OuterPID: 10, ParentStart: "200", OuterStart: "100", ParentNet: p.OwnerNet, ParentMount: p.OwnerMount, OuterNet: p.HostNet, OuterMount: p.HostMount, SelfNet: "net:[30]", SelfMount: p.OwnerMount, TargetNet: "net:[30]", Root: p.Root, Sealed: true, PrivateTmpfs: true, PrivateRoot: true}
	if err := rwValidateProof(p, valid, "sink"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*rwProofFacts){
		"arbitrary environment has no sealed proof": func(f *rwProofFacts) { f.Sealed = false },
		"unowned parent":                      func(f *rwProofFacts) { f.ParentPID++ },
		"reused parent PID":                   func(f *rwProofFacts) { f.ParentStart = "reused" },
		"outer parent changed":                func(f *rwProofFacts) { f.OuterStart = "reused" },
		"source namespace strings fabricated": func(f *rwProofFacts) { f.OuterNet = "net:[fabricated]" },
		"host network":                        func(f *rwProofFacts) { f.SelfNet = p.HostNet; f.TargetNet = p.HostNet },
		"host mount":                          func(f *rwProofFacts) { f.SelfMount = p.HostMount },
		"shared mount propagation":            func(f *rwProofFacts) { f.PrivateTmpfs = false },
		"unowned root":                        func(f *rwProofFacts) { f.PrivateRoot = false },
		"root substitution":                   func(f *rwProofFacts) { f.Root = "/var/lib/other" },
		"wrong child network":                 func(f *rwProofFacts) { f.TargetNet = "net:[other]" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			facts := valid
			mutate(&facts)
			if rwValidateProof(p, facts, "sink") == nil {
				t.Fatal("unsafe entry accepted before mutation")
			}
		})
	}
	entry := valid
	entry.SelfNet = p.OwnerNet
	if err := rwValidateProof(p, entry, "enter"); err != nil {
		t.Fatal(err)
	}
	entry.TargetNet = p.HostNet
	if rwValidateProof(p, entry, "enter") == nil {
		t.Fatal("host target accepted")
	}
	cli := valid
	cli.SelfNet = p.OwnerNet
	if err := rwValidateProof(p, cli, "cli"); err != nil {
		t.Fatal(err)
	}
	cli.SelfNet = valid.SelfNet
	if rwValidateProof(p, cli, "cli") == nil {
		t.Fatal("CLI child namespace accepted")
	}
}

// END PORTABLE PROVENANCE VALIDATOR

func rwProofProcess(pid int) (int, string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, "", err
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return 0, "", fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) <= 19 {
		return 0, "", fmt.Errorf("short process stat")
	}
	parent, err := strconv.Atoi(fields[1])
	return parent, fields[19], err
}
func rwPrivateVarLib() bool {
	var stat unix.Statfs_t
	if unix.Statfs("/var/lib", &stat) != nil || stat.Type != unix.TMPFS_MAGIC {
		return false
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[4] != "/var/lib" {
			continue
		}
		sep := -1
		for i, v := range fields {
			if v == "-" {
				sep = i
				break
			}
		}
		if sep < 6 || sep+1 >= len(fields) || fields[sep+1] != "tmpfs" {
			return false
		}
		for _, v := range fields[6:sep] {
			if strings.HasPrefix(v, "shared:") || strings.HasPrefix(v, "master:") || strings.HasPrefix(v, "propagate_from:") {
				return false
			}
		}
		return true
	}
	return false
}
func rwReadProof(role string) (rwProof, error) {
	var p rwProof
	seals, err := unix.FcntlInt(4, unix.F_GET_SEALS, 0)
	if err != nil {
		return p, fmt.Errorf("missing inherited sealed proof: %w", err)
	}
	required := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	var stat unix.Stat_t
	if seals != required || unix.Fstat(4, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0777 != 0600 || stat.Size <= 0 || stat.Size > 4096 {
		return p, fmt.Errorf("invalid sealed proof descriptor")
	}
	data := make([]byte, int(stat.Size))
	n, err := unix.Pread(4, data, 0)
	if err != nil || n != len(data) {
		return p, fmt.Errorf("cannot read complete sealed proof")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return p, fmt.Errorf("trailing proof data")
	}
	f := rwProofFacts{ParentPID: os.Getppid(), Sealed: true}
	f.OuterPID, f.ParentStart, err = rwProofProcess(f.ParentPID)
	if err != nil {
		return p, err
	}
	_, f.OuterStart, err = rwProofProcess(f.OuterPID)
	if err != nil {
		return p, err
	}
	for _, item := range []struct {
		pid    int
		kind   string
		target *string
	}{{f.ParentPID, "net", &f.ParentNet}, {f.ParentPID, "mnt", &f.ParentMount}, {f.OuterPID, "net", &f.OuterNet}, {f.OuterPID, "mnt", &f.OuterMount}, {os.Getpid(), "net", &f.SelfNet}, {os.Getpid(), "mnt", &f.SelfMount}} {
		*item.target, err = os.Readlink(fmt.Sprintf("/proc/%d/ns/%s", item.pid, item.kind))
		if err != nil {
			return p, err
		}
	}
	if role != "cli" {
		f.TargetNet, err = os.Readlink("/proc/self/fd/3")
		if err != nil {
			return p, err
		}
	}
	f.PrivateTmpfs = rwPrivateVarLib()
	f.Root, err = filepath.EvalSymlinks(p.Root)
	if err != nil {
		return p, err
	}
	var rootStat unix.Stat_t
	f.PrivateRoot = unix.Lstat(p.Root, &rootStat) == nil && rootStat.Mode&unix.S_IFMT == unix.S_IFDIR && rootStat.Mode&0777 == 0700 && rootStat.Uid == uint32(os.Geteuid())
	if err = rwValidateProof(p, f, role); err != nil {
		return p, err
	}
	return p, nil
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
