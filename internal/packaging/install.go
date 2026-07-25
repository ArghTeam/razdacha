package packaging

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Пути по умолчанию — раскладка Debian/Ubuntu.
const (
	DefaultNginxDir = "/etc/nginx"
	DefaultTLSDir   = "/etc/razdacha/tls"
)

// Installer настраивает nginx перед панелью.
//
// Root — корень файловой системы. В работе он пустой, то есть настоящий `/`;
// в тестах туда подставляется временный каталог, и ни один путь не уезжает в
// реальный /etc.
type Installer struct {
	Root     string
	NginxDir string
	TLSDir   string
	Site     SiteConfig
	Log      *slog.Logger

	// Now подменяется в тестах; по умолчанию — time.Now.
	Now func() time.Time
}

// NewInstaller собирает установщик с дефолтами для Debian/Ubuntu.
func NewInstaller(root string) *Installer {
	return &Installer{
		Root:     root,
		NginxDir: DefaultNginxDir,
		TLSDir:   DefaultTLSDir,
		Site:     DefaultSiteConfig(),
		Log:      slog.Default(),
		Now:      time.Now,
	}
}

// Result — что установка сделала на самом деле. Нужен для идемпотентности:
// повторный запуск возвращает все флаги false.
type Result struct {
	SitePath      string
	LinkPath      string
	Cert          CertPaths
	CertIssued    bool
	ConfigWritten bool
	LinkCreated   bool
}

func (i *Installer) sitePath() string {
	return filepath.Join(i.Root, i.NginxDir, "sites-available", SiteName)
}

func (i *Installer) linkPath() string {
	return filepath.Join(i.Root, i.NginxDir, "sites-enabled", SiteName)
}

func (i *Installer) tlsDir() string {
	return filepath.Join(i.Root, i.TLSDir)
}

func (i *Installer) log() *slog.Logger {
	if i.Log == nil {
		return slog.Default()
	}
	return i.Log
}

func (i *Installer) now() time.Time {
	if i.Now == nil {
		return time.Now()
	}
	return i.Now()
}

// Install выпускает сертификат при необходимости, пишет конфиг nginx и
// включает сайт. Повторный запуск на настроенной системе ничего не меняет.
//
// Перезагрузку nginx установщик не делает — это шаг install.sh, который знает
// про systemd; здесь только файлы.
func (i *Installer) Install() (Result, error) {
	res := Result{SitePath: i.sitePath(), LinkPath: i.linkPath()}

	if err := i.checkNginx(); err != nil {
		return res, err
	}

	ip := net.ParseIP(i.Site.ListenAddr)
	if ip == nil {
		return res, fmt.Errorf("%w: адрес панели %q не разобран", ErrBadConfig, i.Site.ListenAddr)
	}
	cert, issued, err := EnsureCertificate(i.tlsDir(), []net.IP{ip}, i.now())
	if err != nil {
		return res, err
	}
	res.Cert, res.CertIssued = cert, issued
	if issued {
		i.log().Info("выпущен сертификат панели", "путь", cert.CertFile)
	}

	// Пути в конфиг идут те же, что на диске: при пустом Root это настоящие
	// /etc/razdacha/tls/..., в тестах — временный каталог.
	site := i.Site
	site.CertFile, site.KeyFile = cert.CertFile, cert.KeyFile

	content, err := site.Render()
	if err != nil {
		return res, err
	}

	written, err := i.writeSite(content)
	if err != nil {
		return res, err
	}
	res.ConfigWritten = written

	created, err := i.enableSite()
	if err != nil {
		return res, err
	}
	res.LinkCreated = created
	return res, nil
}

// checkNginx отвечает на вопрос «есть ли вообще что настраивать». Без него
// установка падала бы с невнятным «no such file or directory» из os.Create.
func (i *Installer) checkNginx() error {
	for _, sub := range []string{"sites-available", "sites-enabled"} {
		dir := filepath.Join(i.Root, i.NginxDir, sub)
		st, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("%w: каталог %s не найден; установите пакет: apt install nginx",
				ErrNginxNotInstalled, dir)
		}
		if !st.IsDir() {
			return fmt.Errorf("%w: %s не каталог", ErrNginxNotInstalled, dir)
		}
	}
	return nil
}

