package packaging

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestInstaller подменяет корень файловой системы временным каталогом:
// пути внутри установщика остаются продуктовыми (/etc/nginx, /etc/razdacha),
// но реального /etc тесты не касаются.
func newTestInstaller(t *testing.T) *Installer {
	t.Helper()
	root := t.TempDir()
	for _, sub := range []string{"sites-available", "sites-enabled"} {
		if err := os.MkdirAll(filepath.Join(root, DefaultNginxDir, sub), 0o755); err != nil {
			t.Fatalf("подготовка каталога nginx: %v", err)
		}
	}
	i := NewInstaller(root)
	i.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	i.Now = func() time.Time { return time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC) }
	// Настоящее определение внешнего адреса лезет в маршрутизацию машины, на
	// которой идут тесты; ответ там зависит от сети раннера.
	i.DetectExternal = func() (netip.Addr, error) {
		return netip.Addr{}, errors.New("в тестах внешний адрес не определяется")
	}
	return i
}

// publicInstaller — установщик в публичном режиме с известным внешним адресом.
func publicInstaller(t *testing.T, external string) *Installer {
	t.Helper()
	i := newTestInstaller(t)
	i.Site = PublicSiteConfig()
	i.Site.MaxBodyMB = DefaultSiteConfig().MaxBodyMB
	i.DetectExternal = func() (netip.Addr, error) { return netip.MustParseAddr(external), nil }
	return i
}

// Заход по публичному адресу не должен упираться в предупреждение о
// несовпадении имени: оно страшнее и непонятнее, чем неизвестный издатель.
func TestInstallPublicCertificateCoversBothAddresses(t *testing.T) {
	const external = "203.0.113.10"
	i := publicInstaller(t, external)

	res, err := i.Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.ExternalAddr != external {
		t.Errorf("внешний адрес в результате %q, ожидался %q", res.ExternalAddr, external)
	}

	cert := readCert(t, res.Cert.CertFile)
	for _, want := range []string{DefaultPanelAddr, external} {
		if !containsIP(cert.IPAddresses, net.ParseIP(want)) {
			t.Errorf("SAN не содержит %s: %v", want, cert.IPAddresses)
		}
	}
	if cert.Subject.CommonName != DefaultPanelAddr {
		t.Errorf("CommonName %q, ожидался адрес VPN", cert.Subject.CommonName)
	}
}

// Не определился внешний адрес — установка продолжается на одном адресе, а не
// падает: панель изнутри VPN важнее, чем идеальный SAN.
func TestInstallPublicWithoutExternalAddr(t *testing.T) {
	i := newTestInstaller(t)
	i.Site = PublicSiteConfig()

	res, err := i.Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.ExternalAddr != "" {
		t.Errorf("внешний адрес %q взялся из ниоткуда", res.ExternalAddr)
	}
	if !res.CertIssued {
		t.Fatal("сертификат не выпущен")
	}

	cert := readCert(t, res.Cert.CertFile)
	if len(cert.IPAddresses) != 1 || !containsIP(cert.IPAddresses, net.ParseIP(DefaultPanelAddr)) {
		t.Errorf("в SAN ожидался только адрес VPN, получено: %v", cert.IPAddresses)
	}

	content, err := os.ReadFile(res.SitePath)
	if err != nil {
		t.Fatalf("чтение конфига: %v", err)
	}
	if !strings.Contains(string(content), "listen 443 ssl;") {
		t.Error("конфиг публичного режима не слушает все интерфейсы")
	}
}

