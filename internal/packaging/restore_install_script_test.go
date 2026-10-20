package packaging

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/restorestate"
)

// Only canonical path/owner spellings are overlaid. The production install and
// setup functions execute unchanged with synthetic archives and real flock calls.
func restoreInstallerFixture(t *testing.T, compatible bool) (string, string, string, []string) {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	prefix := filepath.Join(root, "bin")
	tools := filepath.Join(root, "tools")
	payload := filepath.Join(root, "payload")
	for _, dir := range []string{state, prefix, tools, payload} {
		if e := os.Mkdir(dir, 0o700); e != nil {
			t.Fatal(e)
		}
	}
	original, e := os.ReadFile(installScript(t))
	if e != nil {
		t.Fatal(e)
	}
	script := strings.ReplaceAll(string(original), "/var/lib/razdacha", state)
	script = strings.ReplaceAll(script, "0:700", strconv.Itoa(os.Geteuid())+":700")
	script = strings.ReplaceAll(script, "0:600", strconv.Itoa(os.Geteuid())+":600")
	scriptPath := filepath.Join(root, "install.sh")
	if e = os.WriteFile(scriptPath, []byte(script), 0o600); e != nil {
		t.Fatal(e)
	}
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("python3 required for portable equivalent stat/flock tools")
	}
	for name, body := range map[string]string{
		"stat":  "import os,stat,sys\ns=os.lstat(sys.argv[-1]);print(str(s.st_uid)+':'+format(stat.S_IMODE(s.st_mode),'o'))\n",
		"flock": "import fcntl,sys\nfcntl.flock(int(sys.argv[-1]),fcntl.LOCK_EX|fcntl.LOCK_NB)\n",
	} {
		if e = os.WriteFile(filepath.Join(tools, name), []byte("#!"+python+"\n"+body), 0o700); e != nil {
			t.Fatal(e)
		}
	}
	capability := "exit 2"
	if compatible {
		capability = "printf '%s\\n' '{\"restore_protocol\":1}'; exit 0"
	}
	daemon := "#!/bin/sh\nif [ \"$1\" = -restore-capabilities ]; then " + capability + "; fi\nexit 1\n"
	cli := "#!/bin/sh\nif [ \"$1\" = -restore-capabilities ]; then " + capability + "; fi\nif [ \"$1\" = install-preflight ]; then flock -n 9 || exit 3; printf 'fixture-preflight\\n'; exit 0; fi\nif [ \"$1\" = setup ]; then\n trap 'exit 143' TERM\n printf 'fixture-setup-ready\\n'\n read fixture_reply < \"$FIXTURE_RELEASE\"\n exit 0\nfi\nexit 4\n"
	for name, body := range map[string]string{"razdachad": daemon, "razdacha": cli} {
		if e = os.WriteFile(filepath.Join(payload, name), []byte(body), 0o700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(prefix, name), []byte("old-installed-sentinel"), 0o700); e != nil {
			t.Fatal(e)
		}
	}
	archive := filepath.Join(root, "synthetic.tar.gz")
	if output, e := exec.Command("tar", "-czf", archive, "-C", payload, "razdachad", "razdacha").CombinedOutput(); e != nil {
		t.Fatalf("synthetic archive: %v %s", e, output)
	}
	release := filepath.Join(root, "release.fifo")
	if e = syscall.Mkfifo(release, 0o600); e != nil {
		t.Fatal(e)
	}
	env := append(os.Environ(), "PATH="+tools+":"+os.Getenv("PATH"), "RAZDACHA_SOURCE_ONLY=1", "RAZDACHA_PREFIX="+prefix, "FIXTURE_ARCHIVE="+archive, "FIXTURE_RELEASE="+release)
	return scriptPath, state, prefix, env
}

func restoreInstallCommand(script string) string {
	return ". '" + script + "'\nARCH=amd64\ndownload() { case \"$1\" in *checksums.txt) return 1;; esac; cp \"$FIXTURE_ARCHIVE\" \"$2\"; }\ninstall_binaries\n"
}

func TestInstallerRejectsIncompatibleCandidatesForEveryRestoreEvidence(t *testing.T) {
	for _, record := range []string{restorestate.File, restorestate.Incomplete, restorestate.Runtime, restorestate.PermitFile, ".restore-fixture/recovery.json"} {
		t.Run(record, func(t *testing.T) {
			script, state, prefix, env := restoreInstallerFixture(t, false)
			target := filepath.Join(state, record)
			if e := os.MkdirAll(filepath.Dir(target), 0o700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(target, []byte("synthetic retained evidence"), 0o600); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command("sh", "-c", restoreInstallCommand(script))
			cmd.Env = env
			output, e := cmd.CombinedOutput()
			if e == nil {
				t.Fatalf("incompatible pair published for %s", record)
			}
			if !strings.Contains(string(output), "incompatible update/downgrade refused") {
				t.Fatalf("wrong rejection: %s", output)
			}
			for _, name := range []string{"razdachad", "razdacha"} {
				b, e := os.ReadFile(filepath.Join(prefix, name))
				if e != nil || string(b) != "old-installed-sentinel" {
					t.Fatal("rejection followed binary publication")
				}
			}
		})
	}
}

func TestInstallerOwnsLifecycleThroughPublicationSetupAndSignal(t *testing.T) {
	script, state, prefix, env := restoreInstallerFixture(t, true)
	cmd := exec.Command("sh", "-c", restoreInstallCommand(script)+"run_setup\n")
	cmd.Env = env
	out, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = cmd.Stdout
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	finished := false
	defer func() {
		if !finished {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	ready := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		preflight := false
		for scanner.Scan() {
			if scanner.Text() == "fixture-preflight" {
				preflight = true
			}
			if scanner.Text() == "fixture-setup-ready" {
				if !preflight {
					ready <- fmt.Errorf("setup preceded capability preflight")
				} else {
					ready <- nil
				}
				return
			}
		}
		ready <- fmt.Errorf("installer exited before setup: %w", scanner.Err())
	}()
	select {
	case e = <-ready:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("installer readiness deadline")
	}
	for _, name := range []string{"razdachad", "razdacha"} {
		b, e := os.ReadFile(filepath.Join(prefix, name))
		if e != nil || string(b) == "old-installed-sentinel" {
			t.Fatal("setup started before publication")
		}
	}
	if lease, e := restorestate.Acquire(state, -1); e == nil {
		_ = lease.Close()
		t.Fatal("restore admitted during managed setup")
	}
	if e = cmd.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case e = <-waited:
		if e == nil {
			t.Fatal("signal reported success")
		}
		finished = true
	case <-time.After(10 * time.Second):
		t.Fatal("installer failed to stop/reap setup")
	}
	lease, e := restorestate.Acquire(state, -1)
	if e != nil {
		t.Fatalf("signal left lifecycle lease held: %v", e)
	}
	_ = lease.Close()
}
