package packaging

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Раскладка юнитов systemd.
const (
	// DefaultUnitDir — каталог локальных юнитов. Именно `/etc/systemd/system`,
	// а не `/lib/systemd/system`: последний принадлежит пакетам дистрибутива.
	DefaultUnitDir = "/etc/systemd/system"

	// DaemonUnit — имя юнита демона.
	DaemonUnit = "razdachad.service"

	// SingboxUnit — имя юнита рантайма. Совпадает со штатным именем пакета
	// sing-box намеренно: если пакет в системе есть, его юнит и используется,
	// а свой мы не пишем.
	SingboxUnit = "sing-box.service"

	// SingboxWorkDir — рабочий каталог рантайма: туда он кладёт кэш наборов
	// правил типа remote.
	SingboxWorkDir = "/var/lib/sing-box"
)

// packagedUnitDirs — куда пакеты дистрибутива кладут свои юниты. Найденный там
// `sing-box.service` означает, что рантайм пришёл пакетом и своим юнитом мы его
// не подменяем.
var packagedUnitDirs = []string{"/lib/systemd/system", "/usr/lib/systemd/system"}

// daemonUnitTemplate — юнит демона (docs/08-install-upgrade.md, «Systemd-юнит»).
//
// `ReadWritePaths` включает `/etc/sing-box` не для полноты: при `ProtectSystem=strict`
// без него запись сгенерированного конфига падает по правам, и в UI это приезжает
// как «внутренняя ошибка» на кнопке «Применить». Проверено на стенде.
//
// `Type=notify` — демон сообщает о готовности сам, после того как поднят `wg0` и
// залиты правила (`cmd/razdachad/notify.go`). До этого момента `systemctl start`
// не возвращает управление, и установщик не печатает «готово» раньше времени.
var daemonUnitTemplate = template.Must(template.New("razdachad").Parse(`{{ .Marker }}
# Документ: docs/08-install-upgrade.md

[Unit]
Description=razdacha selective VPN gateway
Documentation=https://github.com/ArghTeam/razdacha
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
ExecStart={{ .Binary }}
Restart=on-failure
RestartSec=5

NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/razdacha /etc/razdacha /etc/sing-box
PrivateTmp=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_NETLINK AF_UNIX
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
`))

// singboxUnitTemplate — юнит рантайма, который пишется только при отсутствии
// пакетного.
//
// `ExecReload` намеренно нет. `systemctl reload-or-restart` при его отсутствии
// перезапускает сервис, а нам нужен именно перезапуск: конфиг генерируется
// целиком, и кэш FakeIP обязан обнулиться вместе с ним — иначе клиент с
// закэшированным адресом уходит в никуда (internal/singbox/singbox.go).
var singboxUnitTemplate = template.Must(template.New("sing-box").Parse(`{{ .Marker }}
# Конфиг генерируется razdachad из состояния БД, править его бесполезно.

[Unit]
Description=sing-box service
Documentation=https://sing-box.sagernet.org
After=network.target nss-lookup.target

[Service]
Type=simple
ExecStart={{ .Binary }} -D {{ .WorkDir }} run -c {{ .Config }}
Restart=on-failure
RestartSec=10

CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`))

// UnitInstaller кладёт юниты systemd на диск. Ни `systemctl daemon-reload`, ни
// `enable` он не делает — это шаг вызывающего, который знает, есть ли systemd.
type UnitInstaller struct {
	// Root — корень файловой системы; в тестах t.TempDir().
	Root string
	// Dir — каталог юнитов; пустой означает [DefaultUnitDir].
	Dir string
	// PackagedDirs — где искать пакетный юнит sing-box; пустой означает
	// [packagedUnitDirs].
	PackagedDirs []string
}

func (u *UnitInstaller) dir() string {
	if u.Dir == "" {
		return DefaultUnitDir
	}
	return u.Dir
}

// Path — куда ляжет юнит с таким именем.
func (u *UnitInstaller) Path(name string) string {
	return filepath.Join(u.Root, u.dir(), name)
}

// RenderDaemonUnit собирает текст юнита демона.
func RenderDaemonUnit(binary string) (string, error) {
	if err := validateUnitPath("путь к бинарнику демона", binary); err != nil {
		return "", err
	}
	var b strings.Builder
	if err := daemonUnitTemplate.Execute(&b, struct{ Marker, Binary string }{marker, binary}); err != nil {
		return "", fmt.Errorf("генерация юнита %s: %w", DaemonUnit, err)
	}
	return b.String(), nil
}

