package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/singbox"
)

// uninstallOptions — параметры `razdacha uninstall`.
type uninstallOptions struct {
	root string

	// purge и keepData отвечают на вопрос про /var/lib/razdacha неинтерактивно:
	// скрипт удаления не должен зависать на приглашении.
	purge    bool
	keepData bool

	// removeSingbox — снимать ли рантайм, положенный нами в /usr/local/bin.
	// Пакетный sing-box не трогается никогда.
	removeSingbox bool

	// assumeYes — не задавать вопросов вовсе.
	assumeYes bool
}

// runUninstall возвращает систему в исходное состояние
// (docs/08-install-upgrade.md, «Удаление»).
//
// Шаги идут от самого заметного к самому мелкому и ни один из них не прерывает
// остальные: незакрытая половина удаления хуже, чем ошибка в конце. Всё, что не
// снялось, попадает в итоговую ошибку списком.
func runUninstall(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	opts := uninstallOptions{}
	flags.StringVar(&opts.root, "root", "", "корень файловой системы; пустой означает /")
	flags.BoolVar(&opts.purge, "purge", false, "удалить /var/lib/razdacha вместе с ключами и пирами")
	flags.BoolVar(&opts.keepData, "keep-data", false, "оставить /var/lib/razdacha, ни о чём не спрашивая")
	flags.BoolVar(&opts.removeSingbox, "remove-singbox", false, "удалить sing-box, если его положили мы")
	flags.BoolVar(&opts.assumeYes, "yes", false, "не задавать вопросов")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if opts.purge && opts.keepData {
		return errors.New("-purge и -keep-data взаимоисключающи")
	}
	return uninstall(ctx, opts, os.Stdin)
}

func uninstall(ctx context.Context, opts uninstallOptions, in *os.File) error {
	log := slog.Default()
	var errs []error

	// 1. Сервисы. Останавливаются до снятия сети: работающий демон вернул бы
	//    правила обратно через десять секунд синхронизации.
	for _, unit := range []string{packaging.DaemonUnit, packaging.SingboxUnit} {
		if systemctlQuiet(ctx, "disable", "--now", unit) {
			log.Info("юнит остановлен и отключён", "юнит", unit)
		}
	}

	// 2–3. Интерфейс, nft-правила, маршрутизация.
	if err := resetNetwork(ctx, log); err != nil {
		errs = append(errs, err)
	}

	// 4–5. Файлы nginx, штатный сайт Debian и drop-in с параметрами ядра —
	//      всё это умеет установщик, и он же знает, что чужое не трогается.
	inst := packaging.NewInstaller(opts.root)
	inst.Log = log
	if err := inst.Uninstall(); err != nil {
		errs = append(errs, err)
	}
	if systemctlQuiet(ctx, "is-active", "nginx.service") {
		// restart по той же причине, что и при установке: удаление возвращает
		// штатный сайт, а его сокеты после reload не поднимутся, пока живы
		// старые воркеры.
		if err := systemctl(ctx, "restart", "nginx.service"); err != nil {
			errs = append(errs, err)
		}
	}
	// Параметры ядра снимаются и в работающем ядре: файл удалён, но значение
	// осталось бы до перезагрузки, а `ip_nonlocal_bind` нужен только нам.
	restoreSysctl(opts.root, log)

	// 6. Юниты. После них daemon-reload, иначе systemd помнит удалённые файлы.
	units := &packaging.UnitInstaller{Root: opts.root}
	removed, err := units.RemoveUnits()
	if err != nil {
		errs = append(errs, err)
	}
	for _, path := range removed {
		log.Info("юнит удалён", "путь", path)
	}
	if len(removed) > 0 {
		if err := systemctl(ctx, "daemon-reload"); err != nil {
			errs = append(errs, err)
		}
	}

	// 7. Конфиг рантайма: он сгенерирован нами и без razdacha бесполезен.
	if err := removeIfExists(filepath.Join(opts.root, singbox.DefaultConfigPath), log); err != nil {
		errs = append(errs, err)
	}

	// 8. Вопросы. Задаются последними: к этому моменту система уже вернулась в
	//    исходное состояние, и остаётся только то, что можно захотеть сохранить.
	if err := maybeRemoveState(opts, in, log); err != nil {
		errs = append(errs, err)
	}
	if err := maybeRemoveSingbox(opts, in, log); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("удаление прошло не полностью: %w", errors.Join(errs...))
	}
	fmt.Println("razdacha удалена, система возвращена в исходное состояние.")
	// Бинарники остаются: из одного из них эта команда сейчас и работает,
	// удалять их из-под себя — лишний способ оборвать удаление на середине.
	fmt.Printf("Снять сами бинарники:  rm -f %s %s\n",
		filepath.Join(opts.root, defaultBinDir, "razdachad"),
		filepath.Join(opts.root, defaultBinDir, "razdacha"))
	return nil
}

