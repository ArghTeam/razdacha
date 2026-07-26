package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// Раскладка на диске (docs/01-architecture.md, «Состояние на диске»).
const (
	defaultDBPath   = "/var/lib/razdacha/state.db"
	defaultStateDir = "/var/lib/razdacha"
	defaultBinDir   = "/usr/local/bin"
	defaultDaemon   = "/usr/local/bin/razdachad"

	// backupDir — куда уезжает копия БД перед обновлением. Внутри состояния, а
	// не рядом: каталог уже 0700, и удаление razdacha уносит копии вместе с
	// оригиналом, не оставляя приватных ключей на диске.
	backupDir = "backups"
)

// firstPeerName — имя первого пира, если имя не задано флагом.
const firstPeerName = "client-1"

// passwordLength и passwordAlphabet — сгенерированный пароль панели.
//
// Алфавит без `0`, `O`, `l`, `1` и `I`: пароль печатается в терминал и
// переписывается руками, а эти символы в половине шрифтов неразличимы.
// Шестнадцать символов из 57 — примерно 93 бита, перебор через панель с
// блокировкой по адресу здесь и близко не стоит.
const (
	passwordLength   = 16
	passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// setupOptions — параметры `razdacha setup`.
type setupOptions struct {
	root     string
	dbPath   string
	daemon   string
	peerName string
	public   bool
	start    bool
	color    bool
}

// runSetup — фаза изменений и фаза вывода из docs/08-install-upgrade.md.
//
// Фаза проверок сюда не входит: она целиком в `install.sh` и выполняется до
// того, как эта команда запустится. Здесь система уже меняется, и порядок шагов
// задан зависимостями: сначала состояние и ключи, потом файлы, потом юниты, и
// только затем запуск сервисов.
func runSetup(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	opts := setupOptions{}
	flags.StringVar(&opts.root, "root", "", "корень файловой системы; пустой означает /")
	flags.StringVar(&opts.dbPath, "db", defaultDBPath, "путь к файлу состояния")
	flags.StringVar(&opts.daemon, "daemon", defaultDaemon, "путь к бинарнику демона для юнита systemd")
	flags.StringVar(&opts.peerName, "peer", firstPeerName, "имя первого пира")
	flags.BoolVar(&opts.public, "public", false,
		"публичный режим панели: nginx слушает все интерфейсы (ADR 0009)")
	flags.BoolVar(&opts.start, "start", true, "запускать сервисы через systemd")
	if err := flags.Parse(args); err != nil {
		return err
	}
	opts.color = isTerminal(os.Stdout)

	out, err := setup(ctx, opts)
	if err != nil {
		return err
	}
	text, err := out.Render()
	if err != nil {
		return err
	}
	fmt.Print(text)
	return nil
}

func setup(ctx context.Context, opts setupOptions) (summary, error) {
	log := slog.Default()

	// Признак первой установки — наличие БД (docs/08-install-upgrade.md,
	// «Идемпотентность»). Проверка делается до открытия: store.Open создаёт
	// файл сам, и после него отличить обновление от установки уже нельзя.
	dbPath := filepath.Join(opts.root, opts.dbPath)
	fresh, err := isFresh(dbPath)
	if err != nil {
		return summary{}, err
	}

	// Резервная копия снимается до открытия БД: миграции накатываются в
	// store.Open, и откатывать их нечем — восстановление это возврат файла.
	if !fresh {
		backup, err := backupDB(dbPath, version)
		if err != nil {
			return summary{}, err
		}
		log.Info("резервная копия состояния", "путь", backup)
	}

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return summary{}, err
	}
	// БД закрывается до запуска демона: писатель у файла один.
	defer func() { _ = st.Close() }()

	// Ключ сервера заводится здесь, а не при первом старте демона: клиентский
	// конфиг без публичной половины ключа не собрать, а печатать его нужно
	// раньше, чем демон успеет подняться. Повторный вызов отдаёт тот же ключ.
	serverKey, err := netstack.EnsureWGServerKey(ctx, st)
	if err != nil {
		return summary{}, err
	}

	settings, err := ensureEndpoint(ctx, st, log)
	if err != nil {
		return summary{}, err
	}

	sb, err := ensureSingbox(ctx, opts.root, log)
	if err != nil {
		return summary{}, err
	}

	inst := packaging.NewInstaller(opts.root)
	inst.Log = log
	if opts.public {
		inst.Site = packaging.PublicSiteConfig()
	}
	if _, err := inst.Install(); err != nil {
		return summary{}, err
	}

	if err := writeFirstSingboxConfig(ctx, opts.root, sb.Path, st, log); err != nil {
		return summary{}, err
	}

	if err := writeUnits(opts, sb.Path, log); err != nil {
		return summary{}, err
	}

	password, err := ensurePassword(ctx, st, log)
	if err != nil {
		return summary{}, err
	}

	peer, created, err := ensureFirstPeer(ctx, st, opts.peerName, log)
	if err != nil {
		return summary{}, err
	}

	out := summary{
		Fresh:    fresh,
		Version:  version,
		Password: password,
		PanelURL: panelURL(settings.WGServerAddress),
		WGPort:   settings.WGListenPort,
		Color:    opts.color,
	}
	if created {
		conf, err := netstack.ClientConfig(peer, settings, serverKey.PublicKey().String())
		if err != nil {
			return summary{}, err
		}
		path, err := savePeerConfig(opts.root, peer.Name, conf)
		if err != nil {
			return summary{}, err
		}
		out.PeerName, out.PeerConfig, out.ClientConfig = peer.Name, path, conf
	}

	if err := st.Close(); err != nil {
		return summary{}, err
	}
	if opts.start {
		if err := startServices(ctx, log); err != nil {
			return summary{}, err
		}
	}
	return out, nil
}

