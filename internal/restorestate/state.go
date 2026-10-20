// Package restorestate owns trusted installation restore state outside imported databases.
package restorestate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	Protocol   = 1
	File       = "restore-state.json"
	Incomplete = "restore-incomplete"
	Runtime    = "restore-runtime-inhibit"
	LockFile   = ".lifecycle.lock"
)

var ErrHeld = errors.New("restore holds external activity; review destination settings and explicitly resume")

// State persists even after resume/Apply so missing safety files never imply activation.
type State struct {
	Version          int    `json:"version"`
	Operation        string `json:"operation"`
	Recovery         string `json:"recovery"`
	Phase            string `json:"phase"`
	Held             bool   `json:"held"`
	RuntimeInhibited bool   `json:"runtime_inhibited"`
}

func (s State) Validate() error {
	if s.Version != Protocol || s.Operation == "" || filepath.Base(s.Operation) != s.Operation || !filepath.IsAbs(s.Recovery) {
		return errors.New("invalid restore state identity")
	}
	switch s.Phase {
	case "preparing", "recovering":
		if !s.Held || !s.RuntimeInhibited {
			return errors.New("incomplete restore must remain held and runtime inhibited")
		}
	case "complete":
		if s.Held && !s.RuntimeInhibited {
			return errors.New("held restore cannot activate runtime")
		}
	default:
		return errors.New("invalid restore phase")
	}
	return nil
}

func (s *State) Blocked() bool {
	return s != nil && (s.Phase != "complete" || s.Held || s.RuntimeInhibited)
}

// Read validates the record and required markers before callers open/migrate the DB.
func Read(dir string) (*State, error) {
	if i, e := os.Lstat(dir); e == nil {
		st, ok := i.Sys().(*syscall.Stat_t)
		if !i.IsDir() || i.Mode().Perm() != 0o700 || !ok || st.Uid != uint32(os.Geteuid()) {
			return nil, errors.New("invalid trusted restore directory")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	data, err := ReadPrivate(filepath.Join(dir, File), 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		for _, name := range []string{Incomplete, Runtime} {
			if _, e := os.Lstat(filepath.Join(dir, name)); !errors.Is(e, os.ErrNotExist) {
				return nil, errors.New("orphan restore inhibitor or inaccessible installation")
			}
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read trusted restore state: %w", err)
	}
	var s State
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return nil, fmt.Errorf("decode restore state: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing restore state data")
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	// Extra inhibitors are safe and block startup until explicit recovery repairs them.
	for _, name := range []string{Incomplete, Runtime} {
		required := name == Incomplete && s.Phase != "complete" || name == Runtime && s.RuntimeInhibited
		b, e := ReadPrivate(filepath.Join(dir, name), 64)
		if e == nil && string(b) != "1\n" {
			return nil, errors.New("invalid restore inhibitor")
		}
		if e != nil && (!errors.Is(e, os.ErrNotExist) || required) {
			return nil, fmt.Errorf("read restore inhibitor: %w", e)
		}
		if e == nil && !required {
			return nil, errors.New("unexpected restore inhibitor; explicit recovery required")
		}
	}
	return &s, nil
}

// Save writes the record only; callers order restrictive markers before live mutation.
func Save(dir string, s State) error {
	if err := s.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return WritePrivate(filepath.Join(dir, File), append(b, '\n'))
}

func ReadPrivate(path string, limit int64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	if err := privateInfo(f, 0o600); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("private file exceeds limit")
	}
	return b, nil
}

func privateInfo(f *os.File, mode os.FileMode) error {
	i, e := f.Stat()
	if e != nil {
		return e
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !i.Mode().IsRegular() || i.Mode().Perm() != mode || !ok || st.Uid != uint32(os.Geteuid()) {
		return errors.New("expected private owner-controlled regular file")
	}
	return nil
}

func EnsureDir(dir string) error {
	if e := os.MkdirAll(dir, 0o700); e != nil {
		return e
	}
	i, e := os.Lstat(dir)
	if e != nil {
		return e
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !i.IsDir() || i.Mode().Perm() != 0o700 || !ok || st.Uid != uint32(os.Geteuid()) {
		return errors.New("expected private owner-controlled installation directory")
	}
	return SyncDir(filepath.Dir(dir))
}

func SyncDir(dir string) error {
	f, e := os.Open(dir)
	if e != nil {
		return e
	}
	e = f.Sync()
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func SyncFile(path string) error {
	f, e := os.OpenFile(path, os.O_RDWR, 0)
	if e != nil {
		return e
	}
	e = f.Sync()
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}

func WritePrivate(path string, b []byte) error {
	dir := filepath.Dir(path)
	f, e := os.CreateTemp(dir, ".restore-write-*")
	if e != nil {
		return e
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if e = f.Chmod(0o600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	return SyncDir(dir)
}

func Remove(path string) error {
	if e := os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return SyncDir(filepath.Dir(path))
}
