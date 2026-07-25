package packaging

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultSysctlDir и SysctlFileName — drop-in с параметрами ядра.
	// Файл один на оба параметра: они появляются и исчезают вместе с
	// razdacha, и разносить их по двум файлам значило бы удалять их в двух
	// местах.
	DefaultSysctlDir = "/etc/sysctl.d"
	SysctlFileName   = "99-razdacha.conf"
)

// sysctlSetting — параметр ядра и причина, по которой он нам нужен.
type sysctlSetting struct {
	Key     string
	Value   string
	Comment string
}

// sysctlSettings — параметры, без которых шлюз не работает.
//
// ip_nonlocal_bind добавлен по итогам проверки на стенде: nginx слушает
// 10.8.0.1, этот адрес создаёт демон вместе с wg0, а после перезагрузки nginx
// стартует раньше — и падает с «cannot assign requested address». Порядок
// юнитов эту дыру не закрывает: демон может уже стартовать, а wg0 ещё не быть.
var sysctlSettings = []sysctlSetting{
	{
		Key:     "net.ipv4.ip_forward",
		Value:   "1",
		Comment: "трафик клиентов wg0 уходит наружу через WAN-интерфейс",
	},
	{
		Key:     "net.ipv4.ip_nonlocal_bind",
		Value:   "1",
		Comment: "nginx поднимается на 10.8.0.1 до того, как демон создаст wg0",
	},
}

// renderSysctl собирает содержимое drop-in.
func renderSysctl() string {
	var b strings.Builder
	b.WriteString(marker)
	b.WriteString("\n# Решение: docs/decisions/0008-nginx-before-panel.md\n")
	for _, s := range sysctlSettings {
		fmt.Fprintf(&b, "\n# %s\n%s = %s\n", s.Comment, s.Key, s.Value)
	}
	return b.String()
}

func (i *Installer) sysctlPath() string {
	return filepath.Join(i.Root, i.SysctlDir, SysctlFileName)
}

// writeSysctl кладёт drop-in, если его ещё нет или он отличается.
// Файл без нашего маркера считается чужим и не перезаписывается.
func (i *Installer) writeSysctl() (bool, error) {
	path := i.sysctlPath()
	content := renderSysctl()

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
		return false, fmt.Errorf("чтение %s: %w", path, err)
	}

	if err := writeFile(path, []byte(content), 0o644); err != nil {
		return false, fmt.Errorf("запись параметров ядра %s: %w", path, err)
	}
	i.log().Info("записаны параметры ядра", "путь", path)
	return true, nil
}

// applySysctl проставляет параметры в работающем ядре, чтобы не ждать
// перезагрузки. Запись идёт прямо в /proc/sys — вызывать `sysctl` бинарником
// незачем, это обычный файл.
//
// Отсутствие /proc/sys ошибкой не считается: так выглядит запуск тестов с
// подменённым корнем и не-Linux. Тогда параметры применятся при следующей
// загрузке из drop-in, о чём говорит предупреждение.
func (i *Installer) applySysctl() {
	for _, s := range sysctlSettings {
		path := filepath.Join(i.Root, "/proc/sys", strings.ReplaceAll(s.Key, ".", "/"))
		if _, err := os.Stat(path); err != nil {
			i.log().Warn("параметр ядра не применён сейчас, вступит в силу после перезагрузки",
				"параметр", s.Key, "путь", path)
			continue
		}
		if err := os.WriteFile(path, []byte(s.Value+"\n"), 0o644); err != nil {
			i.log().Warn("не удалось применить параметр ядра", "параметр", s.Key, "ошибка", err)
			continue
		}
		i.log().Info("параметр ядра применён", "параметр", s.Key, "значение", s.Value)
	}
}

// removeSysctl снимает наш drop-in. Чужой файл с тем же именем остаётся.
func (i *Installer) removeSysctl() error {
	path := i.sysctlPath()
	content, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("чтение %s: %w", path, err)
	}
	if !isOurs(string(content)) {
		i.log().Warn("параметры ядра писали не мы, файл оставлен", "путь", path)
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("удаление %s: %w", path, err)
	}
	i.log().Info("параметры ядра удалены", "путь", path)
	return nil
}
