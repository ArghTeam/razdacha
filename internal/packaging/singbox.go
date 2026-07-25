package packaging

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Раскладка рантайма sing-box.
const (
	// DefaultBinDir — куда кладётся бинарник, если подходящего пакета в системе
	// нет. /usr/local/bin, а не /usr/bin: /usr/bin принадлежит пакетному
	// менеджеру, и апгрейд пакета затёр бы наш файл.
	DefaultBinDir = "/usr/local/bin"

	// SingboxBinary — имя бинарника рантайма.
	SingboxBinary = "sing-box"

	// singboxModule — путь модуля библиотеки в go.mod. Он же единственный
	// источник версии рантайма (инвариант слоя).
	singboxModule = "github.com/sagernet/sing-box"

	// defaultReleaseBase — база URL релизов. Имена ассетов идут без «v»,
	// а тег релиза — с ней, поэтому обе формы нужны одновременно.
	defaultReleaseBase = "https://github.com/SagerNet/sing-box/releases/download"

	// maxArchive — потолок на распакованный бинарник: архив приходит из сети,
	// и распаковывать его «сколько дадут» нельзя.
	maxArchive = 256 << 20

	// downloadTimeout — общий потолок на скачивание релиза.
	downloadTimeout = 5 * time.Minute

	// binaryMode — бинарник исполняемый и читаемый всем: его запускает
	// systemd, а не только мы.
	binaryMode os.FileMode = 0o755
)

var (
	// ErrSingboxVersion — требуемая версия рантайма неизвестна: бинарник
	// собран без информации о зависимостях, а явно версия не задана.
	ErrSingboxVersion = errors.New("версия sing-box не определена")

	// ErrSingboxDownload — релиз нужной версии не скачался или не распаковался.
	ErrSingboxDownload = errors.New("не удалось получить sing-box")

	// ErrSingboxArch — архитектура машины вне матрицы сборки.
	ErrSingboxArch = errors.New("для этой архитектуры релиза sing-box нет")
)

// SingboxLibraryVersion отдаёт версию библиотеки sing-box, с которой собран
// демон, без ведущей «v».
//
// Источник тот же, что у CI (`.github/workflows/ci.yml`, job singbox-check
// читает go.mod через `go mod edit -json`): здесь go.mod доступен через
// вшитую в бинарник информацию о сборке. Числом версия не дублируется нигде.
func SingboxLibraryVersion() (string, error) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", fmt.Errorf("%w: сборка без информации о зависимостях", ErrSingboxVersion)
	}
	return singboxLibraryVersion(bi)
}

func singboxLibraryVersion(bi *debug.BuildInfo) (string, error) {
	if bi == nil {
		return "", fmt.Errorf("%w: сборка без информации о зависимостях", ErrSingboxVersion)
	}
	for _, d := range bi.Deps {
		if d.Path == singboxModule {
			return strings.TrimPrefix(d.Version, "v"), nil
		}
	}
	return "", fmt.Errorf("%w: %s не найден среди зависимостей", ErrSingboxVersion, singboxModule)
}

// SingboxRuntimeVersion спрашивает версию у установленного бинарника.
//
// Разбор тот же, что в CI (`awk 'NR==1 {print $NF}'`): первая строка вывода
// `sing-box version` кончается версией.
func SingboxRuntimeVersion(ctx context.Context, binary string) (string, error) {
	out, err := exec.CommandContext(ctx, binary, "version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("запрос версии %s: %w", binary, err)
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", fmt.Errorf("%w: %s version ничего не вывел", ErrSingboxVersion, binary)
	}
	return strings.TrimPrefix(fields[len(fields)-1], "v"), nil
}

// SingboxInstaller ставит рантайм sing-box на машину.
//
// Порядок такой: сначала смотрим, что уже стоит в системе — пакет из
// репозитория подходит, если его версия совпадает с версией библиотеки из
// go.mod. Не совпала или пакета нет — кладём бинарник релиза той же версии в
// /usr/local/bin. Совпадение версий не пожелание: конфиг генерируется типами
// библиотеки, и рантайм другой версии либо не примет поле, либо поймёт его
// иначе (инвариант слоя packaging).
type SingboxInstaller struct {
	// Root — корень файловой системы; в тестах t.TempDir().
	Root   string
	BinDir string

	// Version — требуемая версия без «v». Пустая означает «взять из go.mod»
	// через [SingboxLibraryVersion].
	Version string

	// Arch — GOARCH целевой машины; пустой означает текущую.
	Arch string

	// ReleaseBase — база URL релизов; пустая означает GitHub. В тестах туда
	// подставляется httptest.
	ReleaseBase string

	HTTP *http.Client
	Log  *slog.Logger

	// LookPath ищет уже установленный рантайм; в тестах подменяется.
	LookPath func(string) (string, error)

	// RuntimeVersion спрашивает версию у бинарника; в тестах подменяется.
	RuntimeVersion func(ctx context.Context, binary string) (string, error)
}

// NewSingboxInstaller собирает установщик рантайма с дефолтами.
func NewSingboxInstaller(root string) *SingboxInstaller {
	return &SingboxInstaller{
		Root:           root,
		BinDir:         DefaultBinDir,
		ReleaseBase:    defaultReleaseBase,
		HTTP:           &http.Client{Timeout: downloadTimeout},
		Log:            slog.Default(),
		LookPath:       exec.LookPath,
		RuntimeVersion: SingboxRuntimeVersion,
	}
}