// Переход в публичный режим на настроенной машине: у лежащего сертификата в
// SAN внешнего адреса нет и сам он там не появится — значит перевыпуск. А уже
// полный SAN перевыпуска не вызывает, иначе исключение в браузере слетало бы
// на каждой установке.
func TestInstallReissuesCertificateForNewAddr(t *testing.T) {
	const external = "203.0.113.10"

	i := newTestInstaller(t)
	first, err := i.Install()
	if err != nil {
		t.Fatalf("первая установка: %v", err)
	}
	if !first.CertIssued {
		t.Fatal("сертификат не выпущен на первой установке")
	}

	i.Site = PublicSiteConfig()
	i.DetectExternal = func() (netip.Addr, error) { return netip.MustParseAddr(external), nil }

	second, err := i.Install()
	if err != nil {
		t.Fatalf("установка в публичном режиме: %v", err)
	}
	if !second.CertIssued {
		t.Fatal("сертификат с неполным SAN не перевыпущен: браузер ругался бы на имя")
	}
	cert := readCert(t, second.Cert.CertFile)
	if !containsIP(cert.IPAddresses, net.ParseIP(external)) {
		t.Fatalf("в SAN нет внешнего адреса: %v", cert.IPAddresses)
	}

	third, err := i.Install()
	if err != nil {
		t.Fatalf("повторная установка: %v", err)
	}
	if third.CertIssued {
		t.Error("сертификат с полным SAN перевыпущен без причины")
	}
	if third.ConfigWritten {
		t.Error("конфиг переписан без изменений")
	}
}

// Удаление возвращает состояние независимо от режима.
func TestUninstallAfterPublicInstall(t *testing.T) {
	i := publicInstaller(t, "203.0.113.10")
	res, err := i.Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := i.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	for _, p := range []string{res.LinkPath, res.SitePath} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s остался после удаления", p)
		}
	}
}

func TestInstallWritesConfigAndLink(t *testing.T) {
	i := newTestInstaller(t)

	res, err := i.Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.ConfigWritten || !res.LinkCreated || !res.CertIssued {
		t.Fatalf("первая установка ничего не сделала: %+v", res)
	}

	content, err := os.ReadFile(res.SitePath)
	if err != nil {
		t.Fatalf("чтение конфига: %v", err)
	}
	if !isOurs(string(content)) {
		t.Error("в конфиге нет маркера")
	}

	dst, err := os.Readlink(res.LinkPath)
	if err != nil {
		t.Fatalf("symlink не создан: %v", err)
	}
	if dst != res.SitePath {
		t.Errorf("symlink ведёт на %s, ожидался %s", dst, res.SitePath)
	}
	if _, err := os.Stat(res.Cert.CertFile); err != nil {
		t.Errorf("сертификат не создан: %v", err)
	}
}

// Повторная установка не должна ничего менять: ни переписывать конфиг, ни
// плодить второй symlink, ни перевыпускать сертификат.
func TestInstallIdempotent(t *testing.T) {
	i := newTestInstaller(t)

	first, err := i.Install()
	if err != nil {
		t.Fatalf("первая установка: %v", err)
	}
	before, err := os.ReadFile(first.SitePath)
	if err != nil {
		t.Fatalf("чтение конфига: %v", err)
	}

	second, err := i.Install()
	if err != nil {
		t.Fatalf("вторая установка: %v", err)
	}
	if second.ConfigWritten || second.LinkCreated || second.CertIssued {
		t.Fatalf("повторная установка изменила систему: %+v", second)
	}

	after, err := os.ReadFile(first.SitePath)
	if err != nil {
		t.Fatalf("чтение конфига: %v", err)
	}
	if string(before) != string(after) {
		t.Error("конфиг переписан на повторной установке")
	}

	enabled, err := os.ReadDir(filepath.Join(i.Root, DefaultNginxDir, "sites-enabled"))
	if err != nil {
		t.Fatalf("чтение sites-enabled: %v", err)
	}
	if len(enabled) != 1 {
		t.Errorf("в sites-enabled %d записей, ожидалась одна", len(enabled))
	}
}

// Изменившиеся настройки должны доехать до файла — иначе обновление молча
// оставляло бы старый конфиг.
func TestInstallRewritesOnChange(t *testing.T) {
	i := newTestInstaller(t)
	if _, err := i.Install(); err != nil {
		t.Fatalf("первая установка: %v", err)
	}

	i.Site.MaxBodyMB = 16
	res, err := i.Install()
	if err != nil {
		t.Fatalf("вторая установка: %v", err)
	}
	if !res.ConfigWritten {
		t.Fatal("конфиг не переписан после смены настроек")
	}
	if res.CertIssued {
		t.Error("сертификат перевыпущен без причины")
	}
}

