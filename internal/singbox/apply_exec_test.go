package singbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemctlReloaderStopsOldRuntimeInsteadOfAcknowledgingAsyncReload(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "runtime")
	if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A packaged ExecReload can succeed while its old runtime is still serving.
	// The production reloader must choose the operation that stops that process.
	script := "#!/bin/sh\ncase \"$1\" in\nreload-or-restart) exit 0;;\nrestart) printf candidate > \"$RUNTIME_MARKER\";;\n*) exit 1;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RUNTIME_MARKER", marker)
	if err := (SystemctlReloader{Service: "fixture.service"}).Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "candidate" {
		t.Fatal("successful asynchronous reload retained the old runtime across publication")
	}
}
