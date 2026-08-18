package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/store"
)

// passphraseEnv — парольная фраза для запуска без терминала.
//
// Аргументом командной строки фраза не передаётся никогда: аргументы процесса
// видны в `ps` любому пользователю системы всё время работы команды. По той же
// причине токен телеграма читается установщиком из окружения (docs/09).
const passphraseEnv = "RAZDACHA_BACKUP_PASSPHRASE"

// restoreOptions — параметры `razdacha restore`.
type restoreOptions struct {
	root           string
	dbPath         string
	file           string
	force          bool
	passphraseFile string

	// systemctl подменяется в тестах: настоящего systemd на машине
	// разработчика нет, а останавливать демон перед подменой файла обязательно.
	systemctl systemctlRunner
	// unitActive отвечает, работает ли демон. Подменяется вместе с systemctl.
	unitActive func(ctx context.Context) bool
	// stdin — откуда читается фраза, когда её не дали ни файлом, ни окружением.
	stdin *os.File
}

// runRestore поднимает состояние из копии (ADR 0016).
//
// Порядок шагов задан одним требованием: рабочий файл состояния не трогается,
// пока копия не разобрана, не расшифрована, не проверена и не мигрирована.
// Отказ на любом из этих шагов оставляет сервер ровно таким, каким он был.
func runRestore(ctx context.Context, args []string) error {
	opts, err := parseRestoreFlags(args)
	if err != nil {
		return err
	}
	opts.systemctl = systemctl
	opts.unitActive = daemonActive
	opts.stdin = os.Stdin
	return restore(ctx, opts)
}

func parseRestoreFlags(args []string) (restoreOptions, error) {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	opts := restoreOptions{}
	flags.StringVar(&opts.root, "root", "", "корень файловой системы; пустой означает /")
	flags.StringVar(&opts.dbPath, "db", defaultDBPath, "путь к файлу состояния")
	flags.BoolVar(&opts.force, "force", false,
		"разрешить восстановление поверх непустой БД: прежнее состояние будет заменено")
	flags.StringVar(&opts.passphraseFile, "passphrase-file", "",
		"файл с парольной фразой; иначе "+passphraseEnv+" или запрос в терминале")
	if err := flags.Parse(args); err != nil {
		return restoreOptions{}, err
	}
	if flags.NArg() != 1 {
		return restoreOptions{}, errors.New("укажите файл копии: razdacha restore <файл>")
	}
	opts.file = flags.Arg(0)
	return opts, nil
}

func restore(ctx context.Context, opts restoreOptions) error {
	data, err := os.ReadFile(opts.file)
	if err != nil {
		return fmt.Errorf("чтение копии %s: %w", opts.file, err)
	}

	// Формат виден из самого файла: фразу спрашиваем только там, где она нужна,
	// и только после того, как убедились, что файл вообще наш.
	switch {
	case store.IsEncryptedBackup(data):
		phrase, err := readPassphrase(opts)
		if err != nil {
			return err
		}
		plain, err := store.DecryptBackup(data, phrase)
		if err != nil {
			// Ни фраза, ни её длина в вывод не попадают.
			return fmt.Errorf("расшифровка копии %s: %w", opts.file, err)
		}
		data = plain
	case store.IsStateFile(data):
	default:
		return fmt.Errorf("%w: %s", store.ErrNotBackup, opts.file)
	}
	if !store.IsStateFile(data) {
		return fmt.Errorf("%w: расшифрованный файл не база SQLite", store.ErrNotBackup)
	}

	dbPath := filepath.Join(opts.root, opts.dbPath)
	target, err := store.StateSummaryAt(ctx, dbPath)
	if err != nil {
		return err
	}
	if !target.Empty() && !opts.force {
		return fmt.Errorf("в %s уже есть состояние: %s. "+
			"Восстановление заменит его целиком — повторите с -force, если это то, что нужно",
			dbPath, describeState(target))
	}

	// Копия раскладывается рядом с рабочим файлом, а не во временном каталоге:
	// подмена обязана быть переименованием в пределах одной файловой системы,
	// иначе половина копии окажется на месте базы при нехватке места.
	staged := dbPath + ".restore"
	if err := writeStaged(staged, data); err != nil {
		return err
	}
	// Вместе с самим файлом убираются и его журналы: открытие копии для
	// миграций заводит рядом `-wal` и `-shm`, и оставленные после отказа они
	// пережили бы команду.
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(staged + suffix)
		}
	}()

	// Проверка версии и миграции — тем же механизмом, что у демона при старте:
	// открытие отвергает схему из будущего и накатывает недостающие шаги на
	// схему из прошлого. Второго механизма сравнения версий в системе нет.
	before, err := store.StateSummaryAt(ctx, staged)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, staged)
	if err != nil {
		return fmt.Errorf("копия %s не принята: %w", opts.file, err)
	}
	after, err := store.StateSummaryAt(ctx, staged)
	if cerr := st.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if after.Empty() {
		return fmt.Errorf("в копии %s нет ни пиров, ни туннелей, ни правил — "+
			"это не состояние razdacha", opts.file)
	}

	// Демон останавливается только теперь: до этого момента всё, что могло
	// отказать, уже отказало, и сервер не оставался бы выключенным зря.
	running := opts.unitActive != nil && opts.unitActive(ctx)
	if running {
		if err := opts.systemctl(ctx, "stop", packaging.DaemonUnit); err != nil {
			return err
		}
	}

	if !target.Empty() {
		saved, err := backupDB(dbPath, "before-restore")
		if err != nil {
			return err
		}
		fmt.Printf("прежнее состояние сохранено: %s\n", saved)
	}
	if err := replaceDB(staged, dbPath); err != nil {
		return err
	}

	if running {
		if err := opts.systemctl(ctx, "start", packaging.DaemonUnit); err != nil {
			return err
		}
	}

	fmt.Printf("состояние восстановлено из %s: %s\n", opts.file, describeState(after))
	if before.SchemaVersion != after.SchemaVersion {
		fmt.Printf("схема БД обновлена с версии %d до %d\n",
			before.SchemaVersion, after.SchemaVersion)
	}
	fmt.Println("клиентские конфиги менять не нужно: ключи пиров восстановлены как были.")
	fmt.Println("если у нового сервера другой внешний адрес — поправьте его в настройках " +
		"панели и перевыдайте конфиги.")
	return nil
}

