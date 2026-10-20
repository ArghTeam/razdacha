package packaging

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAttestInstalledHelperPositiveAndUnsupportedControls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture-helper")
	valid := "#!/bin/sh\n[ \"$1\" = '-restore-capabilities' ] || exit 2\nprintf '%s\\n' '{\"restore_protocol\":1}'\n"
	if e := os.WriteFile(path, []byte(valid), 0o700); e != nil {
		t.Fatal(e)
	}
	initial, e := AttestRestoreBinary(context.Background(), path)
	if e != nil || len(initial) == 0 {
		t.Fatalf("positive harmless helper failed: %v", e)
	}
	if _, e = AttestRestoreBinary(context.Background(), filepath.Join(dir, "missing")); e == nil {
		t.Fatal("missing helper admitted")
	}
	old := filepath.Join(dir, "old-helper")
	if e = os.WriteFile(old, []byte("#!/bin/sh\nexit 2\n"), 0o700); e != nil {
		t.Fatal(e)
	}
	if _, e = AttestRestoreBinary(context.Background(), old); e == nil {
		t.Fatal("old helper admitted")
	}
	alias := filepath.Join(dir, "alias")
	if e = os.Symlink(path, alias); e != nil {
		t.Fatal(e)
	}
	if _, e = AttestRestoreBinary(context.Background(), alias); e == nil {
		t.Fatal("symlink helper admitted")
	}
	if e = os.WriteFile(path, []byte(valid+"# different verified content\n"), 0o700); e != nil {
		t.Fatal(e)
	}
	changed, e := AttestRestoreBinary(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	if string(initial) == string(changed) {
		t.Fatal("helper content mutation did not change attested identity")
	}
}
