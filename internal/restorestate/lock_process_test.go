package restorestate

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// A real exec is required: Go-opened files already have CLOEXEC and cannot expose
// a shell-style inherited original descriptor leaking into a grandchild.
func TestLifecycleInheritedExecDoesNotLeakAndSignalReleases(t *testing.T) {
	for _, mode := range []string{"grandchild", "signal"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if e := os.Chmod(dir, 0o700); e != nil {
				t.Fatal(e)
			}
			first, e := Acquire(dir, -1)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = first.Close() }()
			cmd := exec.Command(os.Args[0], "-test.run=^TestLifecycleExecHelper$")
			cmd.Env = append(os.Environ(), "RAZDACHA_LOCK_TEST_DIR="+dir, "RAZDACHA_LOCK_TEST_MODE="+mode)
			cmd.ExtraFiles = []*os.File{first.file}
			output, e := cmd.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			input, e := cmd.StdinPipe()
			if e != nil {
				t.Fatal(e)
			}
			cmd.Stderr = os.Stderr
			if e = cmd.Start(); e != nil {
				t.Fatal(e)
			}
			done := false
			defer func() {
				if !done {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			ready := make(chan error, 1)
			go func() {
				scanner := bufio.NewScanner(output)
				for scanner.Scan() {
					if scanner.Text() == "handoff-ready" {
						ready <- nil
						return
					}
				}
				ready <- fmt.Errorf("child readiness missing: %w", scanner.Err())
			}()
			select {
			case e = <-ready:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("child handshake deadline")
			}
			if e = first.Close(); e != nil {
				t.Fatal(e)
			}
			if mode == "signal" {
				if unexpected, e := Acquire(dir, -1); e == nil {
					_ = unexpected.Close()
					t.Fatal("inherited child lost lifecycle lease")
				}
				if e = cmd.Process.Signal(os.Interrupt); e != nil {
					t.Fatal(e)
				}
				_ = cmd.Wait()
				done = true
			}
			// In grandchild mode the descendant is still alive with its stdin open.
			// No handoff descriptor may survive in that unrelated exec.
			next, e := Acquire(dir, -1)
			if e != nil {
				t.Fatalf("lease leaked after child release/signal: %v", e)
			}
			_ = next.Close()
			if mode == "grandchild" {
				_, _ = input.Write([]byte("x"))
				_ = input.Close()
				if e = cmd.Wait(); e != nil {
					t.Fatal(e)
				}
				done = true
			}
		})
	}
}

func TestLifecycleExecHelper(t *testing.T) {
	dir := os.Getenv("RAZDACHA_LOCK_TEST_DIR")
	if dir == "" {
		return
	}
	lease, e := Acquire(dir, 3)
	if e != nil {
		t.Fatal(e)
	}
	if os.Getenv("RAZDACHA_LOCK_TEST_MODE") == "signal" {
		fmt.Println("handoff-ready")
		_, _ = bufio.NewReader(os.Stdin).ReadByte()
		_ = lease.Close()
		return
	}
	grandchild := exec.Command("sh", "-c", "printf 'grandchild-ready\\n'; cat >/dev/null")
	grandchild.Stdout = os.Stdout
	grandchild.Stderr = os.Stderr
	input, e := grandchild.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = grandchild.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = grandchild.Process.Kill(); _ = grandchild.Wait() }()
	if e = lease.Close(); e != nil {
		t.Fatal(e)
	}
	if e = os.NewFile(3, "shell inherited original").Close(); e != nil {
		t.Fatal(e)
	}
	fmt.Println("handoff-ready")
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
	_ = input.Close()
	if e = grandchild.Wait(); e != nil {
		t.Fatal(e)
	}
}
