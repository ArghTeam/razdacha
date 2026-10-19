package networkstate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestPrivateRecordRejectsUntrustedFilesAndKinds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, CommittedFile)
	gen := Generation{Version: 1, Kind: CommittedKind, Policy: store.Snapshot{Settings: store.DefaultSettings()}, Lists: map[string]lists.List{}}
	if err := Write(path, gen); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, CommittedKind); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, BootstrapKind); err == nil {
		t.Fatal("committed state accepted as bootstrap intent")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, CommittedKind); err == nil {
		t.Fatal("publicly readable secret record accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(link, CommittedKind); err == nil {
		t.Fatal("symlink accepted as routing authority")
	}
	if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, CommittedKind); err == nil {
		t.Fatal("corrupt state accepted")
	}
}

func TestRestoreInvalidatesBothKindsAndRetainsPrivateRecoveryCopies(t *testing.T) {
	dir := t.TempDir()
	for _, entry := range []struct{ name, kind string }{{CommittedFile, CommittedKind}, {BootstrapFile, BootstrapKind}} {
		if err := Write(filepath.Join(dir, entry.name), Generation{Version: 1, Kind: entry.kind, Lists: map[string]lists.List{}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Invalidate(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{CommittedFile, BootstrapFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatal("stale routing authority survived restore")
		}
		copies, err := filepath.Glob(filepath.Join(dir, name+".before-restore-*"))
		if err != nil || len(copies) != 1 {
			t.Fatalf("recovery copy: %v %v", copies, err)
		}
		info, err := os.Stat(copies[0])
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("recovery copy lost secret permissions")
		}
	}
}

func TestFreshInstallRejectsOrphanRoutingAuthority(t *testing.T) {
	for _, name := range []string{CommittedFile, BootstrapFile} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := CheckFreshDirectory(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte("orphan"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := CheckFreshDirectory(dir); err == nil {
				t.Fatal("new identity could activate under old routing authority")
			}
		})
	}
}

func TestInterruptedRestoreCannotFallbackToObsoleteBootstrap(t *testing.T) {
	dir := t.TempDir()
	for _, entry := range []struct{ name, kind string }{{CommittedFile, CommittedKind}, {BootstrapFile, BootstrapKind}} {
		if err := Write(filepath.Join(dir, entry.name), Generation{Version: 1, Kind: entry.kind, Lists: map[string]lists.List{}}); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	err := invalidate(dir, func(src, dst string) error {
		calls++
		if calls == 2 {
			return os.ErrPermission
		}
		return os.Rename(src, dst)
	})
	if err == nil {
		t.Fatal("injected interrupted invalidation succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, BootstrapFile)); !os.IsNotExist(err) {
		t.Fatal("stale bootstrap authority survived before committed invalidation")
	}
	if _, err := Read(filepath.Join(dir, CommittedFile), CommittedKind); err != nil {
		t.Fatalf("prior committed policy unavailable after interruption: %v", err)
	}
}

func TestInvalidationRetryMustConfirmDirectoryDurabilityEvenWhenFilesAreGone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, BootstrapFile)
	if err := Write(path, Generation{Version: 1, Kind: BootstrapKind, Lists: map[string]lists.List{}}); err != nil {
		t.Fatal(err)
	}
	if err := invalidateUsing(dir, os.Rename, func(string) error { return os.ErrPermission }); err == nil {
		t.Fatal("failed directory sync acknowledged invalidation")
	}
	calls := 0
	if err := invalidateUsing(dir, os.Rename, func(dir string) error { calls++; return syncDirectory(dir) }); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("retry acknowledged absent authority without a directory durability barrier")
	}
}
