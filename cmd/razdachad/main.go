// Command razdachad — демон селективного VPN-шлюза razdacha.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/store"
)

// version подставляется линкером при сборке, см. Makefile.
var version = "dev"

// defaultDBPath — состояние демона, см. docs/01-architecture.md.
const defaultDBPath = "/var/lib/razdacha/state.db"

func main() {
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	listen := flag.String("listen", api.DefaultAddr,
		"адрес HTTP-сервера панели; только loopback — наружу его открывает nginx")
	dbPath := flag.String("db", defaultDBPath, "путь к файлу состояния")
	setPassword := flag.Bool("set-password", false,
		"прочитать пароль панели из stdin, сохранить и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	// Сигналы гасят демон мягко: принятые запросы дорабатывают.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, *listen, *dbPath, *setPassword)
	stop()
	if err != nil {
		slog.Error("razdachad остановлен", "ошибка", err)
		os.Exit(2)
	}
}

func run(ctx context.Context, listen, dbPath string, setPassword bool) error {
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	if setPassword {
		return storePassword(ctx, st)
	}

	// Без пароля демон не стартует: панель доступна из интернета, и запуск
	// «пока без авторизации» отдал бы наружу root-демон (ADR 0009).
	srv, err := api.New(ctx, api.Config{Addr: listen, Store: st})
	if errors.Is(err, api.ErrNoPassword) {
		return fmt.Errorf("%w; задайте его: razdachad -set-password", err)
	}
	if err != nil {
		return err
	}
	return srv.Run(ctx)
}

// storePassword читает пароль из stdin, а не из аргумента командной строки:
// аргументы видны в выводе `ps` любому пользователю машины.
func storePassword(ctx context.Context, st *store.Store) error {
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return fmt.Errorf("чтение пароля: %w", err)
		}
		return errors.New("пароль не прочитан: пустой ввод")
	}
	if err := api.SetPassword(ctx, st, sc.Text()); err != nil {
		return err
	}
	slog.Info("пароль панели сохранён")
	return nil
}
