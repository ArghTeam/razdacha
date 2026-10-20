package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/restorestate"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// passphraseEnv — парольная фраза для запуска без терминала.
//
// Аргументом командной строки фраза не передаётся никогда: аргументы процесса
// видны в `ps` любому пользователю системы всё время работы команды. По той же
// причине токен телеграма читается установщиком из окружения (docs/09).
const passphraseEnv = "RAZDACHA_BACKUP_PASSPHRASE"

// restoreDBFiles is a bounded syscall seam for replacement/crash regression tests.
// Production uses the real durable filesystem operations below.
type restoreDBFiles struct {
	syncFile func(string) error
	syncDir  func(string) error
	rename   func(string, string) error
}

func defaultRestoreDBFiles() restoreDBFiles {
	return restoreDBFiles{restorestate.SyncFile, restorestate.SyncDir, os.Rename}
}

// restoreOptions contains private restore adapters; no production fault switch exists.
type restoreOptions struct {
	dbFiles        *restoreDBFiles
	afterDurable   func(string) error
	services       *packaging.RestoreServices
	attest         func(context.Context, string) ([]byte, error)
	checklist      bool
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
	stdin        *os.File
	closeIngress func() error
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
	if opts.root != "" || opts.dbPath != defaultDBPath {
		return errors.New("restore supports only the canonical managed installation")
	}
	opts.services = &packaging.RestoreServices{Query: packaging.SystemdQuery, Run: systemctl}
	opts.attest = packaging.AttestRestoreBinary
	opts.systemctl = systemctl
	opts.unitActive = daemonActive
	opts.stdin = os.Stdin
	if opts.root == "" {
		opts.closeIngress = installClientGuard
	}
	return restore(ctx, opts)
}

