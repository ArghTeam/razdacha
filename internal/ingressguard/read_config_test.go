package ingressguard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadConfigUsesPrivateControlledExactBytes(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	content := []byte("{\n  \"inbounds\": []\n}\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadConfig(path)
	if err != nil || string(got) != string(content) {
		t.Fatal("controlled config read changed bytes", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfig(path); err == nil {
		t.Fatal("public config accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfig(link); err == nil {
		t.Fatal("symlink config accepted")
	}
}