// RenderSingboxUnit собирает текст юнита рантайма.
func RenderSingboxUnit(binary, config string) (string, error) {
	if err := validateUnitPath("путь к бинарнику sing-box", binary); err != nil {
		return "", err
	}
	if err := validateUnitPath("путь к конфигу sing-box", config); err != nil {
		return "", err
	}
	var b strings.Builder
	data := struct{ Marker, Binary, Config, WorkDir string }{marker, binary, config, SingboxWorkDir}
	if err := singboxUnitTemplate.Execute(&b, data); err != nil {
		return "", fmt.Errorf("генерация юнита %s: %w", SingboxUnit, err)
	}
	return b.String(), nil
}

// validateUnitPath не пускает в юнит пути, которые ломают разбор строки
// ExecStart: пробел разделяет аргументы, перевод строки завершает директиву.
func validateUnitPath(what, path string) error {
	if path == "" {
		return fmt.Errorf("%w: не задан %s", ErrBadConfig, what)
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("%w: %s должен быть абсолютным: %q", ErrBadConfig, what, path)
	}
	if strings.ContainsAny(path, " \t\r\n\"'%$") {
		return fmt.Errorf("%w: %s содержит недопустимые символы: %q", ErrBadConfig, what, path)
	}
	return nil
}

// EnsureDaemonUnit кладёт юнит демона. Возвращает true, если файл изменился —
// значит вызывающему нужен `systemctl daemon-reload`.
func (u *UnitInstaller) EnsureDaemonUnit(binary string) (bool, error) {
	content, err := RenderDaemonUnit(binary)
	if err != nil {
		return false, err
	}
	return u.writeUnit(DaemonUnit, content)
}

// EnsureSingboxUnit кладёт юнит рантайма, если его не принёс пакет
// дистрибутива. Второе значение — писали ли мы файл.
//
// Пакетный юнит не подменяется сознательно: его обновляет пакетный менеджер, и
// наш файл в `/etc/systemd/system` перекрыл бы его навсегда, включая
// исправления безопасности.
func (u *UnitInstaller) EnsureSingboxUnit(binary, config string) (bool, error) {
	if path, ok := u.packagedSingbox(); ok {
		return false, fmt.Errorf("%w: юнит sing-box принёс пакет (%s)", ErrUnitPackaged, path)
	}
	content, err := RenderSingboxUnit(binary, config)
	if err != nil {
		return false, err
	}
	return u.writeUnit(SingboxUnit, content)
}

// ErrUnitPackaged — свой юнит писать не нужно, его принёс пакет дистрибутива.
// Это не сбой: вызывающий проверяет ошибку через errors.Is и идёт дальше.
var ErrUnitPackaged = errors.New("юнит уже есть в системе")

func (u *UnitInstaller) packagedSingbox() (string, bool) {
	dirs := u.PackagedDirs
	if dirs == nil {
		dirs = packagedUnitDirs
	}
	for _, dir := range dirs {
		path := filepath.Join(u.Root, dir, SingboxUnit)
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

// writeUnit пишет юнит, если он отличается от лежащего. Файл без маркера
// считается чужим: пользователь мог написать свой юнит с тем же именем.
func (u *UnitInstaller) writeUnit(name, content string) (bool, error) {
	path := u.Path(name)
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		if !isOurs(string(old)) {
			return false, fmt.Errorf("%w: %s писали не мы, установка остановлена", ErrForeignConfig, path)
		}
		if string(old) == content {
			return false, nil
		}
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, fmt.Errorf("создание каталога %s: %w", filepath.Dir(path), err)
		}
	default:
		return false, fmt.Errorf("чтение юнита %s: %w", path, err)
	}

	if err := writeFile(path, []byte(content), 0o644); err != nil {
		return false, fmt.Errorf("запись юнита %s: %w", path, err)
	}
	return true, nil
}

// RemoveUnits снимает наши юниты. Чужой файл с тем же именем остаётся на месте,
// отсутствие файла ошибкой не считается: удаление идемпотентно.
//
// Первым значением возвращаются снятые файлы — вызывающему они нужны для
// сообщения пользователю и для решения, звать ли `daemon-reload`.
func (u *UnitInstaller) RemoveUnits() ([]string, error) {
	var removed []string
	for _, name := range []string{DaemonUnit, SingboxUnit} {
		path := u.Path(name)
		content, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return removed, fmt.Errorf("чтение юнита %s: %w", path, err)
		case !isOurs(string(content)):
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, fmt.Errorf("удаление юнита %s: %w", path, err)
		}
		removed = append(removed, path)
	}
	return removed, nil
}