// writeStaged кладёт разобранную копию рядом с рабочим файлом.
//
// Права 0600 ставятся до записи содержимого: в файле приватные ключи пиров, и
// окна, в котором он читается кем угодно, быть не должно.
func writeStaged(path string, data []byte) error {
	_ = os.Remove(path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("подготовка копии %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("запись копии %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("запись копии %s: %w", path, err)
	}
	return nil
}

// replaceDB ставит подготовленный файл на место рабочего.
//
// Журналы `-wal` и `-shm` от прежней базы снимаются: оставленные рядом с новым
// файлом, они не старое состояние, а повреждённая база — SQLite примет их за
// журнал того, что лежит сейчас.
func replaceDB(staged, dbPath string) error {
	if err := os.Rename(staged, dbPath); err != nil {
		return fmt.Errorf("подмена состояния %s: %w", dbPath, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("удаление журнала %s: %w", dbPath+suffix, err)
		}
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		return fmt.Errorf("права на состояние %s: %w", dbPath, err)
	}
	return nil
}

// readPassphrase берёт фразу из файла, окружения или терминала — в этом порядке.
func readPassphrase(opts restoreOptions) (string, error) {
	if opts.passphraseFile != "" {
		data, err := os.ReadFile(opts.passphraseFile)
		if err != nil {
			return "", fmt.Errorf("чтение парольной фразы: %w", err)
		}
		return trimPassphrase(string(data))
	}
	if v := os.Getenv(passphraseEnv); v != "" {
		return trimPassphrase(v)
	}
	if opts.stdin == nil {
		return "", errors.New("копия зашифрована: задайте " + passphraseEnv +
			" или -passphrase-file")
	}
	fmt.Print("Парольная фраза копии (ввод виден на экране): ")
	sc := bufio.NewScanner(opts.stdin)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", fmt.Errorf("чтение парольной фразы: %w", err)
		}
		return "", errors.New("парольная фраза не прочитана: пустой ввод")
	}
	return trimPassphrase(sc.Text())
}

// trimPassphrase снимает перевод строки, который приезжает и из файла, и из
// терминала. Пробелы внутри фразы остаются — они её часть.
func trimPassphrase(v string) (string, error) {
	v = strings.Trim(v, "\r\n")
	if v == "" {
		return "", errors.New("парольная фраза пуста")
	}
	return v, nil
}

// describeState — что лежит в базе, словами для вывода команды.
func describeState(v store.StateSummary) string {
	return fmt.Sprintf("пиров %d, туннелей %d, правил %d", v.Peers, v.Tunnels, v.Rules)
}

// daemonActive отвечает, работает ли демон сейчас.
func daemonActive(ctx context.Context) bool {
	return systemctlQuiet(ctx, "is-active", "--quiet", packaging.DaemonUnit)
}
