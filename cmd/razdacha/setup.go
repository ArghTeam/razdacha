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

// unknownVersion — имя версии в резервной копии, снятой с БД, которая свою
// версию не записывала. Так выглядит любое обновление с версий до 0.2.1:
// придумать версию задним числом нечем, а соврать в имени файла отката нельзя.
const unknownVersion = "unknown"

// setupOptions — параметры `razdacha setup`.
type setupOptions struct {
	root     string
	dbPath   string
	daemon   string
	peerName string

	// public и publicSet — режим панели и то, задавали ли его этим запуском.
	// Различать обязательно: режим хранится в БД, и обновление без флага
	// оставляет прежний, а не приватный по умолчанию флага (issue #81).
	public    bool
	publicSet bool

	start bool
	color bool
}

// runSetup — фаза изменений и фаза вывода из docs/08-install-upgrade.md.
//
// Фаза проверок сюда не входит: она целиком в `install.sh` и выполняется до
// того, как эта команда запустится. Здесь система уже меняется, и порядок шагов
// задан зависимостями: сначала состояние и ключи, потом файлы, потом юниты, и
// только затем запуск сервисов.
func runSetup(ctx context.Context, args []string) error {
	opts, err := parseSetupFlags(args)
	if err != nil {
		return err
	}
	opts.color = isTerminal(os.Stdout)

	// Вывод печатается и при неудаче запуска сервисов. Пароль панели
	// сгенерирован и лежит в БД одним лишь хешем: не напечатав его здесь, мы
	// потеряем его навсегда, и пользователю останется `razdachad -set-password`
	// на машине, куда он ещё не может зайти.
	out, setupErr := setup(ctx, opts)
	if out.PanelURL != "" {
		text, err := out.Render()
		if err != nil {
			return errors.Join(setupErr, err)
		}
		fmt.Print(text)
	}
	return setupErr
}

