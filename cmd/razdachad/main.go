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
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/restorestate"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// version и commit подставляются линкером при сборке, см. Makefile.
// Пустой commit означает сборку мимо Makefile: API отдаёт null, а не выдумку.
var (
	version = "dev"
	commit  string
)

// defaultDBPath — состояние демона, см. docs/01-architecture.md.
const defaultDBPath = "/var/lib/razdacha/state.db"

func main() {
	capabilities := flag.Bool("restore-capabilities", false, "report metadata-only safe restore protocol")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	listen := flag.String("listen", api.DefaultAddr,
		"адрес HTTP-сервера панели; только loopback — наружу его открывает nginx")
	dbPath := flag.String("db", defaultDBPath, "путь к файлу состояния")
	setPassword := flag.Bool("set-password", false,
		"прочитать пароль панели из stdin, сохранить и выйти")
	resetNetwork := flag.Bool("reset-network", false,
		"снять nft-правила и маршрутизацию, wg0 оставить, и выйти")
	flag.Parse()

	absoluteDB, err := filepath.Abs(*dbPath)
	if err != nil {
		slog.Error("resolve restore state", "error", err)
		os.Exit(2)
	}
	trusted, err := restorestate.Read(filepath.Dir(defaultDBPath))
	if err != nil {
		slog.Error("invalid trusted restore state", "error", err)
		os.Exit(2)
	}
	if trusted != nil && absoluteDB != defaultDBPath && !*showVersion && !*capabilities {
		slog.Error("alternate database path refused while trusted restore state exists")
		os.Exit(2)
	}
	if *capabilities {
		fmt.Printf("{\"restore_protocol\":%d}\n", restorestate.Protocol)
		return
	}
	if *showVersion {
		fmt.Println(version)
		return
	}

	if *resetNetwork {
		if err := resetCanonicalNetwork(absoluteDB, defaultDBPath, resetNetfilter); err != nil {
			slog.Error("сброс сети", "ошибка", err)
			os.Exit(2)
		}
		slog.Info("правила и маршрутизация сняты")
		return
	}

	// Сигналы гасят демон мягко: принятые запросы дорабатывают.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, *listen, *dbPath, *setPassword)
	stop()
	if err != nil {
		slog.Error("razdachad остановлен", "ошибка", err)
		os.Exit(2)
	}
}

func run(ctx context.Context, listen, dbPath string, setPassword bool) error {
	dbPath, err := filepath.Abs(dbPath)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}
	if setPassword {
		lock, e := passwordLifecycle(dbPath, defaultDBPath)
		if e != nil {
			return e
		}
		defer func() { _ = lock.Close() }()
	}
	canonical, e := restorestate.Read(filepath.Dir(defaultDBPath))
	if e != nil {
		return e
	}
	if canonical != nil && dbPath != defaultDBPath {
		return errors.New("alternate database refused while trusted restore state exists")
	}
	trusted, err := restorestate.Read(filepath.Dir(dbPath))
	if err != nil {
		return err
	}
	if trusted != nil && trusted.Phase != "complete" {
		return fmt.Errorf("incomplete restore requires explicit local recovery")
	}
	if trusted != nil {
		if err := store.ValidateRestoreSchema(ctx, dbPath); err != nil {
			return err
		}
	}
	var ctl *restorestate.Controller
	if trusted != nil {
		ctl = &restorestate.Controller{Dir: filepath.Dir(dbPath)}
		ctl.StopRuntime = func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return (packaging.RestoreServices{Query: packaging.SystemdQuery, Run: func(ctx context.Context, args ...string) error {
				return exec.CommandContext(ctx, "systemctl", args...).Run()
			}}).StopRuntime(ctx)
		}
	}
	var st *store.Store
	if trusted != nil {
		st, err = store.OpenRestoreCurrent(ctx, dbPath)
	} else {
		st, err = store.Open(ctx, dbPath)
	}
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	if setPassword {
		return storePassword(ctx, st)
	}

	nf, err := startNetfilter(ctx)
	if err != nil {
		return err
	}
	defer nf.stop()
	settings, err := initialWGSettings(ctx, st, dbPath)
	if err != nil {
		return err
	}
	var wg *netstack.WGManager
	if trusted == nil || !trusted.RuntimeInhibited {
		wg, err = startWG(ctx, st, settings)
		if err != nil {
			return err
		}
		defer func() { _ = wg.Close() }()
		go syncWGPeers(ctx, st, wg)
	}
	listsMgr := startLists(ctx, st, dbPath, slog.Default(), ctl)
	if trusted == nil {
		ensureBuiltinPool(ctx, st, slog.Default())
	}
	poolsMgr := startPools(ctx, st, slog.Default(), ctl)

	// Применение конфига собирается здесь, а не внутри слоя api: дверей к нему
	// две — кнопка «Применить» и расписание пулов, которое приносит свежие
	// ключи само (issue #121).
	apply, err := startApply(ctx, st, poolsMgr, listsMgr, nf, dbPath, slog.Default(), ctl)
	if err != nil {
		return err
	}

	if err := registerRestoreActivity(ctx, st, listsMgr, poolsMgr, apply, ctl); err != nil {
		return err
	}

	if wg == nil {
		go syncRestoredPeers(ctx, st, apply)
	}
	serverKey := func(ctx context.Context) (string, error) { return netstack.WGServerPublicKey(ctx, st) }
	var peerStats func(context.Context) (map[string]netstack.WGPeerStat, error)
	var wgDiag func(context.Context) (netstack.DiagWGState, error)
	if wg != nil {
		peerStats = wg.Stats
		wgDiag = wg.DiagState
	}
	// Без пароля демон не стартует: панель доступна из интернета, и запуск
	// «пока без авторизации» отдал бы наружу root-демон (ADR 0009).
	srv, err := api.New(ctx, api.Config{
		Addr:            listen,
		Store:           st,
		ServerPublicKey: serverKey,
		Restore:         ctl,
		PeerStats:       peerStats,
		Diag: api.DiagSources{
			WG: wgDiag,
			Nft: func(ctx context.Context) (netstack.DiagNftState, error) {
				if err := apply.networkStatus(); err != nil {
					return netstack.DiagNftState{}, err
				}
				return nf.nftState(ctx)
			},
			Route:     nf.routeState,
			IPForward: netstack.DiagIPForward,
			Lists:     listsDiag(st, listsMgr),
		},
		// Заливка nft уходит в слой api замыканием: `POST /api/apply`
		// применяет обе половины — конфиг sing-box и таблицу (issue #119).

		Applier: apply,
		// Содержимое plain-списков — тем же приёмом: генератор берёт домены и
		// подсети из кэша планировщика, иначе они никуда не попадают
		// (issue #125). Применению они уже отданы в startApply; здесь их берёт
		// диагностика, которая обязана сверять конфиг так же, как он собран.
		PlainLists: func(url string) (singbox.PlainList, bool) {
			p := plainLists(listsMgr)
			if p == nil {
				return singbox.PlainList{}, false
			}
			return p(url)
		},
		// Состояние обновления списков — тем же приёмом: панель показывает его
		// рядом с чипом списка в строке правила (issue #149).
		ListStates: listStates(listsMgr),
		Pools:      poolsMgr,
		Build:      buildInfo(),
	})
	if errors.Is(err, api.ErrNoPassword) {
		return fmt.Errorf("%w; задайте его: razdachad -set-password", err)
	}
	if err != nil {
		return err
	}

	// Готовность объявляется здесь, а не при старте процесса: wg0 поднят,
	// правила залиты, панель сейчас начнёт слушать. Установщик печатает QR
	// сразу после `systemctl start`, и на этот момент клиент обязан
	// подключаться. Отказ сокета не останавливает демон — работать он может и
	// без systemd, — но в лог попадает: юнит уйдёт в failed по таймауту, и
	// причину придётся искать.
	if err := notifyReady(); err != nil {
		slog.Error("готовность systemd не отправлена", "ошибка", err)
	}

	return srv.Run(ctx)
}

