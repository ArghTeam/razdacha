package singbox

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ArghTeam/razdacha/internal/store"
)

// Пути и имена по умолчанию — раскладка Debian/Ubuntu.
const (
	// DefaultConfigPath — единственный артефакт, который слой пишет на диск.
	DefaultConfigPath = "/etc/sing-box/config.json"

	// DefaultService — имя штатного юнита sing-box.
	DefaultService = "sing-box"

	// DefaultBinary — имя бинарника рантайма.
	DefaultBinary = "sing-box"

	// configMode — в конфиге лежат приватные ключи туннелей, поэтому он
	// читается только владельцем. sing-box работает от root своим юнитом.
	configMode fs.FileMode = 0o600

	// configDirMode — каталог /etc/sing-box создаётся, если его нет: без
	// пакета рантайма его никто не создаёт, а первичный конфиг пишется до
	// первого запуска сервиса.
	configDirMode fs.FileMode = 0o755
)

var (
	// ErrGenerateFailed — конфиг не собрался из состояния БД. Причина в самом
	// состоянии (например, правило ссылается на исчезнувший туннель), поэтому
	// вызывающему это ошибка ввода, а не сбой окружения.
	ErrGenerateFailed = errors.New("конфиг sing-box не собран из состояния")

	// ErrCheckFailed — конфиг не прошёл `sing-box check`. Прежний конфиг
	// остаётся в силе, текст ошибки рантайма доходит до UI как есть
	// (docs/02-data-model.md, ADR по генерации конфига целиком).
	ErrCheckFailed = errors.New("конфиг sing-box не прошёл проверку")

	// ErrReloadFailed — конфиг записан и валиден, но сервис его не перечитал.
	ErrReloadFailed = errors.New("не удалось перезагрузить sing-box")
)

// Checker проверяет конфиг по пути на диске. Отдельный интерфейс нужен ровно
// для того, чтобы тесты шли без установленного рантайма.
type Checker interface {
	Check(ctx context.Context, path string) error
}

// Reloader заставляет сервис перечитать конфиг. Тоже интерфейс: в тестах ни
// systemd, ни root нет.
type Reloader interface {
	Reload(ctx context.Context) error
}

// Applier применяет состояние БД к работающему sing-box: генерирует конфиг
// целиком, проверяет его рантаймом, атомарно кладёт на диск и перезагружает
// сервис.
//
// Частичного патча конфига нет и не будет: правило, DNS-правило того же набора
// и endpoint туннеля меняются вместе, и точечная правка JSON этого не
// гарантирует.
type Applier struct {
	// ConfigPath — путь конфига; пустой означает [DefaultConfigPath].
	ConfigPath string

	// Checker и Reloader пустыми означают вызов настоящего рантайма и systemd.
	Checker  Checker
	Reloader Reloader

	Log *slog.Logger
}

// NewApplier собирает применятель с дефолтами для Debian/Ubuntu.
func NewApplier() *Applier {
	return &Applier{
		ConfigPath: DefaultConfigPath,
		Checker:    BinaryChecker{},
		Reloader:   SystemctlReloader{},
		Log:        slog.Default(),
	}
}

// ApplyResult — что применение сделало на самом деле.
type ApplyResult struct {
	// Path — куда лёг (или уже лежал) конфиг.
	Path string
	// Changed — отличался ли новый конфиг от лежащего на диске.
	Changed bool
	// Reloaded — перезагружали ли сервис. Без изменений — не перезагружали.
	Reloaded bool
}

func (a *Applier) path() string {
	if a.ConfigPath == "" {
		return DefaultConfigPath
	}
	return a.ConfigPath
}

func (a *Applier) log() *slog.Logger {
	if a.Log == nil {
		return slog.Default()
	}
	return a.Log
}

func (a *Applier) checker() Checker {
	if a.Checker == nil {
		return BinaryChecker{}
	}
	return a.Checker
}

func (a *Applier) reloader() Reloader {
	if a.Reloader == nil {
		return SystemctlReloader{}
	}
	return a.Reloader
}

