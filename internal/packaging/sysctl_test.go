package packaging

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// Без ip_nonlocal_bind nginx не стартует, пока демон не создал wg0 с адресом
// 10.8.0.1: проверено на стенде — после снятия адреса `systemctl restart nginx`
// уводит юнит в failed. То есть панель недоступна ровно после перезагрузки VPS.
func TestInstallWritesSysctl(t *testing.T) {
	i := newTestInstaller(t)

	res, err := i.Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.SysctlWritten {
		t.Fatal("drop-in с параметрами ядра не записан")
	}

	raw, err := os.ReadFile(i.sysctlPath())
	if err != nil {
		t.Fatalf("чтение drop-in: %v", err)
	}
	content := string(raw)
	for _, want := range []string{
		"net.ipv4.ip_nonlocal_bind = 1",
		"net.ipv4.ip_forward = 1",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("в drop-in нет %q", want)
		}
	}
	if !isOurs(content) {
		t.Error("в drop-in нет маркера: удаление сочтёт его чужим")
	}
	if !strings.HasSuffix(i.sysctlPath(), "/etc/sysctl.d/99-razdacha.conf") {
		t.Errorf("drop-in лежит не там: %s", i.sysctlPath())
	}
}

func TestSysctlIdempotent(t *testing.T) {
	i := newTestInstaller(t)
	if _, err := i.Install(); err != nil {
		t.Fatalf("первая установка: %v", err)
	}
	res, err := i.Install()
	if err != nil {
		t.Fatalf("вторая установка: %v", err)
	}
	if res.SysctlWritten {
		t.Fatal("drop-in переписан на повторной установке")
	}
}

func TestUninstallRemovesSysctl(t *testing.T) {
	i := newTestInstaller(t)
	if _, err := i.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := i.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(i.sysctlPath()); !os.IsNotExist(err) {
		t.Error("drop-in остался после удаления")
	}
}

func TestSysctlKeepsForeignFile(t *testing.T) {
	i := newTestInstaller(t)
	path := i.sysctlPath()
	if err := os.MkdirAll(strings.TrimSuffix(path, "/"+SysctlFileName), 0o755); err != nil {
		t.Fatalf("подготовка каталога: %v", err)
	}
	foreign := "net.ipv4.ip_forward = 0\n"
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatalf("подготовка чужого файла: %v", err)
	}

	if _, err := i.Install(); !errors.Is(err, ErrForeignConfig) {
		t.Fatalf("ожидалась ErrForeignConfig, получено: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чужой файл исчез: %v", err)
	}
	if string(raw) != foreign {
		t.Error("чужой файл перезаписан")
	}

	// Удаление его тоже не трогает.
	if err := i.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("чужой файл удалён: %v", err)
	}
}

// applySysctl пишет прямо в /proc/sys, а с подменённым корнем его нет.
// Отсутствие — не ошибка: параметры вступят в силу после перезагрузки.
func TestApplySysctlWritesProc(t *testing.T) {
	i := newTestInstaller(t)
	procDir := i.Root + "/proc/sys/net/ipv4"
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("подготовка /proc/sys: %v", err)
	}
	for _, name := range []string{"ip_forward", "ip_nonlocal_bind"} {
		if err := os.WriteFile(procDir+"/"+name, []byte("0\n"), 0o644); err != nil {
			t.Fatalf("подготовка %s: %v", name, err)
		}
	}

	i.applySysctl()

	for _, name := range []string{"ip_forward", "ip_nonlocal_bind"} {
		raw, err := os.ReadFile(procDir + "/" + name)
		if err != nil {
			t.Fatalf("чтение %s: %v", name, err)
		}
		if strings.TrimSpace(string(raw)) != "1" {
			t.Errorf("%s = %q, ожидалась 1", name, strings.TrimSpace(string(raw)))
		}
	}
}

func TestApplySysctlWithoutProc(t *testing.T) {
	i := newTestInstaller(t)
	// Паниковать или падать при отсутствии /proc/sys нельзя: так выглядит
	// прогон тестов и не-Linux.
	i.applySysctl()
}