// restoreSysctl возвращает параметры ядра к состоянию до установки.
//
// Ошибки только логируются: /proc/sys может быть недоступен (контейнер,
// тесты), и это не повод объявлять удаление неудавшимся — drop-in уже снят, и
// после перезагрузки значения вернутся сами.
func restoreSysctl(root string, log *slog.Logger) {
	for key, value := range map[string]string{
		"net/ipv4/ip_nonlocal_bind": "0",
	} {
		path := filepath.Join(root, "/proc/sys", key)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
			log.Warn("параметр ядра не сброшен", "параметр", key, "ошибка", err)
			continue
		}
		log.Info("параметр ядра сброшен", "параметр", key, "значение", value)
	}
	// ip_forward намеренно не сбрасывается: его включают не только мы, и
	// выключить его — значит оборвать чужую маршрутизацию на той же машине.
}

// maybeRemoveState решает судьбу /var/lib/razdacha — там ключи сервера и пиры.
// Удаление необратимо, поэтому по умолчанию спрашиваем, а без терминала
// оставляем каталог на месте.
func maybeRemoveState(opts uninstallOptions, in *os.File, log *slog.Logger) error {
	dir := filepath.Join(opts.root, defaultStateDir)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	remove := opts.purge
	switch {
	case opts.purge, opts.keepData:
	case opts.assumeYes:
		remove = false
	default:
		remove = ask(in, fmt.Sprintf(
			"Удалить %s? Там ключи сервера и конфиги всех устройств, восстановить их будет нечем", dir))
	}
	if !remove {
		log.Info("состояние оставлено", "каталог", dir)
		fmt.Printf("Ключи и пиры оставлены в %s\n", dir)
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("удаление %s: %w", dir, err)
	}
	log.Info("состояние удалено", "каталог", dir)
	return nil
}

// maybeRemoveSingbox снимает рантайм, только если его положили мы. Пакетный
// sing-box принадлежит системе: его могли поставить до нас и использовать для
// другого.
func maybeRemoveSingbox(opts uninstallOptions, in *os.File, log *slog.Logger) error {
	path := filepath.Join(opts.root, packaging.DefaultBinDir, packaging.SingboxBinary)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	remove := opts.removeSingbox
	if !remove && !opts.assumeYes && !opts.purge && !opts.keepData {
		remove = ask(in, fmt.Sprintf("Удалить sing-box (%s)?", path))
	}
	if !remove {
		log.Info("sing-box оставлен", "путь", path)
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("удаление %s: %w", path, err)
	}
	log.Info("sing-box удалён", "путь", path)
	return nil
}

// removeIfExists снимает файл; отсутствие файла ошибкой не считается.
func removeIfExists(path string, log *slog.Logger) error {
	switch err := os.Remove(path); {
	case err == nil:
		log.Info("файл удалён", "путь", path)
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("удаление %s: %w", path, err)
	}
}

// ask задаёт вопрос с ответом по умолчанию «нет».
//
// Без терминала на том конце вопрос не задаётся вовсе и ответом считается
// «нет»: `razdacha uninstall`, запущенная из скрипта, не должна ни зависать на
// приглашении, ни удалять ключи оттого, что stdin оказался пустым.
func ask(in *os.File, question string) bool {
	if !isTerminal(in) {
		fmt.Printf("%s — не спрашиваем, ввод не с терминала; оставлено\n", question)
		return false
	}
	fmt.Printf("%s [y/N]: ", question)
	sc := bufio.NewScanner(in)
	if !sc.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(sc.Text()))
	return answer == "y" || answer == "yes" || answer == "д" || answer == "да"
}
