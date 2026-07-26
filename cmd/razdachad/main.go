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
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/netstack"
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
	resetNetwork := flag.Bool("reset-network", false,
		"снять nft-правила и маршрутизацию, wg0 оставить, и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if *resetNetwork {
		if err := resetNetfilter(); err != nil {
			slog.Error("сброс сети", "ошибка", err)
			os.Exit(2)
		}
		slog.Info("правила и маршрутизация сняты")
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

	wg, err := startWG(ctx, st)
	if err != nil {
		return err
	}
	defer func() { _ = wg.Close() }()
	go syncWGPeers(ctx, st, wg)

	nf, err := startNetfilter(ctx, st, slog.Default())
	if err != nil {
		return err
	}
	defer nf.stop()

	// Без пароля демон не стартует: панель доступна из интернета, и запуск
	// «пока без авторизации» отдал бы наружу root-демон (ADR 0009).
	srv, err := api.New(ctx, api.Config{
		Addr:            listen,
		Store:           st,
		ServerPublicKey: wg.PublicKey,
		PeerStats:       wg.Stats,
		// Источник свежести списков не заполнен: планировщик слоя lists к
		// демону ещё не подключён, и проверка честно говорит об этом сама.
		Diag: api.DiagSources{
			WG:        wg.DiagState,
			Nft:       nf.nftState,
			IPForward: netstack.DiagIPForward,
		},
	})
	if errors.Is(err, api.ErrNoPassword) {
		return fmt.Errorf("%w; задайте его: razdachad -set-password", err)
	}
	if err != nil {
		return err
	}
	return srv.Run(ctx)
}

// netfilter — то, что демон получает от подсистемы правил: снятие при
// остановке и чтение состояния для диагностики. Тип объявлен здесь, а не в
// netfilter_linux.go, потому что нужен обеим сборкам.
type netfilter struct {
	// stop вызывается при остановке демона.
	stop func()
	// nftState отдаёт состояние таблицы диагностике. Пустое поле означает
	// «источника нет», и проверка отвечает unknown с объяснением.
	nftState func(context.Context) (netstack.DiagNftState, error)
}

// wgSyncInterval — как часто состояние БД сверяется с интерфейсом.
//
// Сверка идёт диффом: на неизменном состоянии не выполняется ни одной записи и
// живые хендшейки не страдают. Событийная перевыдача пиров приедет вместе с
// применением конфига (issue #30), до неё периодическая сверка — способ не
// заставлять пользователя перезапускать демон после добавления устройства.
const wgSyncInterval = 10 * time.Second

// startWG поднимает wg0 по настройкам из БД. Интерфейс — не опция: без него
// демон не выполняет свою работу, поэтому ошибка здесь останавливает запуск.
func startWG(ctx context.Context, st *store.Store) (*netstack.WGManager, error) {
	settings, err := st.Settings(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := netstack.WGConfigFromSettings(settings)
	if err != nil {
		return nil, err
	}
	wg, err := netstack.NewWGManager(cfg, st, slog.Default())
	if err != nil {
		return nil, err
	}
	if err := wg.Up(ctx); err != nil {
		_ = wg.Close()
		return nil, err
	}
	return wg, nil
}

// syncWGPeers держит набор пиров на интерфейсе равным состоянию БД.
// Интерфейс при остановке демона не снимается: рестарт не должен рвать клиентам
// связь, а возврат системы в исходное состояние — дело удаления пакета.
func syncWGPeers(ctx context.Context, st *store.Store, wg *netstack.WGManager) {
	ticker := time.NewTicker(wgSyncInterval)
	defer ticker.Stop()
	for {
		peers, err := st.Peers(ctx)
		if err != nil {
			slog.Error("чтение пиров", "ошибка", err)
		} else if _, err := wg.SyncPeers(ctx, peers); err != nil {
			slog.Error("синхронизация пиров", "ошибка", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