// writeSite пишет конфиг, если его там ещё нет или он отличается. Файл без
// нашего маркера считается чужим и не перезаписывается.
func (i *Installer) writeSite(content string) (bool, error) {
	path := i.sitePath()
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		if !isOurs(string(old)) {
			return false, fmt.Errorf("%w: %s писали не мы, установка остановлена", ErrForeignConfig, path)
		}
		if string(old) == content {
			i.log().Debug("конфиг nginx не изменился", "путь", path)
			return false, nil
		}
	case errors.Is(err, fs.ErrNotExist):
		// первая установка
	default:
		return false, fmt.Errorf("чтение конфига nginx %s: %w", path, err)
	}

	if err := writeFile(path, []byte(content), 0o644); err != nil {
		return false, fmt.Errorf("запись конфига nginx %s: %w", path, err)
	}
	i.log().Info("записан конфиг nginx", "путь", path)
	return true, nil
}

// enableSite создаёт symlink в sites-enabled. Уже указывающий на наш файл
// symlink оставляется как есть, всё остальное — чужое.
func (i *Installer) enableSite() (bool, error) {
	link, target := i.linkPath(), i.sitePath()

	switch dst, err := os.Readlink(link); {
	case err == nil:
		if dst == target {
			return false, nil
		}
		return false, fmt.Errorf("%w: %s уже ведёт на %s", ErrForeignConfig, link, dst)
	case errors.Is(err, fs.ErrNotExist):
		// ссылки нет, создаём ниже
	default:
		// Не symlink, а обычный файл или каталог — трогать нельзя.
		if _, statErr := os.Lstat(link); statErr == nil {
			return false, fmt.Errorf("%w: %s не является нашим symlink", ErrForeignConfig, link)
		}
		return false, fmt.Errorf("чтение %s: %w", link, err)
	}

	if err := os.Symlink(target, link); err != nil {
		return false, fmt.Errorf("включение сайта nginx %s: %w", link, err)
	}
	i.log().Info("сайт nginx включён", "ссылка", link)
	return true, nil
}

// Uninstall снимает наш конфиг и symlink, не трогая ничего чужого.
// Отсутствие файлов ошибкой не считается: удаление тоже идемпотентно.
//
// Сертификат остаётся: он лежит в /etc/razdacha и уходит вместе с ним.
func (i *Installer) Uninstall() error {
	link := i.linkPath()
	switch dst, err := os.Readlink(link); {
	case err == nil:
		if dst != i.sitePath() {
			i.log().Warn("symlink nginx ведёт не на наш файл, оставлен", "ссылка", link, "цель", dst)
			break
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("снятие сайта nginx %s: %w", link, err)
		}
		i.log().Info("сайт nginx выключен", "ссылка", link)
	case errors.Is(err, fs.ErrNotExist):
		// уже снят
	default:
		i.log().Warn("по пути symlink nginx лежит что-то другое, оставлено", "путь", link)
	}

	path := i.sitePath()
	content, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("чтение конфига nginx %s: %w", path, err)
	}
	if !isOurs(string(content)) {
		i.log().Warn("конфиг nginx писали не мы, оставлен", "путь", path)
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("удаление конфига nginx %s: %w", path, err)
	}
	i.log().Info("конфиг nginx удалён", "путь", path)
	return nil
}

// writeFile пишет файл через временный в том же каталоге: оборванная запись
// не оставляет полуготовый конфиг на месте рабочего. Права выставляются до
// переименования, чтобы ключ ни мгновения не лежал доступным всем.
func writeFile(dst string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, filepath.Base(dst)+".tmp-*")
	if err != nil {
		return fmt.Errorf("создание временного файла в %s: %w", dir, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("запись во временный файл: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("сброс временного файла на диск: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("закрытие временного файла: %w", err)
	}
	if err := os.Chmod(name, mode); err != nil {
		return fmt.Errorf("права на временный файл: %w", err)
	}
	if err := os.Rename(name, dst); err != nil {
		return fmt.Errorf("переименование в %s: %w", dst, err)
	}
	return nil
}
