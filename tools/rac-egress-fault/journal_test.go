package main

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestPrepareRefusesRunReuse(t *testing.T) {
	j := journals{root: filepath.Join(t.TempDir(), "private")}
	p := fixturePlan()
	if err := j.prepare(p); err != nil {
		t.Fatal(err)
	}
	if j.prepare(p) == nil {
		t.Fatal("existing run reused")
	}
}

func TestJournalRejectsForeignPathsAndSymlinks(t *testing.T) {
	for _, kind := range []string{"symlink", "mode", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			j := journals{root: filepath.Join(t.TempDir(), "private")}
			p := fixturePlan()
			if err := j.prepare(p); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				if err := os.Rename(j.path(p.RunID), j.path(p.RunID)+".target"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(j.path(p.RunID)+".target", j.path(p.RunID)); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(j.path(p.RunID), 0o644); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(j.path(p.RunID), j.path(p.RunID)+".link"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := j.load(p.RunID); err == nil {
				t.Fatal("unsafe journal accepted")
			}
		})
	}
}

func TestJournalLockWaitIsBounded(t *testing.T) {
	j := journals{root: filepath.Join(t.TempDir(), "private"), lockWait: 30 * time.Millisecond}
	p := fixturePlan()
	if err := j.prepare(p); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(j.dir(p.RunID), "lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	start := time.Now()
	if j.withLock(p.RunID, func() error { return nil }) == nil {
		t.Fatal("lock unexpectedly acquired")
	}
	if time.Since(start) > time.Second {
		t.Fatal("lock wait was not bounded")
	}
}

func TestConcurrentCleanupIsIdempotentAndTombstonePersists(t *testing.T) {
	j := journals{root: filepath.Join(t.TempDir(), "private")}
	p := fixturePlan()
	if err := j.prepare(p); err != nil {
		t.Fatal(err)
	}
	fw := &memoryFirewall{present: true}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures <- j.withLock(p.RunID, func() error {
				current, e := j.load(p.RunID)
				if e != nil {
					return e
				}
				return revoke(&current, j.save, fw)
			})
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	current, err := j.load(p.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Phase != "revoked" || fw.present || fw.removes != 1 {
		t.Fatal("concurrent cleanup did not preserve revoked absence")
	}
}

func TestStalledHolderCannotInstallAfterDeadlineAndCleanupRetry(t *testing.T) {
	j := journals{root: filepath.Join(t.TempDir(), "private"), lockWait: 30 * time.Millisecond}
	p := fixturePlan()
	if err := j.prepare(p); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	fw := &memoryFirewall{}
	fw.onInspect = func() { close(entered); <-release; fw.onInspect = nil }
	expired := false
	done := make(chan error, 1)
	go func() {
		done <- j.withLock(p.RunID, func() error {
			current, e := j.load(p.RunID)
			if e != nil {
				return e
			}
			return install(&current, func() (time.Time, uint64) {
				if expired {
					return p.Deadline, p.DeadlineMonoUS
				}
				return p.PreparedAt.Add(time.Second), 2_000_000
			}, j.save, fw)
		})
	}()
	<-entered
	if j.withLock(p.RunID, func() error { return nil }) == nil {
		t.Fatal("blocked cleanup incorrectly reported success")
	}
	// The stalled pre-install source resumes only after its absolute deadline.
	expired = true
	close(release)
	if e := <-done; e == nil {
		t.Fatal("stalled expired holder installed")
	}
	if e := j.withLock(p.RunID, func() error {
		current, e := j.load(p.RunID)
		if e != nil {
			return e
		}
		return revoke(&current, j.save, fw)
	}); e != nil {
		t.Fatal(e)
	}
	if fw.installs != 0 || fw.present {
		t.Fatal("late fault survived cleanup")
	}
}
