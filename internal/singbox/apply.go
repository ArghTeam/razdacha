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
	// BeforeReload runs only after checked config bytes and their directory are durable.
	BeforeReload func(context.Context) error
	AfterReload  func(context.Context) error
	// ConfigPath — путь конфига; пустой означает [DefaultConfigPath].
	ConfigPath string

	// Checker и Reloader пустыми означают вызов настоящего рантайма и systemd.
	Checker  Checker
	Reloader Reloader

	// PlainLists отдаёт содержимое внешних списков, которые sing-box не читает
	// сам. Пустое означает работу без них: наборы таких списков в конфиг не
	// попадут, и правило, у которого список единственный, выпадет (issue #125).
	PlainLists PlainLists

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
	// NftSubnets is populated by the daemon's coherent network publisher.
	NftSubnets *int
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

// Prepared owns a checked temporary configuration. Close must always be called.
// Preparing never changes the published configuration or running service.
type Prepared struct {
	applier   *Applier
	result    ApplyResult
	temporary string
}

func (p *Prepared) Close() {
	if p.temporary != "" {
		_ = os.Remove(p.temporary)
	}
}

// Changed reports a disk change; a guarded recovery can still require reload.
func (p *Prepared) Changed() bool { return p.temporary != "" }

// Commit publishes checked bytes and reloads. Force reload on uncertain recovery,
// even if a previous failed attempt already wrote identical bytes to disk.
func (p *Prepared) Commit(ctx context.Context, force bool) (ApplyResult, error) {
	res := p.result
	if p.temporary != "" {
		if err := os.Rename(p.temporary, res.Path); err != nil {
			return res, fmt.Errorf("publish sing-box configuration: %w", err)
		}
		p.temporary = ""
		res.Changed = true
	}
	if res.Changed || force {
		dir, err := os.Open(filepath.Dir(res.Path))
		if err != nil {
			return res, fmt.Errorf("open configuration directory: %w", err)
		}
		err = dir.Sync()
		_ = dir.Close()
		if err != nil {
			return res, fmt.Errorf("sync configuration directory: %w", err)
		}
		if p.applier.BeforeReload != nil {
			if err := p.applier.BeforeReload(ctx); err != nil {
				return res, err
			}
		}
		if err := p.applier.reloader().Reload(ctx); err != nil {
			return res, fmt.Errorf("%w: %w", ErrReloadFailed, err)
		}
		if p.applier.AfterReload != nil {
			if err := p.applier.AfterReload(ctx); err != nil {
				return res, fmt.Errorf("%w: %w", ErrReloadFailed, err)
			}
		}
		res.Reloaded = true
	}
	return res, nil
}

func (a *Applier) Apply(ctx context.Context, snap store.Snapshot) (ApplyResult, error) {
	p, err := a.Prepare(ctx, snap)
	if err != nil {
		return ApplyResult{Path: a.path()}, err
	}
	defer p.Close()
	return p.Commit(ctx, false)
}

// Prepare generates/checks the complete candidate, including bounded ADR 0019
// pool-member retries, without replacing the published file or runtime.
func (a *Applier) Prepare(ctx context.Context, snap store.Snapshot) (*Prepared, error) {
	return a.prepare(ctx, snap, false)
}

// PrepareRecovery validates even byte-identical disk content before recovery.
func (a *Applier) PrepareRecovery(ctx context.Context, snap store.Snapshot) (*Prepared, error) {
	return a.prepare(ctx, snap, true)
}

func (a *Applier) prepare(ctx context.Context, snap store.Snapshot, forceCheck bool) (*Prepared, error) {
	res := ApplyResult{Path: a.path()}

	// Потолок выкидываний: отвергнуть можно не больше, чем есть серверов в пулах,
	// а каждый проход убирает ровно один — цикл обязан закончиться (ADR 0019).
	maxDrops := poolServerCount(snap)

	for drop := 0; ; drop++ {
		opts, err := Generate(snap, a.PlainLists)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrGenerateFailed, err)
		}
		data, err := Marshal(opts)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrGenerateFailed, err)
		}

		same, err := sameOnDisk(res.Path, data)
		if err != nil {
			return nil, err
		}
		if same && !forceCheck {
			a.log().Debug("конфиг sing-box не изменился", "путь", res.Path)
			return &Prepared{applier: a, result: res}, nil
		}

		dir := filepath.Dir(res.Path)
		if err := os.MkdirAll(dir, configDirMode); err != nil {
			return nil, fmt.Errorf("create configuration directory %s: %w", dir, err)
		}

		tmp, err := writeTemp(res.Path, data)
		if err != nil {
			return nil, err
		}

		if err := a.checker().Check(ctx, tmp); err != nil {
			// Временный файл в любом исходе не переживает итерацию: успешный
			// rename его уносит сам, отказ — убираем здесь.
			_ = os.Remove(tmp)
			// Виноват участник пула — выкидываем ровно его и пересобираем
			// конфиг (ADR 0019). Всё прочее (ручной туннель, второе звено,
			// не-pool outbound) — отказ как прежде: прежний конфиг остаётся.
			if m, ok := rejectedPoolMember(opts, snap, err); ok && drop < maxDrops {
				a.log().Warn("участник пула отвергнут рантаймом, выкинут из конфига",
					"туннель", m.tunnelName, "адрес", m.addr, "причина", rejectReason(err))
				snap = withoutPoolServer(snap, m.tunnelID, m.url)
				continue
			}
			a.log().Error("конфиг sing-box отклонён рантаймом", "ошибка", err)
			return nil, err
		}

		if same {
			_ = os.Remove(tmp)
			return &Prepared{applier: a, result: res}, nil
		}
		return &Prepared{applier: a, result: res, temporary: tmp}, nil
	}
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
