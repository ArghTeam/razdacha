package ingressguard

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// WithLock serializes short authority/kernel transactions across processes.
// Never hold this lock across runtime restart: ExecStartPre needs the same lock.
// The persistent lock inode is retained so concurrent waiters cannot split locks.
func WithLock(manifestPath string, operation func() error) error {
	if operation == nil {
		return errors.New("public ingress transaction is required")
	}
	if err := privateParent(manifestPath); err != nil {
		return err
	}
	file, err := os.OpenFile(manifestPath+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open public ingress lock: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || owner.Uid != uint32(os.Geteuid()) {
		return errors.New("public ingress lock must be a private owner-controlled regular file")
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("acquire public ingress lock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()
	return operation()
}
