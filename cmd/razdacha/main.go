// Command razdacha — установка, обновление и удаление razdacha на машине.
//
// Демон живёт отдельной командой (`razdachad`) и занят своей работой; сюда
// вынесено всё, что делается вокруг него один раз: раскладка файлов, юниты
// systemd, первый пир и возврат системы в исходное состояние.
//
// Фаза проверок из docs/08-install-upgrade.md сюда не входит намеренно: она
// выполняется в `packaging/install.sh` до того, как этот бинарник вообще
// окажется на машине.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// version подставляется линкером при сборке, см. Makefile и workflow релиза.
var version = "dev"

const usage = `razdacha — управление установкой селективного VPN-шлюза.

Команды:
  setup       установить или обновить: sing-box, файлы, юниты, первый пир
  uninstall   снять razdacha и вернуть систему в исходное состояние
  version     показать версию

Подробности по каждой команде: razdacha <команда> -h
`

// main только выбирает код возврата: вся работа в [run], иначе os.Exit
// обрывал бы отложенные вызовы — в том числе снятие обработчика сигналов.
func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd := os.Args[1]; cmd {
	case "setup":
		err = runSetup(ctx, os.Args[2:])
	case "uninstall":
		err = runUninstall(ctx, os.Args[2:])
	case "version", "-version", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n%s", cmd, usage)
		return 2
	}

	if err != nil {
		// Прерывание по Ctrl-C — не сбой установки, а решение пользователя:
		// в лог оно не пишется, но код возврата остаётся ненулевым.
		if !errors.Is(err, context.Canceled) {
			slog.Error("razdacha", "ошибка", err)
		}
		return 1
	}
	return 0
}