// SingboxResult — чем кончилась установка рантайма.
type SingboxResult struct {
	// Path — путь бинарника, который будет запускаться.
	Path string
	// Version — его версия.
	Version string
	// Downloaded — скачивали ли мы бинарник. Повторный запуск даёт false.
	Downloaded bool
	// FromSystem — подошёл ли уже стоявший в системе рантайм.
	FromSystem bool
}

func (i *SingboxInstaller) log() *slog.Logger {
	if i.Log == nil {
		return slog.Default()
	}
	return i.Log
}

func (i *SingboxInstaller) binPath() string {
	dir := i.BinDir
	if dir == "" {
		dir = DefaultBinDir
	}
	return filepath.Join(i.Root, dir, SingboxBinary)
}

func (i *SingboxInstaller) lookPath(name string) (string, error) {
	if i.LookPath == nil {
		return exec.LookPath(name)
	}
	return i.LookPath(name)
}

func (i *SingboxInstaller) runtimeVersion(ctx context.Context, binary string) (string, error) {
	if i.RuntimeVersion == nil {
		return SingboxRuntimeVersion(ctx, binary)
	}
	return i.RuntimeVersion(ctx, binary)
}

// wantVersion — требуемая версия рантайма.
func (i *SingboxInstaller) wantVersion() (string, error) {
	if i.Version != "" {
		return strings.TrimPrefix(i.Version, "v"), nil
	}
	return SingboxLibraryVersion()
}

// EnsureSingbox доводит систему до состояния «рантайм нужной версии на месте».
// Идемпотентна: на настроенной машине ничего не скачивает.
func (i *SingboxInstaller) EnsureSingbox(ctx context.Context) (SingboxResult, error) {
	want, err := i.wantVersion()
	if err != nil {
		return SingboxResult{}, err
	}

	// Сначала то, что уже стоит: пакет из репозитория подходящей версии — самый
	// удобный вариант, его обновляет система, а не мы.
	if path, err := i.lookPath(SingboxBinary); err == nil {
		switch have, verr := i.runtimeVersion(ctx, path); {
		case verr != nil:
			i.log().Warn("установленный sing-box не отвечает на version", "путь", path, "ошибка", verr)
		case have == want:
			i.log().Info("sing-box нужной версии уже установлен", "путь", path, "версия", have)
			return SingboxResult{Path: path, Version: have, FromSystem: true}, nil
		default:
			i.log().Warn("версия установленного sing-box не совпадает с библиотекой",
				"путь", path, "установлено", have, "нужно", want)
		}
	}

	dst := i.binPath()
	data, err := i.download(ctx, want)
	if err != nil {
		return SingboxResult{}, err
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SingboxResult{}, fmt.Errorf("создание каталога %s: %w", dir, err)
	}
	if err := writeFile(dst, data, binaryMode); err != nil {
		return SingboxResult{}, fmt.Errorf("запись %s: %w", dst, err)
	}
	i.log().Info("установлен sing-box", "путь", dst, "версия", want)
	return SingboxResult{Path: dst, Version: want, Downloaded: true}, nil
}

// assetName — имя каталога и архива релиза: `sing-box-1.12.25-linux-amd64`.
func (i *SingboxInstaller) assetName(version string) (string, error) {
	arch := i.Arch
	if arch == "" {
		arch = runtime.GOARCH
	}
	switch arch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("%w: %s", ErrSingboxArch, arch)
	}
	return fmt.Sprintf("%s-%s-linux-%s", SingboxBinary, version, arch), nil
}

// download тянет релизный tar.gz и достаёт из него один файл — бинарник.
func (i *SingboxInstaller) download(ctx context.Context, version string) ([]byte, error) {
	name, err := i.assetName(version)
	if err != nil {
		return nil, err
	}
	base := i.ReleaseBase
	if base == "" {
		base = defaultReleaseBase
	}
	url := fmt.Sprintf("%s/v%s/%s.tar.gz", strings.TrimSuffix(base, "/"), version, name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: запрос %s: %w", ErrSingboxDownload, url, err)
	}
	client := i.HTTP
	if client == nil {
		client = &http.Client{Timeout: downloadTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrSingboxDownload, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s: ответ %s", ErrSingboxDownload, url, resp.Status)
	}

	data, err := extractBinary(io.LimitReader(resp.Body, maxArchive))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrSingboxDownload, url, err)
	}
	return data, nil
}

// extractBinary достаёт из tar.gz единственный интересный файл — sing-box.
// Остальные записи (LICENSE, скрипты дополнения оболочки) пропускаются, пути с
// «..» не разбираются вовсе: имя сравнивается целиком, никуда не записывается.
func extractBinary(r io.Reader) ([]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("распаковка архива: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("в архиве нет файла %s", SingboxBinary)
		}
		if err != nil {
			return nil, fmt.Errorf("чтение архива: %w", err)
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != SingboxBinary {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxArchive))
		if err != nil {
			return nil, fmt.Errorf("чтение %s из архива: %w", h.Name, err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("файл %s в архиве пуст", h.Name)
		}
		return data, nil
	}
}