// buildInfo собирает факты сборки для `GET /api/version`: их знает точка входа,
// слой api их только отдаёт.
//
// Коммит берётся от линкера, а не из вшитой информации о сборке
// (`debug.ReadBuildInfo`, настройка `vcs.revision`), хотя она и доступна: в
// связанном git-worktree она показывает HEAD основного чекаута, а не рабочего
// дерева. То есть врёт ровно в той ситуации, ради которой ручку и завели —
// когда собранное и работающее расходятся. Сборка мимо `Makefile` оставляет
// коммит пустым, и API отдаёт null вместо выдуманного значения.
func buildInfo() api.Build {
	// Версия библиотеки sing-box — не условие работы: не определилась, панель
	// скажет «неизвестна», а демон останавливать из-за этого не за что.
	lib, err := packaging.SingboxLibraryVersion()
	if err != nil {
		slog.Warn("версия библиотеки sing-box не определена", "ошибка", err)
	}
	return api.Build{Version: version, Commit: commit, SingboxLibrary: lib}
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
	// routeState отдаёт состояние правила по метке и таблицы маршрутизации.
	// Пустое поле означает «источника нет» — как и у nftState.
	routeState func(context.Context) (netstack.DiagRouteState, error)
	// Publication is owned exclusively by the shared apply gate.
	guard       func(bool) error
	ensureRoute func(context.Context) error
	publish     func(context.Context, store.Snapshot, map[string]lists.List) (int, error)
}

// listsDiag — источник свежести списков для диагностики: набор источников
// берётся из состояния БД, загруженность и время прогона — у планировщика.
//
// Планировщик не поднялся — источник не подделывается под пустой кэш: проверка
// должна сказать, что списки не обновляются, а не что они свежие.
func listsDiag(st *store.Store, m *lists.Manager) func(context.Context) (api.DiagLists, error) {
	return func(ctx context.Context) (api.DiagLists, error) {
		if m == nil {
			return api.DiagLists{}, errors.New("кэш списков не открыт, планировщик не работает")
		}
		snap, err := st.Snapshot(ctx)
		if err != nil {
			return api.DiagLists{}, err
		}
		out := api.DiagLists{LastRefresh: m.LastRefresh()}
		for _, src := range lists.Sources(snap) {
			out.Sources++
			if _, ok := m.List(src.URL); ok {
				out.Loaded++
			}
		}
		return out, nil
	}
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
func startWG(ctx context.Context, st *store.Store, settings store.Settings) (*netstack.WGManager, error) {
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
