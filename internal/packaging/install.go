package packaging

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"
)

// Пути по умолчанию — раскладка Debian/Ubuntu.
const (
	DefaultNginxDir = "/etc/nginx"
	DefaultStateDir = "/etc/razdacha"
	// DefaultConfDir — каталог настроек демона. Там же живёт сертификат панели,
	// поэтому при полном удалении он снимается целиком: приватный ключ не должен
	// пережить `uninstall -purge`.
	DefaultConfDir = "/etc/razdacha"
	DefaultTLSDir  = DefaultConfDir + "/tls"
)

// Installer настраивает nginx перед панелью.
//
// Root — корень файловой системы. В работе он пустой, то есть настоящий `/`;
// в тестах туда подставляется временный каталог, и ни один путь не уезжает в
// реальный /etc.
type Installer struct {
	Root      string
	NginxDir  string
	StateDir  string
	TLSDir    string
	SysctlDir string
	Site      SiteConfig
	Log       *slog.Logger

	// ExternalAddr — внешний адрес сервера для SAN сертификата. Пустое
	// значение в публичном режиме означает «определи сам».
	ExternalAddr string

	// DetectExternal подменяется в тестах; по умолчанию — DetectExternalAddr.
	DetectExternal func() (netip.Addr, error)

	// Now подменяется в тестах; по умолчанию — time.Now.
	Now func() time.Time
}

// NewInstaller собирает установщик с дефолтами для Debian/Ubuntu.
func NewInstaller(root string) *Installer {
	return &Installer{
		Root:           root,
		NginxDir:       DefaultNginxDir,
		StateDir:       DefaultStateDir,
		TLSDir:         DefaultTLSDir,
		SysctlDir:      DefaultSysctlDir,
		Site:           DefaultSiteConfig(),
		Log:            slog.Default(),
		DetectExternal: DetectExternalAddr,
		Now:            time.Now,
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

	// DefaultSiteDisabled — сняли ли мы штатный сайт Debian, слушавший
	// 0.0.0.0:80 мимо нашего конфига.
	DefaultSiteDisabled bool

	// SysctlWritten — записан ли drop-in с параметрами ядра.
	SysctlWritten bool

	// ExternalAddr — внешний адрес, попавший в SAN сертификата. Пусто, если
	// режим не публичный или адрес определить не удалось.
	ExternalAddr string
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

// SavedMode — что удалось узнать о режиме панели из уже лежащего конфига nginx.
//
// Ошибки здесь нет намеренно: чтение конфига ради догадки о режиме — не повод
// останавливать установку. Но и молчать нельзя, иначе повторится то, ради чего
// всё это написано: панель, тихо ушедшая из интернета (issue #81). Поэтому
// причина неудачи возвращается текстом и доезжает до вывода установщика.
type SavedMode struct {
	// Public — выведенный режим; значим только при Known.
	Public bool

	// Known — режим выведен. Ложь означает либо что конфига нет вовсе (первая
	// установка, Reason пуст), либо что его не удалось прочитать или разобрать.
	Known bool

	// Reason — почему режим вывести не удалось. Пусто, если конфига просто нет:
	// это не сбой, а установка на чистую машину.
	Reason string
}

// SavedPanelMode читает режим панели из конфига nginx, который оставила
// предыдущая установка. Путь берётся тот же, по которому конфиг пишется.
func (i *Installer) SavedPanelMode() SavedMode {
	path := i.sitePath()
	content, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return SavedMode{}
	case err != nil:
		return SavedMode{Reason: fmt.Sprintf("конфиг nginx %s не прочитан: %v", path, err)}
	}
	public, err := PublicFromConfig(string(content))
	if err != nil {
		return SavedMode{Reason: fmt.Sprintf("%s: %v", path, err)}
	}
	return SavedMode{Public: public, Known: true}
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

	ips, external, err := i.certIPs()
	if err != nil {
		return res, err
	}
	res.ExternalAddr = external

	cert, issued, err := EnsureCertificate(i.tlsDir(), ips, i.now())
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

	// Дефолтный сайт снимается до включения нашего: пока он на месте, nginx
	// слушает 0.0.0.0:80, и панель видна из интернета мимо нашего конфига.
	disabled, err := i.disableDefaultSite()
	if err != nil {
		return res, err
	}
	res.DefaultSiteDisabled = disabled

	created, err := i.enableSite()
	if err != nil {
		return res, err
	}
	res.LinkCreated = created

	sysctlWritten, err := i.writeSysctl()
	if err != nil {
		return res, err
	}
	res.SysctlWritten = sysctlWritten
	i.applySysctl()

	return res, nil
}

// certIPs собирает адреса для SAN сертификата и возвращает вторым значением
// внешний адрес, если он в SAN попал.
//
// Первым всегда идёт адрес панели внутри VPN: он же становится CommonName, и
// он же — тот адрес, который работает всегда. В публичном режиме к нему
// добавляется внешний адрес сервера: без него браузер при заходе по публичному
// IP ругается на несовпадение имени, а это предупреждение страшнее и
// непонятнее, чем «неизвестный издатель».
//
// Не определившийся внешний адрес — не повод останавливать установку: панель
// поднимется на одном адресе, а в лог уйдёт причина.
func (i *Installer) certIPs() ([]net.IP, string, error) {
	panelAddr := i.Site.ListenAddr
	if i.Site.allInterfaces() {
		// Слушаем всё, но `0.0.0.0` в сертификат не положишь.
		panelAddr = DefaultPanelAddr
	}
	panelIP := net.ParseIP(panelAddr)
	if panelIP == nil {
		return nil, "", fmt.Errorf("%w: адрес панели %q не разобран", ErrBadConfig, panelAddr)
	}
	ips := []net.IP{panelIP}

	if !i.Site.Public {
		return ips, "", nil
	}

	external, err := i.externalAddr()
	if err != nil {
		i.log().Warn("внешний адрес не определён, сертификат будет только на адрес VPN",
			"адрес", panelAddr, "причина", err)
		return ips, "", nil
	}
	if external.String() == panelAddr {
		return ips, "", nil
	}
	return append(ips, net.IP(external.AsSlice())), external.String(), nil
}

func (i *Installer) externalAddr() (netip.Addr, error) {
	if i.ExternalAddr != "" {
		addr, err := netip.ParseAddr(i.ExternalAddr)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("внешний адрес %q не разобран: %w", i.ExternalAddr, err)
		}
		if !addr.Is4() {
			return netip.Addr{}, fmt.Errorf("внешний адрес %s должен быть IPv4", addr)
		}
		return addr, nil
	}
	if i.DetectExternal == nil {
		return DetectExternalAddr()
	}
	return i.DetectExternal()
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

// Uninstall снимает наш конфиг и symlink, возвращает дефолтный сайт Debian
// (если снимали его мы) и удаляет наш drop-in с параметрами ядра. Ничего
// чужого не трогает, отсутствие файлов ошибкой не считается: удаление тоже
// идемпотентно.
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
	switch content, err := os.ReadFile(path); {
	case errors.Is(err, fs.ErrNotExist):
		// конфига нет — снимать нечего
	case err != nil:
		return fmt.Errorf("чтение конфига nginx %s: %w", path, err)
	case !isOurs(string(content)):
		i.log().Warn("конфиг nginx писали не мы, оставлен", "путь", path)
	default:
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("удаление конфига nginx %s: %w", path, err)
		}
		i.log().Info("конфиг nginx удалён", "путь", path)
	}

	if err := i.restoreDefaultSite(); err != nil {
		return err
	}
	return i.removeSysctl()
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