func TestInstallKeepsForeignConfig(t *testing.T) {
	i := newTestInstaller(t)
	path := filepath.Join(i.Root, DefaultNginxDir, "sites-available", SiteName)
	foreign := "server { listen 80; }\n"
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatalf("подготовка чужого конфига: %v", err)
	}

	if _, err := i.Install(); !errors.Is(err, ErrForeignConfig) {
		t.Fatalf("ожидалась ErrForeignConfig, получено: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение конфига: %v", err)
	}
	if string(content) != foreign {
		t.Error("чужой конфиг перезаписан")
	}
}

func TestInstallKeepsForeignLink(t *testing.T) {
	i := newTestInstaller(t)
	other := filepath.Join(i.Root, DefaultNginxDir, "sites-available", "default")
	if err := os.WriteFile(other, []byte("server { listen 8081; }\n"), 0o644); err != nil {
		t.Fatalf("подготовка чужого сайта: %v", err)
	}
	link := filepath.Join(i.Root, DefaultNginxDir, "sites-enabled", SiteName)
	if err := os.Symlink(other, link); err != nil {
		t.Fatalf("подготовка чужого symlink: %v", err)
	}

	if _, err := i.Install(); !errors.Is(err, ErrForeignConfig) {
		t.Fatalf("ожидалась ErrForeignConfig, получено: %v", err)
	}
	if dst, _ := os.Readlink(link); dst != other {
		t.Errorf("чужой symlink переставлен на %s", dst)
	}
}

func TestInstallWithoutNginx(t *testing.T) {
	i := NewInstaller(t.TempDir())
	i.Log = slog.New(slog.NewTextHandler(io.Discard, nil))

	_, err := i.Install()
	if !errors.Is(err, ErrNginxNotInstalled) {
		t.Fatalf("ожидалась ErrNginxNotInstalled, получено: %v", err)
	}
	if !strings.Contains(err.Error(), "apt install nginx") {
		t.Errorf("в ошибке нет подсказки про установку пакета: %v", err)
	}
}

func TestUninstallRemovesOnlyOurs(t *testing.T) {
	i := newTestInstaller(t)
	res, err := i.Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := i.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Lstat(res.LinkPath); !os.IsNotExist(err) {
		t.Error("symlink остался после удаления")
	}
	if _, err := os.Lstat(res.SitePath); !os.IsNotExist(err) {
		t.Error("конфиг остался после удаления")
	}
	// Сертификат живёт в /etc/razdacha и уходит вместе с ним, а не здесь.
	if _, err := os.Stat(res.Cert.CertFile); err != nil {
		t.Errorf("сертификат удалён вместе с конфигом nginx: %v", err)
	}

	// Повторное удаление тоже не должно падать.
	if err := i.Uninstall(); err != nil {
		t.Fatalf("повторный Uninstall: %v", err)
	}
}

func TestUninstallKeepsForeignFiles(t *testing.T) {
	i := newTestInstaller(t)
	path := filepath.Join(i.Root, DefaultNginxDir, "sites-available", SiteName)
	foreign := "server { listen 80; }\n"
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatalf("подготовка чужого конфига: %v", err)
	}

	if err := i.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чужой конфиг удалён: %v", err)
	}
	if string(content) != foreign {
		t.Error("чужой конфиг изменён")
	}
}

// Установка не должна писать за пределы подменённого корня — иначе тесты
// молча трогали бы настоящий /etc.
func TestInstallStaysUnderRoot(t *testing.T) {
	i := newTestInstaller(t)
	res, err := i.Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, p := range []string{res.SitePath, res.LinkPath, res.Cert.CertFile, res.Cert.KeyFile} {
		if !strings.HasPrefix(p, i.Root) {
			t.Errorf("путь %s вне подменённого корня %s", p, i.Root)
		}
	}
}
