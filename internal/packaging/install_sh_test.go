package packaging

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Тесты установщика гоняют настоящий packaging/install.sh, но только его
// определения: скрипт подгружается через `.` с RAZDACHA_SOURCE_ONLY=1, и ни
// одна проверка первой фазы при этом не запускается, ничего не скачивается и
// ничего не пишется мимо t.TempDir.

// builtTagLine — строка, которую workflow релиза заменяет тегом. Если её
// переписать, релиз перестанет привязывать скрипт к версии, поэтому она
// проверяется отдельным тестом.
const builtTagLine = `BUILT_TAG=""`

func installScript(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "packaging", "install.sh"))
	if err != nil {
		t.Fatalf("путь к install.sh: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("install.sh не найден: %v", err)
	}
	return path
}

// sourceScript подгружает скрипт и выполняет после него command.
func sourceScript(t *testing.T, script, command string, env ...string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("нет sh: проверять установщик нечем")
	}
	cmd := exec.Command("sh", "-c", ". '"+script+"'\n"+command)
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"RAZDACHA_SOURCE_ONLY=1",
	}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("запуск %s: %v\n%s", command, err, out)
	}
	return strings.TrimSpace(string(out))
}

// stampedScript — копия скрипта с подставленным тегом, ровно как её делает
// workflow релиза.
func stampedScript(t *testing.T, tag string) string {
	t.Helper()
	data, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatalf("чтение install.sh: %v", err)
	}
	stamped := strings.Replace(string(data), builtTagLine, `BUILT_TAG="`+tag+`"`, 1)
	if stamped == string(data) {
		t.Fatalf("строка %s в install.sh не найдена: релиз не сможет подставить тег", builtTagLine)
	}
	path := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(path, []byte(stamped), 0o755); err != nil {
		t.Fatalf("запись копии: %v", err)
	}
	return path
}

// TestReleaseURLDefaultsToLatest — скрипт из репозитория ставит последний релиз:
// тега у него нет.
func TestReleaseURLDefaultsToLatest(t *testing.T) {
	got := sourceScript(t, installScript(t), "release_url razdacha-linux-amd64.tar.gz")
	want := "https://github.com/ArghTeam/razdacha/releases/latest/download/razdacha-linux-amd64.tar.gz"
	if got != want {
		t.Fatalf("адрес %q, ожидался %q", got, want)
	}
}

// TestReleaseURLUsesBuiltTag — скрипт, выложенный под тегом, ставит свою
// версию. Раньше install.sh из v0.1.1 ставил v0.2.0 (issue #82).
func TestReleaseURLUsesBuiltTag(t *testing.T) {
	got := sourceScript(t, stampedScript(t, "v0.1.1"), "release_url razdacha-linux-amd64.tar.gz")
	want := "https://github.com/ArghTeam/razdacha/releases/download/v0.1.1/razdacha-linux-amd64.tar.gz"
	if got != want {
		t.Fatalf("адрес %q, ожидался %q", got, want)
	}
}

// TestReleaseURLVersionOverridesBuiltTag — RAZDACHA_VERSION перебивает и
// подставленный тег, и «последний релиз».
func TestReleaseURLVersionOverridesBuiltTag(t *testing.T) {
	cases := map[string]string{
		"репозиторий": installScript(t),
		"релиз":       stampedScript(t, "v0.1.1"),
	}
	for name, script := range cases {
		got := sourceScript(t, script, "release_url checksums.txt", "RAZDACHA_VERSION=v9.9.9")
		want := "https://github.com/ArghTeam/razdacha/releases/download/v9.9.9/checksums.txt"
		if got != want {
			t.Fatalf("%s: адрес %q, ожидался %q", name, got, want)
		}
	}
}

// TestReleaseWorkflowStampsTag — workflow подставляет тег в ту самую строку.
// Тест держит вместе две половины одного механизма: разъедутся — релиз молча
// вернётся к «ставим последний».
func TestReleaseWorkflowStampsTag(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("чтение workflow: %v", err)
	}
	if !strings.Contains(string(data), `s|^BUILT_TAG=\"\"$|BUILT_TAG=\"$TAG\"|`) {
		t.Fatal("workflow релиза не подставляет тег в install.sh")
	}
	script, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatalf("чтение install.sh: %v", err)
	}
	if !strings.Contains(string(script), "\n"+builtTagLine+"\n") {
		t.Fatalf("в install.sh нет строки %s", builtTagLine)
	}
}

// TestSetupArgsPanelMode — RAZDACHA_PUBLIC имеет три состояния, а не два.
// Незаданная переменная не должна приводить к флагу вовсе: режим панели хранится
// в БД, и обновление голым однострочником обязано оставить его прежним (#81).
func TestSetupArgsPanelMode(t *testing.T) {
	script := installScript(t)
	prefix := t.TempDir()
	fake := filepath.Join(prefix, "razdacha")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatalf("подготовка бинарника: %v", err)
	}

	cases := []struct {
		name string
		env  []string
		want string
	}{
		{"не задана", nil, ""},
		{"единица", []string{"RAZDACHA_PUBLIC=1"}, "--public=true"},
		{"да словом", []string{"RAZDACHA_PUBLIC=yes"}, "--public=true"},
		{"ноль", []string{"RAZDACHA_PUBLIC=0"}, "--public=false"},
		{"false", []string{"RAZDACHA_PUBLIC=false"}, "--public=false"},
		{"пустая", []string{"RAZDACHA_PUBLIC="}, "--public=false"},
	}
	for _, c := range cases {
		env := append([]string{"RAZDACHA_PREFIX=" + prefix}, c.env...)
		out := sourceScript(t, script, "run_setup", env...)
		args := out[strings.LastIndex(out, "\n")+1:]
		want := "setup --daemon " + filepath.Join(prefix, "razdachad")
		if c.want != "" {
			want += " " + c.want
		}
		if args != want {
			t.Fatalf("%s: аргументы %q, ожидались %q", c.name, args, want)
		}
	}
}
