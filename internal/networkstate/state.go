// Package networkstate stores private routing generations and explicit fresh-install seeds.
// These records never restore database rows, authentication state or WireGuard peers.
package networkstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
)

const CommittedFile = "applied-network.json"
const BootstrapFile = "bootstrap-network.json"
const CommittedKind = "committed"
const BootstrapKind = "bootstrap"

type Generation struct {
	Kind    string                `json:"kind"`
	Version int                   `json:"version"`
	Policy  store.Snapshot        `json:"policy"`
	Lists   map[string]lists.List `json:"lists"`
}

func Read(path, kind string) (Generation, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return Generation{}, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return Generation{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return Generation{}, errors.New("routing generation must be a private owner-controlled regular file")
	}
	const maxRecordBytes = 256 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxRecordBytes+1))
	if err != nil {
		return Generation{}, err
	}
	if len(data) > maxRecordBytes {
		return Generation{}, errors.New("routing generation exceeds size limit")
	}
	var gen Generation
	if err := json.Unmarshal(data, &gen); err != nil {
		return gen, fmt.Errorf("decode committed network generation: %w", err)
	}
	if gen.Version != 1 || gen.Kind != kind || gen.Lists == nil {
		return gen, errors.New("invalid committed network generation")
	}
	return gen, nil
}

func Write(path string, gen Generation) error {
	data, err := json.Marshal(gen)
	if err != nil {
		return fmt.Errorf("encode committed network generation: %w", err)
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".applied-network-*")
	if err != nil {
		return fmt.Errorf("create committed generation: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("publish committed generation: %w", err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// Invalidate retains private recovery artifacts but removes their publication authority.
// Call only with the daemon stopped and client ingress guarded on a live host.
func Invalidate(dir string) error { return invalidate(dir, os.Rename) }

func invalidate(dir string, rename func(string, string) error) error {
	return invalidateUsing(dir, rename, syncDirectory)
}

func invalidateUsing(dir string, rename func(string, string) error, syncDir func(string) error) error {
	// Bootstrap must durably lose authority before the committed record: otherwise
	// interrupted restore could fall back to an obsolete permissive first seed.
	for _, name := range []string{BootstrapFile, CommittedFile} {
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		backup := path + ".before-restore-" + time.Now().UTC().Format("20060102T150405.000000000")
		if err := rename(path, backup); err != nil {
			return fmt.Errorf("invalidate routing generation: %w", err)
		}
		if err := syncDir(dir); err != nil {
			return err
		}
	}
	return syncDir(dir)
}

// DiscardBootstrap must complete before releasing an active generation's guard.
func DiscardBootstrap(path string) error {
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return syncDirectory(filepath.Dir(path))
	} else if err != nil {
		return fmt.Errorf("discard bootstrap authority: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// CheckFreshDirectory refuses orphan authority before a new identity is created.
func CheckFreshDirectory(dir string) error {
	for _, name := range []string{CommittedFile, BootstrapFile} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return errors.New("fresh installation refused: orphan routing authority requires explicit recovery")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