// isFresh отвечает, первая ли это установка.
func isFresh(dbPath string) (bool, error) {
	switch _, err := os.Stat(dbPath); {
	case err == nil:
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		return true, nil
	default:
		return false, fmt.Errorf("проверка состояния %s: %w", dbPath, err)
	}
}

// backupDB копирует состояние перед обновлением.
//
// Миграции накатываются при открытии БД и назад не откатываются: единственный
// способ вернуться на предыдущую версию демона — вернуть файл. Копия называется
// по версии и времени, потому что обновлений на одну версию бывает несколько
// (переустановка того же релиза), и затирать прошлую копию нечем оправдать.
func backupDB(dbPath, ver string) (string, error) {
	dir := filepath.Join(filepath.Dir(dbPath), backupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("создание каталога %s: %w", dir, err)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		return "", fmt.Errorf("чтение состояния %s: %w", dbPath, err)
	}
	name := fmt.Sprintf("state-%s-%s.db", ver, time.Now().UTC().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	// 0600 — там приватные ключи пиров, ровно как в оригинале.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("запись резервной копии %s: %w", path, err)
	}
	return path, nil
}

// ensureEndpoint дозаполняет адрес, который попадёт в клиентские конфиги.
//
// Настройка пустая по умолчанию, а без неё конфиг не собрать вовсе. Берётся
// внешний адрес машины из маршрутизации — тем же способом, что и для SAN
// сертификата. Уже заданный адрес не трогается: его мог поменять пользователь.
func ensureEndpoint(ctx context.Context, st *store.Store, log *slog.Logger) (store.Settings, error) {
	settings, err := st.Settings(ctx)
	if err != nil {
		return store.Settings{}, err
	}
	if settings.EndpointHost != "" {
		return settings, nil
	}
	addr, err := packaging.DetectExternalAddr()
	if err != nil {
		// Без адреса установка не падает: панель поднимется, а адрес
		// подключения пользователь задаст сам. Молча это оставить нельзя —
		// конфиг первого пира тогда не выдастся.
		return settings, fmt.Errorf("внешний адрес сервера не определён, задайте его в панели: %w", err)
	}
	settings.EndpointHost = addr.String()
	if err := st.SaveSettings(ctx, settings); err != nil {
		return store.Settings{}, err
	}
	log.Info("адрес подключения определён", "адрес", settings.EndpointHost)
	return settings, nil
}

// ensureSingbox ставит рантайм нужной версии.
func ensureSingbox(ctx context.Context, root string, log *slog.Logger) (packaging.SingboxResult, error) {
	sbi := packaging.NewSingboxInstaller(root)
	sbi.Log = log
	res, err := sbi.EnsureSingbox(ctx)
	if err != nil {
		return packaging.SingboxResult{}, err
	}
	return res, nil
}

// writeFirstSingboxConfig кладёт первичный конфиг рантайма.
//
// Reload здесь заведомо не нужен и был бы вреден: юнит sing-box ещё не
// установлен, а `systemctl reload-or-restart` на несуществующем юните — ошибка.
// Дальше конфиг пересобирает демон по `POST /api/apply`, уже со штатным reload.
func writeFirstSingboxConfig(ctx context.Context, root, binary string, st *store.Store,
	log *slog.Logger,
) error {
	snap, err := st.Snapshot(ctx)
	if err != nil {
		return err
	}
	applier := &singbox.Applier{
		ConfigPath: filepath.Join(root, singbox.DefaultConfigPath),
		Checker:    singbox.BinaryChecker{Binary: binary},
		Reloader:   noopReloader{},
		Log:        log,
	}
	if _, err := applier.Apply(ctx, snap); err != nil {
		return err
	}
	return nil
}

// noopReloader — перезагружать нечего: сервиса ещё нет.
type noopReloader struct{}

func (noopReloader) Reload(context.Context) error { return nil }

