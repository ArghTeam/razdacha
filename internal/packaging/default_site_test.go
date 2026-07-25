package packaging

import (
	"os"
	"path/filepath"
	"testing"
)

// enableDebianDefault воспроизводит то, что делает `apt install nginx`:
// файл sites-available/default с `listen 80 default_server` и symlink на него.
func enableDebianDefault(t *testing.T, i *Installer) (link, target string) {
	t.Helper()
	target = filepath.Join(i.Root, i.NginxDir, "sites-available", DefaultSiteName)
	body := "server {\n\tlisten 80 default_server;\n\tlisten [::]:80 default_server;\n}\n"
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatalf("подготовка дефолтного сайта: %v", err)
	}
	link = filepath.Join(i.Root, i.NginxDir, "sites-enabled", DefaultSiteName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("включение дефолтного сайта: %v", err)
	}
	return link, target
}

// Пока дефолтный сайт включён, nginx слушает 0.0.0.0:80 мимо нашего конфига,
// и панель видна из интернета. Проверено на стенде: curl по публичному адресу
// отдавал 200.
func TestInstallDisablesDebianDefaultSite(t *testing.T) {
	i := newTestInstaller(t)
	link, target := enableDebianDefault(t, i)

	res, err := i.Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.DefaultSiteDisabled {
		t.Fatal("дефолтный сайт не снят")
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("symlink дефолтного сайта остался в sites-enabled")
	}
	// Сам файл в sites-available трогать нельзя: он принадлежит пакету nginx.
	if _, err := os.Stat(target); err != nil {
		t.Errorf("файл дефолтного сайта удалён: %v", err)
	}
	if _, err := os.Stat(i.defaultStatePath()); err != nil {
		t.Errorf("отметка о снятии не записана: %v", err)
	}
}

func TestUninstallRestoresDefaultSite(t *testing.T) {
	i := newTestInstaller(t)
	link, target := enableDebianDefault(t, i)

	if _, err := i.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := i.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	dst, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("дефолтный сайт не возвращён: %v", err)
	}
	if dst != target {
		t.Errorf("symlink ведёт на %s, ожидался %s", dst, target)
	}
	if _, err := os.Stat(i.defaultStatePath()); !os.IsNotExist(err) {
		t.Error("отметка о снятии осталась после удаления")
	}
}

// Мы его не снимали — значит и возвращать нечего. Иначе удаление включало бы
// пользователю сайт, который он выключил сам.
func TestUninstallDoesNotCreateDefaultSite(t *testing.T) {
	i := newTestInstaller(t)

	if _, err := i.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := i.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Lstat(i.defaultLinkPath()); !os.IsNotExist(err) {
		t.Error("дефолтный сайт появился из ниоткуда")
	}
}

// Symlink с именем default, ведущий не на штатный файл Debian, — чужая
// настройка: трогать нельзя.
func TestInstallKeepsForeignDefaultSite(t *testing.T) {
	i := newTestInstaller(t)
	other := filepath.Join(i.Root, i.NginxDir, "sites-available", "my-site")
	if err := os.WriteFile(other, []byte("server { listen 10.8.0.1:8081; }\n"), 0o644); err != nil {
		t.Fatalf("подготовка чужого сайта: %v", err)
	}
	link := filepath.Join(i.Root, i.NginxDir, "sites-enabled", DefaultSiteName)
	if err := os.Symlink(other, link); err != nil {
		t.Fatalf("подготовка чужого symlink: %v", err)
	}

	res, err := i.Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.DefaultSiteDisabled {
		t.Fatal("снят чужой symlink с именем default")
	}
	dst, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("чужой symlink исчез: %v", err)
	}
	if dst != other {
		t.Errorf("чужой symlink переставлен на %s", dst)
	}
	if _, err := os.Stat(i.defaultStatePath()); !os.IsNotExist(err) {
		t.Error("записана отметка о снятии, которого не было")
	}
}

// Пакет nginx кладёт абсолютную ссылку на /etc/nginx/sites-available/default,
// но встречается и относительная — обе должны распознаваться как штатные.
func TestInstallDisablesRelativeDefaultLink(t *testing.T) {
	i := newTestInstaller(t)
	target := filepath.Join(i.Root, i.NginxDir, "sites-available", DefaultSiteName)
	if err := os.WriteFile(target, []byte("server { listen 80 default_server; }\n"), 0o644); err != nil {
		t.Fatalf("подготовка дефолтного сайта: %v", err)
	}
	link := filepath.Join(i.Root, i.NginxDir, "sites-enabled", DefaultSiteName)
	if err := os.Symlink("../sites-available/default", link); err != nil {
		t.Fatalf("подготовка относительной ссылки: %v", err)
	}

	res, err := i.Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.DefaultSiteDisabled {
		t.Fatal("относительная ссылка на дефолтный сайт не распознана")
	}
}

// Повторная установка не должна пытаться снимать уже снятое.
func TestDisableDefaultSiteIdempotent(t *testing.T) {
	i := newTestInstaller(t)
	enableDebianDefault(t, i)

	if _, err := i.Install(); err != nil {
		t.Fatalf("первая установка: %v", err)
	}
	res, err := i.Install()
	if err != nil {
		t.Fatalf("вторая установка: %v", err)
	}
	if res.DefaultSiteDisabled {
		t.Fatal("повторная установка снова «сняла» дефолтный сайт")
	}
}
