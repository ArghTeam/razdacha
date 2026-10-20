package restorestate

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLifecycleExclusiveAndInherited(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	first, e := Acquire(dir, -1)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = first.Close() }()
	if second, e := Acquire(dir, -1); e == nil {
		_ = second.Close()
		t.Fatal("concurrent lifecycle owner admitted")
	}
	inherited, e := Acquire(dir, int(first.file.Fd()))
	if e != nil {
		t.Fatal(e)
	}
	if e = inherited.Close(); e != nil {
		t.Fatal(e)
	}
	if second, e := Acquire(dir, -1); e == nil {
		_ = second.Close()
		t.Fatal("handoff released parent's lock")
	}
	other, e := os.CreateTemp(dir, "wrong")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = other.Close() }()
	if wrong, e := Acquire(dir, int(other.Fd())); e == nil {
		_ = wrong.Close()
		t.Fatal("wrong lock inode admitted")
	}
	flags, e := unix.FcntlInt(first.file.Fd(), unix.F_GETFD, 0)
	if e != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("lifecycle lock can leak to unrelated exec")
	}
	if e = first.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := Acquire(dir, -1)
	if e != nil {
		t.Fatal(e)
	}
	_ = next.Close()
}

func TestTrustedStateRejectsMissingCorruptAndOrphanSafety(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	s := State{Version: Protocol, Operation: "fixture", Recovery: filepath.Join(dir, "recovery"), Phase: "complete", Held: true, RuntimeInhibited: true}
	if _, e := Read(dir); e != nil {
		t.Fatal(e)
	}
	if e := WritePrivate(filepath.Join(dir, Runtime), []byte("1\n")); e != nil {
		t.Fatal(e)
	}
	if _, e := Read(dir); e == nil {
		t.Fatal("orphan runtime inhibitor accepted")
	}
	if e := Save(dir, s); e != nil {
		t.Fatal(e)
	}
	got, e := Read(dir)
	if e != nil || !got.Blocked() {
		t.Fatalf("valid held record rejected: %v", e)
	}
	if e := os.Remove(filepath.Join(dir, Runtime)); e != nil {
		t.Fatal(e)
	}
	if _, e = Read(dir); e == nil {
		t.Fatal("missing required runtime inhibitor interpreted as activation")
	}
	if e := WritePrivate(filepath.Join(dir, Runtime), []byte("1\n")); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(filepath.Join(dir, File), 0o644); e != nil {
		t.Fatal(e)
	}
	if _, e = Read(dir); e == nil {
		t.Fatal("public trusted state accepted")
	}
	if e := os.Remove(filepath.Join(dir, File)); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(dir, Runtime), filepath.Join(dir, File)); e != nil {
		t.Fatal(e)
	}
	if _, e = Read(dir); e == nil {
		t.Fatal("symlink trusted state accepted")
	}
}

func TestTrustedStateRejectsIncompleteUnholdAndUnknownFields(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	s := State{Version: Protocol, Operation: "fixture", Recovery: dir, Phase: "preparing", Held: false, RuntimeInhibited: true}
	if e := Save(dir, s); e == nil {
		t.Fatal("incomplete unhold accepted")
	}
	if e := WritePrivate(filepath.Join(dir, File), []byte(`{"version":1,"operation":"fixture","recovery":"/private/fixture","phase":"complete","held":false,"runtime_inhibited":false,"unexpected":true}`)); e != nil {
		t.Fatal(e)
	}
	if _, e := Read(dir); e == nil {
		t.Fatal("unknown trusted state fields accepted")
	}
}

func TestTrustedStateRequiresEverySafetyFieldExactlyOnce(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"operation":"fixture","recovery":"/fixture","phase":"complete","runtime_inhibited":true}`,
		`{"version":1,"operation":"fixture","recovery":"/fixture","phase":"complete","held":true,"held":false,"runtime_inhibited":true}`,
		`{"version":1,"operation":"fixture","recovery":"/fixture","phase":"complete","held":null,"runtime_inhibited":true}`,
		`{"version":1,"operation":"fixture","recovery":"/fixture","phase":"complete","held":true,"runtime_inhibited":true} {}`,
	} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			if e := os.Chmod(dir, 0o700); e != nil {
				t.Fatal(e)
			}
			if e := WritePrivate(filepath.Join(dir, Runtime), []byte("1\n")); e != nil {
				t.Fatal(e)
			}
			if e := WritePrivate(filepath.Join(dir, File), []byte(raw)); e != nil {
				t.Fatal(e)
			}
			if _, e := Read(dir); e == nil {
				t.Fatal("malformed safety state implied resume")
			}
		})
	}
}

func TestInheritedShellDescriptorCannotLeakToExec(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	owner, e := Acquire(dir, -1)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = owner.Close() }()
	shellFD, e := unix.Dup(int(owner.file.Fd()))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = unix.Close(shellFD) }()
	// Dup deliberately clears CLOEXEC, like a shell's inherited descriptor.
	flags, e := unix.FcntlInt(uintptr(shellFD), unix.F_GETFD, 0)
	if e != nil || flags&unix.FD_CLOEXEC != 0 {
		t.Fatal("fixture did not create a non-CLOEXEC shell descriptor")
	}
	inherited, e := Acquire(dir, shellFD)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = inherited.Close() }()
	for _, fd := range []int{shellFD, int(inherited.file.Fd())} {
		flags, e = unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if e != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatal("validated inherited descriptor leaks to unrelated exec")
		}
	}
}
