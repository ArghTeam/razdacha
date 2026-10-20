package api

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// isolateUIFixture returns true only in the parent after its isolated child exits.
func isolateUIFixture(t *testing.T) bool {
	t.Helper()
	if os.Getenv("RAZDACHA_UI_FIXTURE_CHILD") == "1" {
		parent, err := strconv.Atoi(os.Getenv("RAZDACHA_UI_FIXTURE_PARENT_PID"))
		if err != nil || parent <= 1 || os.Getppid() != parent {
			t.Fatal("fixture parent identity lost")
		}
		// Prove both namespaces changed before the first mount mutation.
		for _, name := range []string{"net", "mnt"} {
			original := os.Getenv(map[string]string{"net": "RAZDACHA_UI_ORIGINAL_NET_NS", "mnt": "RAZDACHA_UI_ORIGINAL_MNT_NS"}[name])
			current, err := os.Readlink("/proc/self/ns/" + name)
			if err != nil || original == "" || original == current {
				t.Fatal("fresh namespace not proven before mount: " + name)
			}
		}
		if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
			t.Fatalf("private fixture propagation: %v", err)
		}
		if err := unix.Mount("tmpfs", "/var/lib", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0700,size=16m"); err != nil {
			t.Fatalf("fixture tmpfs: %v", err)
		}
		return false
	}
	originalNet, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	originalMount, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestUISecretsFixture$", "-test.v", "-test.timeout=9m")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "LANG=C", "TMPDIR=" + os.Getenv("TMPDIR"),
		"RAZDACHA_UI_SECRETS_FIXTURE=isolated", "RAZDACHA_UI_FIXTURE_ROOT=" + os.Getenv("RAZDACHA_UI_FIXTURE_ROOT"),
		"RAZDACHA_UI_FIXTURE_CHILD=1", fmt.Sprintf("RAZDACHA_UI_FIXTURE_PARENT_PID=%d", os.Getpid()),
		"RAZDACHA_UI_ORIGINAL_NET_NS=" + originalNet, "RAZDACHA_UI_ORIGINAL_MNT_NS=" + originalMount,
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWNET | unix.CLONE_NEWNS, Pdeathsig: syscall.SIGKILL}
	// Linux parent-death notification follows the spawning thread's lifetime.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := cmd.Run(); err != nil {
		t.Fatalf("isolated UI fixture child: %v", err)
	}
	return true
}
