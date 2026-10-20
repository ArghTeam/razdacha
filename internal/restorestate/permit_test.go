package restorestate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func permitFixture(t *testing.T) (string, string, State, Permit, KernelFacts) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	config := filepath.Join(dir, "config.json")
	if e := WritePrivate(config, []byte(`{"outbounds":[]}`)); e != nil {
		t.Fatal(e)
	}
	hash, e := configHash(config)
	if e != nil {
		t.Fatal(e)
	}
	s := State{Version: Protocol, Operation: "fixture", Recovery: filepath.Join(dir, "recovery"), Phase: "publishing", Publication: strings.Repeat("a", 32), ConfigHash: hash}
	if e = Save(dir, s); e != nil {
		t.Fatal(e)
	}
	facts := KernelFacts{BootID: "fixture-boot", PID: 123, ProcessStart: "456", NowNS: 1e9}
	p := Permit{Version: Protocol, Operation: s.Operation, Publication: s.Publication, ConfigHash: hash, BootID: facts.BootID, PID: facts.PID, ProcessStart: facts.ProcessStart, GrantedNS: facts.NowNS, DeadlineNS: facts.NowNS + permitLifetimeNS}
	writePermit(t, dir, p)
	return dir, config, s, p, facts
}
func writePermit(t *testing.T, dir string, p Permit) {
	t.Helper()
	b, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = WritePrivate(filepath.Join(dir, PermitFile), b); e != nil {
		t.Fatal(e)
	}
}

func TestPublishingPermitPositiveAndFiniteFailureControls(t *testing.T) {
	dir, config, _, p, f := permitFixture(t)
	alive := func(pid int, start string) bool { return pid == f.PID && start == f.ProcessStart }
	if e := runtimeStartAllowed(dir, config, f, alive); e != nil {
		t.Fatalf("positive controlled start rejected: %v", e)
	}
	for _, name := range []string{"reboot", "expired", "dead", "reused", "hash", "nonce", "unbounded", "future"} {
		t.Run(name, func(t *testing.T) {
			trial := p
			now := f
			check := alive
			switch name {
			case "reboot":
				now.BootID = "another-boot"
			case "expired":
				now.NowNS = p.DeadlineNS + 1
			case "dead":
				check = func(int, string) bool { return false }
			case "reused":
				trial.ProcessStart = "different"
			case "hash":
				trial.ConfigHash = strings.Repeat("f", 64)
			case "nonce":
				trial.Publication = strings.Repeat("b", 32)
			case "unbounded":
				trial.DeadlineNS = trial.GrantedNS + permitLifetimeNS + 1
			case "future":
				now.NowNS = p.GrantedNS - 1
			}
			writePermit(t, dir, trial)
			if e := runtimeStartAllowed(dir, config, now, check); e == nil {
				t.Fatal("uncontrolled runtime start admitted")
			}
			writePermit(t, dir, p)
		})
	}
	if e := WritePrivate(config, []byte(`{"endpoints":[{"type":"wireguard"}]}`)); e != nil {
		t.Fatal(e)
	}
	if e := runtimeStartAllowed(dir, config, f, alive); e == nil {
		t.Fatal("changed config admitted")
	}
}

func TestPublishingPermitRejectsMalformedSafetyFields(t *testing.T) {
	dir, config, _, p, f := permitFixture(t)
	raw, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	variants := []string{strings.Replace(string(raw), `"daemon_pid":123,`, "", 1), strings.Replace(string(raw), `"daemon_pid":123`, `"daemon_pid":null`, 1), strings.Replace(string(raw), `"daemon_pid":123`, `"daemon_pid":123,"daemon_pid":123`, 1), string(raw) + ` {}`, strings.Replace(string(raw), `"daemon_pid":123`, `"daemon_pid":"123"`, 1)}
	for _, v := range variants {
		if e := WritePrivate(filepath.Join(dir, PermitFile), []byte(v)); e != nil {
			t.Fatal(e)
		}
		if e := runtimeStartAllowed(dir, config, f, func(int, string) bool { return true }); e == nil {
			t.Fatal("malformed permit implied controlled start")
		}
	}
}

func TestAbortRequiresPositiveStopBeforeRetryableRecord(t *testing.T) {
	dir, config, _, _, f := permitFixture(t)
	c := &Controller{Dir: dir}
	if e := c.FinishPublication(false); e == nil {
		t.Fatal("Abort claimed stopped state without adapter")
	}
	s, e := privateRecord(dir)
	if e != nil || s.Phase != "publishing" {
		t.Fatal("uncertain Abort became retryable")
	}
	if e := runtimeStartAllowed(dir, config, f, func(int, string) bool { return true }); e == nil {
		t.Fatal("Abort did not inhibit independent new starts")
	}
	stopped := false
	c.StopRuntime = func() error {
		if _, e := os.Stat(filepath.Join(dir, Runtime)); e != nil {
			t.Fatal("stop before durable start inhibition")
		}
		if _, e := os.Stat(filepath.Join(dir, PermitFile)); !os.IsNotExist(e) {
			t.Fatal("stop before permit revocation")
		}
		stopped = true
		return nil
	}
	if e := c.FinishPublication(false); e != nil {
		t.Fatal(e)
	}
	s, e = Read(dir)
	if e != nil || !stopped || s.Phase != "complete" || !s.RuntimeInhibited {
		t.Fatalf("retryable Abort state invalid: %v", e)
	}
}

func TestOrphanPublishingEvidenceCannotBecomeOrdinaryInstallation(t *testing.T) {
	dir, config, _, _, facts := permitFixture(t)
	if e := os.Remove(filepath.Join(dir, File)); e != nil {
		t.Fatal(e)
	}
	facts.NowNS += 2 * permitLifetimeNS
	if e := runtimeStartAllowed(dir, config, facts, func(int, string) bool { return false }); e == nil {
		t.Fatal("missing authority bypassed expired/dead publishing permit")
	}
	if e := os.Remove(filepath.Join(dir, PermitFile)); e != nil {
		t.Fatal(e)
	}
	recovery := filepath.Join(dir, ".restore-fixture")
	if e := os.Mkdir(recovery, 0o700); e != nil {
		t.Fatal(e)
	}
	if e := WritePrivate(filepath.Join(recovery, "recovery.json"), []byte(`{"incomplete":"fixture"}`)); e != nil {
		t.Fatal(e)
	}
	if _, e := Read(dir); e == nil {
		t.Fatal("missing authority ignored retained recovery evidence")
	}
	if e := os.Remove(filepath.Join(recovery, "recovery.json")); e != nil {
		t.Fatal(e)
	}
	if e := runtimeStartAllowed(dir, config, facts, func(int, string) bool { return false }); e != nil {
		t.Fatalf("positive ordinary installation control failed: %v", e)
	}
}