// Apply собирает конфиг из снимка состояния и применяет его.
//
// Порядок шагов задан требованием «битый конфиг не затирает рабочий»:
//
//  1. генерация и сериализация — в памяти;
//  2. сравнение с лежащим на диске: совпало — выходим, сервис не трогаем.
//     Podkop сравнивает md5 (podkop/files/usr/bin/podkop:1256), здесь сравнение
//     идёт по самим байтам: файл читается всё равно, а хеш только добавил бы
//     шанс на совпадение при разном содержимом;
//  3. запись во временный файл рядом с целевым и `sing-box check` по нему:
//     проверяется ровно тот файл, который станет конфигом;
//  4. rename поверх старого — атомарная замена в пределах каталога;
//  5. reload сервиса.
//
// Отказ на шаге 3 оставляет рабочий конфиг нетронутым, а временный файл
// удаляется.
func (a *Applier) Apply(ctx context.Context, snap store.Snapshot) (ApplyResult, error) {
	res := ApplyResult{Path: a.path()}

	opts, err := Generate(snap)
	if err != nil {
		return res, fmt.Errorf("%w: %w", ErrGenerateFailed, err)
	}
	data, err := Marshal(opts)
	if err != nil {
		return res, fmt.Errorf("%w: %w", ErrGenerateFailed, err)
	}

	same, err := sameOnDisk(res.Path, data)
	if err != nil {
		return res, err
	}
	if same {
		a.log().Debug("конфиг sing-box не изменился", "путь", res.Path)
		return res, nil
	}
	res.Changed = true

	dir := filepath.Dir(res.Path)
	if err := os.MkdirAll(dir, configDirMode); err != nil {
		return res, fmt.Errorf("создание каталога %s: %w", dir, err)
	}

	tmp, err := writeTemp(res.Path, data)
	if err != nil {
		return res, err
	}
	// Временный файл удаляется в любом исходе: после успешного rename его уже
	// нет, и os.Remove просто вернёт «не найден».
	defer func() { _ = os.Remove(tmp) }()

	if err := a.checker().Check(ctx, tmp); err != nil {
		a.log().Error("конфиг sing-box отклонён рантаймом", "ошибка", err)
		return ApplyResult{Path: res.Path}, err
	}

	if err := os.Rename(tmp, res.Path); err != nil {
		return res, fmt.Errorf("запись конфига sing-box в %s: %w", res.Path, err)
	}
	a.log().Info("записан конфиг sing-box", "путь", res.Path, "байт", len(data))

	if err := a.reloader().Reload(ctx); err != nil {
		// Конфиг уже на диске и валиден: следующий старт сервиса его подхватит.
		// Поэтому это ошибка применения, а не повод откатывать файл.
		return res, err
	}
	res.Reloaded = true
	a.log().Info("sing-box перезагружен")
	return res, nil
}

// sameOnDisk отвечает, лежит ли по пути ровно тот же конфиг. Отсутствие файла —
// не ошибка: первое применение.
func sameOnDisk(path string, data []byte) (bool, error) {
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		return string(old) == string(data), nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("чтение конфига sing-box %s: %w", path, err)
	}
}

// writeTemp кладёт данные во временный файл в каталоге целевого — иначе rename
// уехал бы через границу файловых систем и перестал быть атомарным. Права
// выставляются до переименования: ключи туннелей ни мгновения не лежат
// доступными всем.
func writeTemp(dst string, data []byte) (string, error) {
	dir := filepath.Dir(dst)
	f, err := os.CreateTemp(dir, filepath.Base(dst)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("создание временного файла в %s: %w", dir, err)
	}
	name := f.Name()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", fmt.Errorf("запись во временный файл: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", fmt.Errorf("сброс временного файла на диск: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("закрытие временного файла: %w", err)
	}
	if err := os.Chmod(name, configMode); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("права на временный файл: %w", err)
	}
	return name, nil
}
