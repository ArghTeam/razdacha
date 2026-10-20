package restorestate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lock owns a stable installation-wide flock. The inode must never be removed.
type Lock struct{ file *os.File }

// Acquire optionally validates an inherited descriptor from the managed installer.
// Re-requesting flock on the inherited open-file description is nonblocking/idempotent.
func Acquire(dir string, inherited int) (*Lock, error) {
	if err := EnsureDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, LockFile)
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	if err = privateInfo(f, 0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	if inherited >= 0 {
		n, e := unix.Dup(inherited)
		if e != nil {
			_ = f.Close()
			return nil, e
		}
		unix.CloseOnExec(n)
		handoff := os.NewFile(uintptr(n), "inherited lifecycle lock")
		a, e := f.Stat()
		if e != nil {
			_ = handoff.Close()
			_ = f.Close()
			return nil, e
		}
		b, e := handoff.Stat()
		if e != nil || !os.SameFile(a, b) || privateInfo(handoff, 0o600) != nil {
			_ = handoff.Close()
			_ = f.Close()
			return nil, errors.New("invalid inherited lifecycle lock")
		}
		// The original shell descriptor is distinct from our duplicate: CLOEXEC is
		// per descriptor, not per open-file description. Protect both in this process.
		unix.CloseOnExec(inherited)
		_ = f.Close()
		f = handoff
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("installation lifecycle is busy: %w", err)
	}
	return &Lock{file: f}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}
