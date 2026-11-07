package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type journals struct {
	root     string
	lockWait time.Duration
}

func privateInfo(path string, dir bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("fixture path must be owned and not a symlink")
	}
	if dir {
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			return errors.New("fixture directory must have mode 0700")
		}
	} else if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || owner.Nlink != 1 {
		return errors.New("fixture file must be private, regular and singly linked")
	}
	return nil
}
func (j journals) dir(id string) string  { return filepath.Join(j.root, id) }
func (j journals) path(id string) string { return filepath.Join(j.dir(id), "state.json") }
func (j journals) prepare(p plan) error {
	if err := p.validate(); err != nil {
		return err
	}
	if err := os.Mkdir(j.root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := privateInfo(j.root, true); err != nil {
		return err
	}
	if err := os.Mkdir(j.dir(p.RunID), 0o700); err != nil {
		return fmt.Errorf("create fresh fixture run (reuse is refused): %w", err)
	}
	return j.withLock(p.RunID, func() error { return j.save(p) })
}

func (j journals) withLock(id string, fn func() error) error {
	if err := validRun(id); err != nil {
		return err
	}
	if err := privateInfo(j.root, true); err != nil {
		return err
	}
	if err := privateInfo(j.dir(id), true); err != nil {
		return err
	}
	path := filepath.Join(j.dir(id), "lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err = privateInfo(path, false); err != nil {
		return err
	}
	wait := j.lockWait
	if wait == 0 {
		wait = 3 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EINTR) && !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if !time.Now().Before(deadline) {
			return errors.New("fixture journal lock deadline exceeded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func (j journals) load(id string) (plan, error) {
	var p plan
	path := j.path(id)
	if err := privateInfo(path, false); err != nil {
		return p, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return p, err
	}
	defer func() { _ = f.Close() }()
	d := json.NewDecoder(io.LimitReader(f, 8193))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return p, err
	}
	if d.Decode(new(any)) != io.EOF {
		return p, errors.New("fixture journal contains trailing data")
	}
	if err = p.validate(); err != nil {
		return p, err
	}
	if p.RunID != id {
		return p, errors.New("fixture journal run identity mismatch")
	}
	return p, nil
}

func (j journals) save(p plan) error {
	if err := p.validate(); err != nil {
		return err
	}
	if err := privateInfo(j.dir(p.RunID), true); err != nil {
		return err
	}
	f, err := os.CreateTemp(j.dir(p.RunID), ".state-")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if err = json.NewEncoder(f).Encode(p); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), j.path(p.RunID)); err != nil {
		return err
	}
	dir, err := os.Open(j.dir(p.RunID))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
