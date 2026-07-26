package packaging

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newUnitInstaller(t *testing.T) *UnitInstaller {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DefaultUnitDir), 0o755); err != nil {
		t.Fatalf("подготовка каталога юнитов: %v", err)
	}
	// Пакетные каталоги существуют, но пусты: так выглядит машина без пакета
	// sing-box.
	return &UnitInstaller{Root: root, PackagedDirs: []string{"/lib/systemd/system"}}
}

// TestDaemonUnitContent — в юните демона обязаны быть Type=notify и
// /etc/sing-box в ReadWritePaths: без первого установщик печатает «готово» до
// того, как поднят wg0, без второго применение конфига падает по правам.
func TestDaemonUnitContent(t *testing.T) {
	out, err := RenderDaemonUnit("/usr/local/bin/razdachad")
	if err != nil {
		t.Fatalf("генерация юнита: %v", err)
	}
	if !strings.HasPrefix(out, marker) {
		t.Fatal("юнит не начинается с маркера, свой файл не отличить от чужого")
	}
	for _, want := range []string{
		"Type=notify",
		"ExecStart=/usr/local/bin/razdachad\n",
		"ProtectSystem=strict",
		"ReadWritePaths=/var/lib/razdacha /etc/razdacha /etc/sing-box",
		"CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("в юните нет %q:\n%s", want, out)
		}
	}
}

// TestSingboxUnitNoReload — ExecReload в юните быть не должно: `reload-or-restart`
// при его наличии сделал бы reload, а конфиг генерируется целиком, и кэш FakeIP
// обязан обнулиться вместе с ним.
func TestSingboxUnitNoReload(t *testing.T) {
	out, err := RenderSingboxUnit("/usr/local/bin/sing-box", "/etc/sing-box/config.json")
	if err != nil {
		t.Fatalf("генерация юнита: %v", err)
	}
	if strings.Contains(out, "ExecReload") {
		t.Fatalf("в юните sing-box оказался ExecReload:\n%s", out)
	}
	if !strings.Contains(out, "ExecStart=/usr/local/bin/sing-box -D /var/lib/sing-box run -c /etc/sing-box/config.json") {
		t.Fatalf("ExecStart собран не так:\n%s", out)
	}
}

// TestUnitPathValidation — путь с пробелом разорвал бы ExecStart на аргументы.
func TestUnitPathValidation(t *testing.T) {
	for _, bad := range []string{"", "razdachad", "/usr/local/bin/razdacha d", "/usr/bin/x\ny"} {
		if _, err := RenderDaemonUnit(bad); !errors.Is(err, ErrBadConfig) {
			t.Fatalf("путь %q принят: %v", bad, err)
		}
	}
}

// TestEnsureUnitsIdempotent — повторная установка не переписывает файл.
func TestEnsureUnitsIdempotent(t *testing.T) {
	u := newUnitInstaller(t)

	written, err := u.EnsureDaemonUnit("/usr/local/bin/razdachad")
	if err != nil || !written {
		t.Fatalf("первая установка: written=%v err=%v", written, err)
	}
	written, err = u.EnsureDaemonUnit("/usr/local/bin/razdachad")
	if err != nil || written {
		t.Fatalf("повторная установка переписала юнит: written=%v err=%v", written, err)
	}

	// Смена пути к бинарнику — уже изменение, файл обязан обновиться.
	written, err = u.EnsureDaemonUnit("/usr/bin/razdachad")
	if err != nil || !written {
		t.Fatalf("смена пути не переписала юнит: written=%v err=%v", written, err)
	}
}

// TestEnsureUnitForeign — чужой юнит с тем же именем не перезаписывается.
func TestEnsureUnitForeign(t *testing.T) {
	u := newUnitInstaller(t)
	path := u.Path(DaemonUnit)
	if err := os.WriteFile(path, []byte("[Unit]\nDescription=чужой\n"), 0o644); err != nil {
		t.Fatalf("подготовка чужого юнита: %v", err)
	}
	if _, err := u.EnsureDaemonUnit("/usr/local/bin/razdachad"); !errors.Is(err, ErrForeignConfig) {
		t.Fatalf("чужой юнит перезаписан: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), "чужой") {
		t.Fatalf("чужой юнит изменён: %q %v", content, err)
	}
}

// TestEnsureSingboxUnitPackaged — пакетный юнит не подменяется: его обновляет
// пакетный менеджер, а наш файл перекрыл бы его навсегда.
func TestEnsureSingboxUnitPackaged(t *testing.T) {
	u := newUnitInstaller(t)
	packaged := filepath.Join(u.Root, "/lib/systemd/system", SingboxUnit)
	if err := os.MkdirAll(filepath.Dir(packaged), 0o755); err != nil {
		t.Fatalf("подготовка каталога: %v", err)
	}
	if err := os.WriteFile(packaged, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("подготовка пакетного юнита: %v", err)
	}

	written, err := u.EnsureSingboxUnit("/usr/bin/sing-box", "/etc/sing-box/config.json")
	if !errors.Is(err, ErrUnitPackaged) {
		t.Fatalf("ожидалась ErrUnitPackaged, получено: %v", err)
	}
	if written {
		t.Fatal("при пакетном юните файл всё равно записан")
	}
	if _, err := os.Stat(u.Path(SingboxUnit)); err == nil {
		t.Fatal("свой юнит sing-box записан поверх пакетного")
	}
}

// TestRemoveUnits — снимаются только наши файлы, отсутствие файла не ошибка.
func TestRemoveUnits(t *testing.T) {
	u := newUnitInstaller(t)
	if _, err := u.EnsureDaemonUnit("/usr/local/bin/razdachad"); err != nil {
		t.Fatalf("установка юнита демона: %v", err)
	}
	foreign := u.Path(SingboxUnit)
	if err := os.WriteFile(foreign, []byte("[Unit]\nDescription=чужой\n"), 0o644); err != nil {
		t.Fatalf("подготовка чужого юнита: %v", err)
	}

	removed, err := u.RemoveUnits()
	if err != nil {
		t.Fatalf("снятие юнитов: %v", err)
	}
	if len(removed) != 1 || removed[0] != u.Path(DaemonUnit) {
		t.Fatalf("снято не то: %v", removed)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("чужой юнит удалён: %v", err)
	}

	removed, err = u.RemoveUnits()
	if err != nil || len(removed) != 0 {
		t.Fatalf("повторное снятие: %v %v", removed, err)
	}
}