// writeUnits кладёт юниты systemd. Пакетный юнит sing-box не подменяется.
func writeUnits(opts setupOptions, singboxBinary string, log *slog.Logger) error {
	u := &packaging.UnitInstaller{Root: opts.root}
	if _, err := u.EnsureDaemonUnit(opts.daemon); err != nil {
		return err
	}
	configPath := singbox.DefaultConfigPath
	switch _, err := u.EnsureSingboxUnit(singboxBinary, configPath); {
	case errors.Is(err, packaging.ErrUnitPackaged):
		log.Info("юнит sing-box принесён пакетом, свой не пишем")
	case err != nil:
		return err
	}
	// Рабочий каталог рантайма создаём сами: без него юнит падает на старте, а
	// пакет его не создаёт, если рантайм положили мы.
	workDir := filepath.Join(opts.root, packaging.SingboxWorkDir)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("создание каталога %s: %w", workDir, err)
	}
	return nil
}

// ensurePassword заводит пароль панели при первой установке и возвращает его
// открытым текстом ровно один раз — чтобы установщик его напечатал.
//
// Уже заданный пароль не меняется: повторный запуск install.sh — обновление, а
// не сброс доступа.
func ensurePassword(ctx context.Context, st *store.Store, log *slog.Logger) (string, error) {
	switch _, err := st.PasswordHash(ctx); {
	case err == nil:
		return "", nil
	case !errors.Is(err, store.ErrNoPassword):
		return "", err
	}
	password, err := generatePassword()
	if err != nil {
		return "", err
	}
	if err := api.SetPassword(ctx, st, password); err != nil {
		return "", err
	}
	log.Info("пароль панели сгенерирован")
	return password, nil
}

// generatePassword выдаёт пароль из криптостойкого источника. `math/rand`
// здесь был бы дырой: пароль защищает root-демон, открытый в интернет.
func generatePassword() (string, error) {
	var b strings.Builder
	limit := big.NewInt(int64(len(passwordAlphabet)))
	for i := 0; i < passwordLength; i++ {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("генерация пароля панели: %w", err)
		}
		b.WriteByte(passwordAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// ensureFirstPeer создаёт первого пира, если пиров ещё нет. Второе значение —
// создавали ли мы его сейчас: на обновлении QR печатать не надо.
func ensureFirstPeer(ctx context.Context, st *store.Store, name string, log *slog.Logger) (
	store.Peer, bool, error,
) {
	peers, err := st.Peers(ctx)
	if err != nil {
		return store.Peer{}, false, err
	}
	if len(peers) > 0 {
		return store.Peer{}, false, nil
	}
	if strings.TrimSpace(name) == "" {
		name = firstPeerName
	}
	peer, err := api.CreatePeer(ctx, st, name, time.Now())
	if err != nil {
		return store.Peer{}, false, err
	}
	log.Info("создан первый пир", "имя", peer.Name, "адрес", peer.Address)
	return peer, true, nil
}

// savePeerConfig кладёт конфиг рядом с состоянием: скачать его по ssh проще,
// чем переписывать QR с экрана. Права 0600 — в файле приватный ключ клиента.
func savePeerConfig(root, name, conf string) (string, error) {
	dir := filepath.Join(root, defaultStateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("создание каталога %s: %w", dir, err)
	}
	path := filepath.Join(dir, confFileName(name))
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		return "", fmt.Errorf("запись конфига пира %s: %w", path, err)
	}
	return path, nil
}

// confFileName — имя файла конфига. Всё, кроме латиницы, цифр и дефиса,
// заменяется дефисом: имя пира приходит из hostname клиента и в нём бывает
// что угодно, вплоть до пробелов и слэшей.
func confFileName(name string) string {
	var b strings.Builder
	prev := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prev = false
		default:
			if !prev && b.Len() > 0 {
				b.WriteByte('-')
				prev = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "peer"
	}
	return out + ".conf"
}

// panelURL — адрес панели изнутри VPN.
func panelURL(addr string) string {
	return (&url.URL{Scheme: "https", Host: addr}).String()
}

// startServices поднимает сервисы в том порядке, в каком они друг от друга
// зависят: сначала рантайм с уже записанным конфигом, затем демон (он поднимет
// wg0 и зальёт правила), в конце nginx — ему нужен адрес wg0, точнее
// разрешение слушать его заранее (ip_nonlocal_bind).
func startServices(ctx context.Context, log *slog.Logger) error {
	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := systemctl(ctx, "enable", "--now", "sing-box.service"); err != nil {
		return err
	}
	if err := systemctl(ctx, "enable", "--now", "razdachad.service"); err != nil {
		return err
	}
	// nginx мог не запускаться ни разу: reload на остановленном сервисе
	// отвечает отказом, поэтому reload-or-restart.
	if err := systemctl(ctx, "enable", "nginx.service"); err != nil {
		return err
	}
	if err := systemctl(ctx, "reload-or-restart", "nginx.service"); err != nil {
		return err
	}
	log.Info("сервисы запущены")
	return nil
}

// isTerminal отвечает, идёт ли вывод на терминал. Нужен ради QR: цвет в
// перенаправленном в файл выводе — мусор из управляющих последовательностей.
func isTerminal(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