func parseRestoreFlags(args []string) (restoreOptions, error) {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	opts := restoreOptions{}
	flags.BoolVar(&opts.checklist, "destination-reviewed", false, "confirm endpoint, WAN, NAT, panel exposure and certificate SAN review")
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
	dbPath, err := filepath.Abs(filepath.Join(opts.root, opts.dbPath))
	if err != nil {
		return err
	}
	dir := filepath.Dir(dbPath)
	lock, err := restorestate.Acquire(dir, -1)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	previous, err := restorestate.Read(dir)
	if err != nil {
		return err
	}
	if previous != nil && previous.Phase != "complete" {
		return errors.New("unfinished restore requires explicit local recovery")
	}
	info, err := os.Stat(opts.file)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return errors.New("backup must be a bounded regular file")
	}
	data, err := os.ReadFile(opts.file)
	if err != nil {
		return fmt.Errorf("read restore archive: %w", err)
	}
	if store.IsEncryptedBackup(data) {
		phrase, e := readPassphrase(opts)
		if e != nil {
			return e
		}
		data, e = store.DecryptBackup(data, phrase)
		if e != nil {
			return fmt.Errorf("decrypt restore archive: %w", e)
		}
	}
	if !store.IsStateFile(data) {
		return store.ErrNotBackup
	}
	op, err := os.MkdirTemp(dir, ".restore-")
	if err != nil {
		return err
	}
	if err = os.Chmod(op, 0o700); err != nil {
		return err
	}
	if err = restorestate.SyncDir(dir); err != nil {
		return err
	}
	staged := filepath.Join(op, "incoming.db")
	if err = writeStaged(staged, data); err != nil {
		return err
	}
	if _, err = store.PrepareRestore(ctx, staged, singbox.RestoreWireGuardIDs); err != nil {
		return err
	}
	after, err := store.StateSummaryAt(ctx, staged)
	if err != nil {
		return err
	}
	if after.Empty() {
		return errors.New("archive contains no peers, tunnels or rules")
	}
	target, err := store.RestoreSummary(ctx, dbPath)
	if err != nil {
		return err
	}
	if !target.Empty() && !opts.force {
		return errors.New("destination has existing state; repeat with -force to replace it")
	}
	if !opts.checklist {
		return errors.New("review endpoint, WAN interface, UDP/NAT mapping, panel exposure and certificate SANs; repeat with -destination-reviewed")
	}
	if opts.services == nil || opts.attest == nil || opts.closeIngress == nil {
		return errors.New("restore requires contained managed service, capability and ingress adapters")
	}
	services, err := opts.services.Preflight(ctx)
	if err != nil {
		return err
	}
	binary := filepath.Join(opts.services.Root, defaultDaemon)
	attested, err := opts.attest(ctx, binary)
	if err != nil {
		return err
	}
	helper := filepath.Join(opts.services.Root, defaultBinDir, "razdacha")
	helperAttested, err := opts.attest(ctx, helper)
	if err != nil {
		return fmt.Errorf("installed runtime start helper is incompatible: %w", err)
	}
	fmt.Printf("Destination checklist acknowledged. Private current-destination recovery: %s\n", op)
	record := restorestate.State{Version: restorestate.Protocol, Operation: filepath.Base(op), Recovery: op, Phase: "preparing", Held: true, RuntimeInhibited: true}
	recovery := restorestate.Recovery{}
	recovery.Services, err = json.Marshal(services)
	if err != nil {
		return err
	}
	// Save the original safety/drop-in metadata BEFORE any inhibitor installation.
	metadata := []string{filepath.Join(dir, restorestate.File), filepath.Join(dir, restorestate.Incomplete), filepath.Join(dir, restorestate.Runtime), filepath.Join(dir, restorestate.PermitFile)}
	for _, unit := range []string{packaging.DaemonUnit, packaging.SingboxUnit} {
		metadata = append(metadata, filepath.Join(opts.services.Root, packaging.DefaultUnitDir, unit+".d", packaging.RestoreDropIn))
	}
	for _, path := range metadata {
		f, e := restorestate.CaptureFile(op, path)
		if e != nil {
			return e
		}
		recovery.Files = append(recovery.Files, f)
	}
	if err = restorestate.SaveRecovery(op, recovery); err != nil {
		return err
	}
	if err = opts.observeDurable("recovery-metadata"); err != nil {
		return err
	}
	if err = restorestate.Save(dir, record); err != nil {
		return err
	}
	if err = opts.observeDurable("preparing-record"); err != nil {
		return err
	}
	if err = opts.services.Inhibit(ctx, dir); err != nil {
		return err
	}
	if err = opts.observeDurable("inhibitors-installed"); err != nil {
		return err
	}
	if err = opts.services.Stop(ctx); err != nil {
		return err
	}
	if err = opts.observeDurable("services-stopped"); err != nil {
		return err
	}
	if err = opts.closeIngress(); err != nil {
		return err
	}
	if err = opts.observeDurable("ingress-guarded"); err != nil {
		return err
	}
	_, err = collectDestinationRecovery(ctx, op, dbPath, opts.services.Root, recovery)
	if err != nil {
		return err
	}
	if err = opts.observeDurable("recovery-complete"); err != nil {
		return err
	}
	runtime := filepath.Join(opts.services.Root, singbox.DefaultConfigPath)
	// Runtime is stopped and persistently inhibited; remove its sole effective config.
	if err = restorestate.Remove(runtime); err != nil {
		return err
	}
	if err = opts.observeDurable("runtime-quarantined"); err != nil {
		return err
	}
	if err = networkstate.Invalidate(dir); err != nil {
		return err
	}
	if err = opts.observeDurable("authority-invalidated"); err != nil {
		return err
	}
	if err = replaceDBUsing(staged, dbPath, opts.dbFiles); err != nil {
		return err
	}
	if err = opts.observeDurable("database-published"); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	again, err := opts.attest(ctx, binary)
	if err != nil {
		return err
	}
	if !bytes.Equal(attested, again) {
		return errors.New("installed daemon changed before startup; explicit recovery required")
	}
	helperAgain, err := opts.attest(ctx, helper)
	if err != nil {
		return err
	}
	if !bytes.Equal(helperAttested, helperAgain) {
		return errors.New("installed runtime start helper changed before startup; explicit recovery required")
	}
	record.Phase = "complete"
	if err = restorestate.Remove(filepath.Join(dir, restorestate.Incomplete)); err != nil {
		return err
	}
	if err = restorestate.Save(dir, record); err != nil {
		return err
	}
	if err = opts.observeDurable("restore-complete"); err != nil {
		return err
	}
	if services[packaging.DaemonUnit].Active == "active" {
		if err = opts.services.Run(ctx, "start", packaging.DaemonUnit); err != nil {
			return err
		}
	}
	fmt.Printf("Restore complete. Outgoing WireGuard is paused; external activity is held; client ingress remains guarded. Recovery: %s\n", op)
	fmt.Println("Review destination settings, explicitly resume external activity, then Apply paused policy. Enable chosen WireGuard credentials separately and Apply again. Endpoint or certificate changes may require client updates.")
	return nil
}

