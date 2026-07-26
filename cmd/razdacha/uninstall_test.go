package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/ArghTeam/razdacha/internal/packaging"
)

// notATerminal — stdin, которого нет. Обычный файл символьным устройством не
// является, поэтому [ask] считает, что спрашивать некого.
func notATerminal(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatalf("подготовка stdin: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func stateDir(t *testing.T) (root, dir string) {
	t.Helper()
	root = t.TempDir()
	dir = filepath.Join(root, defaultStateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("подготовка состояния: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.db"), []byte("ключи"), 0o600); err != nil {
		t.Fatalf("подготовка состояния: %v", err)
	}
	return root, dir
}

// TestRemoveStateWithoutTerminal — вопрос без терминала считается отвеченным
// «нет». Ключи сервера удалять оттого, что stdin оказался пустым, нельзя.
func TestRemoveStateWithoutTerminal(t *testing.T) {
	root, dir := stateDir(t)
	if err := maybeRemoveState(uninstallOptions{root: root}, notATerminal(t), slog.Default()); err != nil {
		t.Fatalf("удаление состояния: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("состояние удалено без явного согласия: %v", err)
	}
}

// TestRemoveStatePurge — с флагом каталог сносится целиком.
func TestRemoveStatePurge(t *testing.T) {
	root, dir := stateDir(t)
	opts := uninstallOptions{root: root, purge: true}
	if err := maybeRemoveState(opts, notATerminal(t), slog.Default()); err != nil {
		t.Fatalf("удаление состояния: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("каталог состояния остался при -purge")
	}
}

// TestRemoveStatePurgeTakesCertificate — на живой машине после `-purge`
// оставался /etc/razdacha/tls с приватным ключом панели: «система возвращена в
// исходное состояние» с секретом на диске — неправда.
func TestRemoveStatePurgeTakesCertificate(t *testing.T) {
	root, _ := stateDir(t)
	tls := filepath.Join(root, packaging.DefaultTLSDir)
	if err := os.MkdirAll(tls, 0o700); err != nil {
		t.Fatalf("подготовка каталога: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tls, "key.pem"), []byte("секрет"), 0o600); err != nil {
		t.Fatalf("подготовка ключа: %v", err)
	}

	opts := uninstallOptions{root: root, purge: true}
	if err := maybeRemoveState(opts, notATerminal(t), slog.Default()); err != nil {
		t.Fatalf("удаление состояния: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, packaging.DefaultConfDir)); err == nil {
		t.Fatal("каталог с сертификатом пережил -purge")
	}
}

// TestRemoveStateKeepData — с флагом каталог остаётся, вопрос не задаётся.
func TestRemoveStateKeepData(t *testing.T) {
	root, dir := stateDir(t)
	opts := uninstallOptions{root: root, keepData: true}
	if err := maybeRemoveState(opts, notATerminal(t), slog.Default()); err != nil {
		t.Fatalf("удаление состояния: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("каталог удалён при -keep-data: %v", err)
	}
}

// TestRemoveStateMissing — каталога нет, удалять нечего, ошибки быть не должно:
// удаление идемпотентно.
func TestRemoveStateMissing(t *testing.T) {
	opts := uninstallOptions{root: t.TempDir()}
	if err := maybeRemoveState(opts, notATerminal(t), slog.Default()); err != nil {
		t.Fatalf("повторное удаление: %v", err)
	}
}

// TestRemoveSingbox — рантайм снимается только по явному флагу; -purge про него
// ничего не говорит и трогать его не должен.
func TestRemoveSingbox(t *testing.T) {
	newRoot := func(t *testing.T) (string, string) {
		t.Helper()
		root := t.TempDir()
		path := filepath.Join(root, packaging.DefaultBinDir, packaging.SingboxBinary)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("подготовка каталога: %v", err)
		}
		if err := os.WriteFile(path, []byte("бинарник"), 0o755); err != nil {
			t.Fatalf("подготовка sing-box: %v", err)
		}
		return root, path
	}

	root, path := newRoot(t)
	opts := uninstallOptions{root: root, purge: true}
	if err := maybeRemoveSingbox(opts, notATerminal(t), slog.Default()); err != nil {
		t.Fatalf("удаление sing-box: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sing-box снят при -purge, хотя флага -remove-singbox не было: %v", err)
	}

	root, path = newRoot(t)
	opts = uninstallOptions{root: root, removeSingbox: true}
	if err := maybeRemoveSingbox(opts, notATerminal(t), slog.Default()); err != nil {
		t.Fatalf("удаление sing-box: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("sing-box остался при -remove-singbox")
	}
}
