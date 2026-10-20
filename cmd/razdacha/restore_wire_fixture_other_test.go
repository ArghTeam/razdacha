//go:build !linux

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoreWireCLI(t *testing.T) { t.Skip("requires separately reviewed contained Linux runner") }

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