// writeStaged кладёт разобранную копию рядом с рабочим файлом.
//
// Права 0600 ставятся до записи содержимого: в файле приватные ключи пиров, и
// окна, в котором он читается кем угодно, быть не должно.
func writeStaged(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	return restorestate.SyncDir(filepath.Dir(path))
}

func (opts restoreOptions) observeDurable(cut string) error {
	if opts.afterDurable != nil {
		return opts.afterDurable(cut)
	}
	return nil
}

// replaceDB checkpoints old WAL before journal removal and durable atomic publication.
func replaceDBUsing(staged, dbPath string, configured *restoreDBFiles) error {
	ops := defaultRestoreDBFiles()
	if configured != nil {
		ops = *configured
	}
	if ops.syncFile == nil || ops.syncDir == nil || ops.rename == nil {
		return errors.New("incomplete restore DB filesystem adapter")
	}
	if _, e := os.Stat(dbPath); e == nil {
		if e = store.CheckpointAt(context.Background(), dbPath); e != nil {
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if e := restorestate.Remove(dbPath + suffix); e != nil {
			return e
		}
	}
	if e := ops.syncFile(staged); e != nil {
		return e
	}
	if e := ops.rename(staged, dbPath); e != nil {
		return e
	}
	if e := ops.syncDir(filepath.Dir(staged)); e != nil {
		return e
	}
	return ops.syncDir(filepath.Dir(dbPath))
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

// daemonActive отвечает, работает ли демон сейчас.
func daemonActive(ctx context.Context) bool {
	return systemctlQuiet(ctx, "is-active", "--quiet", packaging.DaemonUnit)
}

// collectDestinationRecovery is legal only before imported DB/runtime/authority
// mutation. A durable Complete manifest is the authority for all later rollback.
func collectDestinationRecovery(ctx context.Context, op, dbPath, serviceRoot string, recovery restorestate.Recovery) (restorestate.Recovery, error) {
	dir := filepath.Dir(dbPath)
	var err error
	// A stable SQLite snapshot includes committed WAL pages and does not migrate old state.
	if _, e := os.Stat(dbPath); e == nil {
		snapshotFile, e := os.CreateTemp(op, "destination-*.db")
		if e != nil {
			return recovery, e
		}
		snapshot := snapshotFile.Name()
		if e = snapshotFile.Close(); e != nil {
			return recovery, e
		}
		if e = os.Remove(snapshot); e != nil {
			return recovery, e
		}
		if err = store.BackupAt(ctx, dbPath, snapshot); err != nil {
			return recovery, err
		}
		if err = os.Chmod(snapshot, 0o600); err != nil {
			return recovery, err
		}
		if err = restorestate.SyncFile(snapshot); err != nil {
			return recovery, err
		}
		f, e := restorestate.CaptureFile(op, snapshot)
		if e != nil {
			return recovery, e
		}
		f.Target = dbPath
		recovery.Files = append(recovery.Files, f)
		recovery.DatabasePresent = true
	} else if !errors.Is(e, os.ErrNotExist) {
		return recovery, e
	}
	runtime := filepath.Join(serviceRoot, singbox.DefaultConfigPath)
	for _, path := range []string{runtime, filepath.Join(dir, networkstate.CommittedFile), filepath.Join(dir, networkstate.BootstrapFile)} {
		f, e := restorestate.CaptureFile(op, path)
		if e != nil {
			return recovery, e
		}
		recovery.Files = append(recovery.Files, f)
	}
	// Preserve every native artifact file, not just the latest mutable accepted manifest.
	native := filepath.Join(dir, "lists", "native")
	if err = ensureRecoveryParent(filepath.Join(native, ".recovery-parent-check"), dir); err != nil {
		return recovery, err
	}
	if err = filepath.WalkDir(native, func(path string, d os.DirEntry, e error) error {
		if errors.Is(e, os.ErrNotExist) && path == native {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		f, e := restorestate.CaptureFile(op, path)
		if e != nil {
			return e
		}
		recovery.Files = append(recovery.Files, f)
		return nil
	}); err != nil {
		return recovery, err
	}
	recovery.Complete = true
	if err = restorestate.SaveRecovery(op, recovery); err != nil {
		return recovery, err
	}
	if _, err = restorestate.ReadRecovery(op); err != nil {
		return recovery, err
	}
	return recovery, nil
}