// parseSetupFlags разбирает аргументы команды.
func parseSetupFlags(args []string) (setupOptions, error) {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	opts := setupOptions{}
	flags.StringVar(&opts.root, "root", "", "корень файловой системы; пустой означает /")
	flags.StringVar(&opts.dbPath, "db", defaultDBPath, "путь к файлу состояния")
	flags.StringVar(&opts.daemon, "daemon", defaultDaemon, "путь к бинарнику демона для юнита systemd")
	flags.StringVar(&opts.peerName, "peer", firstPeerName, "имя первого пира")
	flags.BoolVar(&opts.public, "public", false,
		"публичный режим панели: nginx слушает все интерфейсы (ADR 0009); "+
			"-public=false выключает режим обратно")
	flags.BoolVar(&opts.start, "start", true, "запускать сервисы через systemd")
	if err := flags.Parse(args); err != nil {
		return setupOptions{}, err
	}
	// Заданный флаг отличается от незаданного только так: у `flag` значение по
	// умолчанию и явное `-public=false` неразличимы, а решают они разное —
	// «оставь как было» против «выключи».
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "public" {
			opts.publicSet = true
		}
	})
	return opts, nil
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
	//
	// Называется копия версией, состояние которой в ней лежит, а не той, на
	// которую обновляемся: копия и есть путь отката. Версия читается из самой
	// БД отдельным чтением — после store.Open файл был бы уже мигрирован.
	if !fresh {
		prev, err := store.InstalledVersionAt(ctx, dbPath)
		if err != nil {
			return summary{}, err
		}
		backup, err := backupDB(dbPath, prev)
		if err != nil {
			return summary{}, err
		}
		log.Info("резервная копия состояния", "путь", backup, "версия", backupVersion(prev))
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

	// Установщик заводится до решения о режиме: у него же и спрашивается, что
	// стоит в конфиге nginx, когда режим в БД не записан.
	inst := packaging.NewInstaller(opts.root)
	inst.Log = log

	mode, err := ensurePanelMode(ctx, st, inst, opts, log)
	if err != nil {
		return summary{}, err
	}
	if err := ensureNotify(ctx, st, log); err != nil {
		return summary{}, err
	}
	if mode.Public {
		inst.Site = packaging.PublicSiteConfig()
	}
	installRes, err := inst.Install()
	if err != nil {
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

	// Версия записывается последней из того, что трогает БД: по ней назовётся
	// резервная копия при следующем обновлении, и записать её раньше, чем
	// установка сложилась, значило бы пообещать откат к тому, чего не было.
	if err := st.SetInstalledVersion(ctx, version); err != nil {
		return summary{}, err
	}

	out := summary{
		Fresh:             fresh,
		Version:           version,
		Password:          password,
		PanelURL:          panelURL(settings.WGServerAddress),
		WGPort:            settings.WGListenPort,
		PanelPublic:       mode.Public,
		PanelModeChanged:  mode.Changed,
		PanelModeInferred: mode.Inferred,
		PanelModeUnknown:  mode.Undetermined,
		Color:             opts.color,
	}
	// ExternalPanelURL — адрес, по которому панель отвечает снаружи VPN. Берём
	// адрес, уже определённый для SAN сертификата (Installer.certIPs), а не
	// вызываем определение заново: это тот же адрес, на который выписан
	// сертификат, и вторая попытка могла бы разойтись с первой. Пусто, если
	// режим не публичный или адрес определить не удалось — тогда врать нечем,
	// и в выводе печатается честная причина, а не подставной адрес.
	if installRes.ExternalAddr != "" {
		out.ExternalPanelURL = panelURL(installRes.ExternalAddr)
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
		// Собранный вывод возвращается вместе с ошибкой: он уже содержит
		// пароль и QR, и терять их из-за неподнявшегося сервиса нельзя.
		if err := startServices(ctx, systemctl, log); err != nil {
			return out, err
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

// backupVersion — как копия называет сохранённую версию. Пустая версия
// означает БД от сборки, которая свою версию не записывала.
func backupVersion(ver string) string {
	if ver == "" {
		return unknownVersion
	}
	return ver
}

// backupDB копирует состояние перед обновлением.
//
// Миграции накатываются при открытии БД и назад не откатываются: единственный
// способ вернуться на предыдущую версию демона — вернуть файл. Копия называется
// по версии и времени, потому что обновлений на одну версию бывает несколько
// (переустановка того же релиза), и затирать прошлую копию нечем оправдать.
//
// ver — версия, состояние которой сохраняется, то есть та, что стояла до этого
// запуска. Не та, на которую обновляемся: имя файла отката обязано называть то,
// к чему откатываешься (issue #82).
func backupDB(dbPath, ver string) (string, error) {
	dir := filepath.Join(filepath.Dir(dbPath), backupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("создание каталога %s: %w", dir, err)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		return "", fmt.Errorf("чтение состояния %s: %w", dbPath, err)
	}
	name := fmt.Sprintf("state-%s-%s.db", backupVersion(ver), time.Now().UTC().Format("20060102-150405"))
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

// panelMode — чем кончилось решение о режиме панели: сам режим и то, откуда он
// взялся. Происхождение печатается пользователю, поэтому оно и возвращается.
type panelMode struct {
	// Public — режим, в котором поднимется панель.
	Public bool

	// Changed — этот запуск режим изменил.
	Changed bool

	// Inferred — режим не был записан и выведен из лежащего конфига nginx.
	Inferred bool

	// Undetermined — причина, по которой режим вывести не удалось. Непустая
	// означает, что взят приватный режим, а в БД не записано ничего.
	Undetermined string
}

// ensurePanelMode решает, в каком режиме поднимается панель, и запоминает
// решение.
//
// Режим — свойство установки, а не аргумент запуска (ADR 0009 о самом режиме,
// issue #81 о его потере). Обновление документированным однострочником не
// передаёт никаких флагов, и пока режим жил только во флаге, панель молча
// уходила из интернета. Отсюда порядок:
//
//   - `-public` (и `-public=false`) задаёт режим и запоминает его — всегда, даже
//     когда значение совпало с прежним: «выбрал приватный» и «не спрашивали» —
//     разные состояния, и различать их надо до первого обновления;
//   - без флага берётся сохранённое;
//   - сохранённого нет — режим выводится из конфига nginx, который оставила
//     предыдущая установка, и записывается: версии до 0.2.1 ключ не писали, и
//     читать на обновлении с них больше неоткуда;
//   - конфига нет вовсе — первая установка, приватный режим, как было всегда;
//   - конфиг есть, но не разобран — приватный режим, но в БД не пишется ничего
//     и причина уходит в вывод: записать догадку хуже, чем спросить.
//
// Выключается режим тем же флагом, которым включался: `-public=false`, а через
// установщик — `RAZDACHA_PUBLIC=0`. Отдельной команды для этого нет намеренно —
// вторая точка входа в ту же работу расходилась бы с первой.
func ensurePanelMode(ctx context.Context, st *store.Store, inst *packaging.Installer,
	opts setupOptions, log *slog.Logger,
) (panelMode, error) {
	saved, known, err := st.PanelPublic(ctx)
	if err != nil {
		return panelMode{}, err
	}

	// Сохранённый режим закрывает вопрос: конфиг nginx в этой ветке не читается
	// вовсе — он вторичен, а БД первична.
	if known {
		mode := panelMode{Public: saved}
		if opts.publicSet {
			mode.Public, mode.Changed = opts.public, opts.public != saved
		}
		if err := writePanelMode(ctx, st, mode.Public, opts.publicSet, log); err != nil {
			return panelMode{}, err
		}
		return mode, nil
	}

	// Ключа нет: разовая миграция режима из конфига предыдущей установки.
	from := inst.SavedPanelMode()
	mode := panelMode{Public: from.Public, Inferred: from.Known, Undetermined: from.Reason}
	switch {
	case from.Known:
		log.Info("режим панели выведен из конфига nginx", "публичный", from.Public)
	case from.Reason != "":
		log.Warn("режим панели не выведен, поднимаем приватный", "причина", from.Reason)
	}
	if opts.publicSet {
		// Явный флаг перебивает и сохранённое, и выведенное. Выведенное всё
		// равно посчитано: без него нечем ответить, изменил ли запуск режим.
		mode = panelMode{Public: opts.public, Changed: opts.public != from.Public}
	}
	// Пишется только то, что кто-то решил: явный флаг или разобранный конфиг.
	// Приватный режим по умолчанию — не решение, и записывать его нечем.
	if err := writePanelMode(ctx, st, mode.Public, opts.publicSet || from.Known, log); err != nil {
		return panelMode{}, err
	}
	return mode, nil
}

// writePanelMode записывает режим, если его есть на чём основать.
func writePanelMode(ctx context.Context, st *store.Store, public, write bool, log *slog.Logger) error {
	if !write {
		return nil
	}
	if err := st.SetPanelPublic(ctx, public); err != nil {
		return err
	}
	log.Info("режим панели записан", "публичный", public)
	return nil
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
//
// Каждый юнит получает `enable` и `restart` — не `enable --now`.
//
// `--now` запускает остановленный юнит и ничего не делает с работающим. На
// обновлении это означало новый бинарник на диске и старый процесс в памяти
// (`/proc/<pid>/exe` с пометкой `(deleted)`), то есть ни одно изменение демона
// до пользователя не доезжало вовсе — при том, что установщик печатал «сервисы
// запущены» (issue #80). На первой установке разницы нет: `restart` запускает и
// остановленный юнит.
//
// Рантайм перезапускается по той же причине и ещё одной: конфиг генерируется
// целиком, и кэш FakeIP обязан обнулиться вместе с ним.
//
// nginx — отдельная история, и она про `restart` вместо `reload`: установка
// снимает штатный сайт Debian, слушающий `0.0.0.0:80`, и добавляет свой на
// адресе wg0. При перечитывании конфига nginx оставляет прежние сокеты жить до
// конца работы старых воркеров: на свежепоставленной машине это давало открытый
// наружу 80-й порт и панель, не слушающую вовсе. Проверено на чистом Debian 13 —
// после reload `ss -tlpn` показывал `0.0.0.0:80`, после restart появлялись
// `10.8.0.1:80` и `:443`.
func startServices(ctx context.Context, run systemctlRunner, log *slog.Logger) error {
	if err := run(ctx, "daemon-reload"); err != nil {
		return err
	}
	for _, unit := range []string{packaging.SingboxUnit, packaging.DaemonUnit, nginxUnit} {
		if err := run(ctx, "enable", unit); err != nil {
			return err
		}
		if err := run(ctx, "restart", unit); err != nil {
			return err
		}
	}
	log.Info("сервисы запущены и перезапущены на новую версию")
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
