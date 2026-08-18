package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/ArghTeam/razdacha/ui"
)

const (
	// DefaultAddr — адрес по умолчанию. Loopback: наружу панель открывает nginx.
	DefaultAddr = "127.0.0.1:8080"

	// readHeaderTimeout закрывает медленное чтение заголовков (Slowloris).
	readHeaderTimeout = 10 * time.Second
	// readTimeout и writeTimeout считаются с запасом на задержку при переборе.
	readTimeout  = 30 * time.Second
	writeTimeout = 60 * time.Second
	// idleTimeout — сколько держится keep-alive без запросов.
	idleTimeout = 120 * time.Second

	// shutdownTimeout — сколько ждём завершения запросов при остановке.
	shutdownTimeout = 10 * time.Second

	// maxLoginBody — тело запроса входа заведомо мелкое.
	maxLoginBody = 4 << 10

	// maxVerifications — сколько проверок пароля считается одновременно.
	// Каждая занимает argonMemory (64 MiB); без ограничения десяток
	// параллельных попыток съедает память демона целиком.
	maxVerifications = 2

	// sessionSweepInterval — период уборки истёкших сессий.
	sessionSweepInterval = time.Hour
)

// Config — параметры сервера панели.
type Config struct {
	// Addr — адрес прослушивания, только loopback. Пустой — [DefaultAddr].
	Addr string
	// Store — состояние демона; обязателен.
	Store *store.Store
	// Logger — пустой означает slog.Default().
	Logger *slog.Logger
	// Now подменяется в тестах. Пустой — time.Now.
	Now func() time.Time

	// ServerPublicKey отдаёт публичный ключ wg0 — его требует клиентский конфиг.
	// Ключи сервера заводит слой netstack при поднятии интерфейса. Пустое поле
	// (и пустая строка от него) означает «ключа пока нет»: API отдаёт
	// `server_public_key: null`, а выдача конфига честно отказывает.
	ServerPublicKey func(context.Context) (string, error)

	// PeerStats отдаёт замеры с wg0 по публичным ключам пиров. Пустое поле
	// означает «источника нет», и производные поля пиров остаются null.
	PeerStats func(context.Context) (map[string]netstack.WGPeerStat, error)

	// Diag — источники данных диагностики. Пустые поля означают «источника
	// нет»: проверка отвечает unknown с объяснением, а не исчезает из сводки.
	Diag DiagSources

	// UI — статика панели с корнем на index.html. Пустой означает встроенную
	// сборку из ui/dist; тесты подставляют свою.
	UI fs.FS

	// ClashAddr — адрес Clash API sing-box («хост:порт»). Пустой означает
	// [clash.DefaultAddr] — тот же адрес, что ставит генератор конфига.
	// Тесты подставляют адрес httptest вместо настоящего рантайма.
	ClashAddr string

	// Applier применяет конфиг sing-box по `POST /api/apply`. Пустой означает
	// настоящий [singbox.NewApplier] — запись в /etc/sing-box/config.json,
	// `sing-box check` и `systemctl reload`; тесты подставляют свой.
	Applier Applier

	// PlainLists отдаёт содержимое внешних списков, которые sing-box не читает
	// сам (не .srs и не .json). Уходит и в применение, и в диагностику: сверять
	// конфиг с БД по-другому, чем он собирается, значит врать про правила
	// (issue #125). Пустое означает работу без таких списков.
	PlainLists singbox.PlainLists

	// ListStates отдаёт состояние обновления списков, которые качает демон:
	// адрес источника → когда обновился и чем кончилась последняя попытка.
	// Пустое поле означает «источника нет» — состояние каждого списка приходит
	// как `unknown`, а не как удачное обновление: планировщик мог не подняться,
	// и тогда списки не обновляются вовсе.
	ListStates func() map[string]ListState

	// ApplyNft перезаливает таблицу nft по тому же `POST /api/apply`: конфиг и
	// сет подсетей — две половины одного применения. Пустое поле означает
	// «источника нет» — ручка применяет только конфиг и отдаёт `nft: null`;
	// подсистема правил есть только в Linux, и в тестах слоя её нет.
	ApplyNft NftApplier

	// Pools обходит каталог туннеля-пула по `POST /api/tunnels/{id}/refresh`.
	// Пустой означает, что расписание не запущено: ручка отвечает 503, а не
	// делает вид, что обновила каталог.
	Pools poolRefresher

	// PoolEvery — период обхода каталога, от которого считается next_update_at
	// в ответе. Ноль означает [lists.DefaultPoolInterval].
	PoolEvery time.Duration

	// PoolProxies — источник живого состояния пулов. Пустой означает настоящий
	// клиент Clash API; тесты подставляют свой.
	PoolProxies clashProxies

	// WARP регистрирует устройство по `POST /api/tunnels/warp`. Пустой означает
	// настоящий API Cloudflare; тесты подставляют свой — наружу они не ходят.
	WARP warpRegistrar

	// Build — версия демона, коммит и версия библиотеки sing-box для
	// `GET /api/version`. Пустые поля означают сборку мимо `Makefile`: версия
	// отдаётся как `dev`, остальное как null.
	Build Build
}

// Server — HTTP-сервер панели.
type Server struct {
	addr     string
	store    *store.Store
	log      *slog.Logger
	now      func() time.Time
	throttle *throttle
	verify   chan struct{}
	// sleep выдерживает задержку после неудачного входа; в тестах подменяется,
	// чтобы проверка блокировки не занимала секунды реального времени.
	sleep      func(context.Context, time.Duration)
	serverKey  func(context.Context) (string, error)
	peerStat   func(context.Context) (map[string]netstack.WGPeerStat, error)
	diag       DiagSources
	ui         fs.FS
	applier    Applier
	applyNft   NftApplier
	plainLists singbox.PlainLists
	listStates func() map[string]ListState
	clash      *clash.Client
	// reach ходит к домену напрямую для пробы доступности. Подменяется в
	// тестах: настоящая сеть в них не участвует. Пустой означает реальный поход.
	reach reachProber
	// checks — результаты проверок туннелей с момента запуска демона,
	// источник производных полей в `GET /api/tunnels`.
	checks *checkCache
	// events — подтверждение переходов туннеля для оповещений. В памяти
	// намеренно: после перезапуска подтверждение набирается заново.
	events  *tunnelEvents
	handler http.Handler

	pools       poolRefresher
	poolEvery   time.Duration
	poolProxies clashProxies

	// warp регистрирует устройство WARP. Пустой означает настоящий API
	// Cloudflare — до нажатия кнопки к нему никто не обращается.
	warp warpRegistrar

	// notify подменяет отправителя оповещений в тестах: настоящий
	// api.telegram.org в них не участвует. Пустой означает настоящий транспорт.
	notify func(store.NotifyConfig) notifySender

	// build — факты сборки демона, источник `GET /api/version`.
	build Build
}

// notifySender — то, что слою api нужно от отправителя оповещений.
type notifySender interface {
	Send(ctx context.Context, text string) error
}

// New собирает сервер и проверяет условия запуска.
//
// Отсутствие пароля — отказ стартовать, а не запуск без авторизации: панель
// доступна из интернета, и «временно без пароля» означало бы отданный наружу
// root-демон (ADR 0009).
func New(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: не задано хранилище", ErrBadConfig)
	}
	addr := cfg.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	if err := checkAddr(addr); err != nil {
		return nil, err
	}

	if _, err := cfg.Store.PasswordHash(ctx); err != nil {
		if errors.Is(err, store.ErrNoPassword) {
			return nil, ErrNoPassword
		}
		return nil, fmt.Errorf("проверка пароля панели: %w", err)
	}

	s := &Server{
		addr:        addr,
		store:       cfg.Store,
		log:         cfg.Logger,
		now:         cfg.Now,
		serverKey:   cfg.ServerPublicKey,
		peerStat:    cfg.PeerStats,
		diag:        cfg.Diag,
		ui:          cfg.UI,
		applier:     cfg.Applier,
		applyNft:    cfg.ApplyNft,
		plainLists:  cfg.PlainLists,
		listStates:  cfg.ListStates,
		clash:       clash.New(clash.Options{Addr: cfg.ClashAddr}),
		checks:      newCheckCache(),
		events:      newTunnelEvents(),
		pools:       cfg.Pools,
		poolEvery:   cfg.PoolEvery,
		poolProxies: cfg.PoolProxies,
		warp:        cfg.WARP,
		build:       cfg.Build,
		verify:      make(chan struct{}, maxVerifications),
	}
	if s.ui == nil {
		s.ui = ui.Files()
	}
	if s.applier == nil {
		a := singbox.NewApplier()
		a.PlainLists = cfg.PlainLists
		s.applier = a
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.throttle = newThrottle(s.now)
	s.sleep = wait
	if s.reach == nil {
		s.reach = probeReachability
	}
	// Порядок важен: statusWriter создаётся в logging, а recoverer по нему
	// понимает, ушёл ли заголовок ответа до паники.
	s.handler = s.logging(s.recoverer(s.routes()))
	return s, nil
}

// Handler отдаёт обработчик со всеми middleware — для httptest и для встраивания.
func (s *Server) Handler() http.Handler { return s.handler }

// Run поднимает сервер и держит его до отмены контекста, после чего гасит
// мягко: принятые запросы дорабатывают, новые не принимаются.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelDebug),
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("прослушивание %s: %w", s.addr, err)
	}
	s.log.Info("панель слушает", "адрес", ln.Addr().String())

	go s.sweep(ctx)
	go s.watchTunnels(ctx)

	errc := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errc <- err
	}()

	select {
	case err := <-errc:
		if err != nil {
			return fmt.Errorf("сервер панели: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(stopCtx); err != nil {
		return fmt.Errorf("остановка сервера панели: %w", err)
	}
	return <-errc
}

// sweep убирает истёкшие сессии и протухшие счётчики попыток.
func (s *Server) sweep(ctx context.Context) {
	ticker := time.NewTicker(sessionSweepInterval)
	defer ticker.Stop()
	for {
		if n, err := s.store.DeleteExpiredSessions(ctx, s.now()); err != nil {
			s.log.Error("уборка сессий", "ошибка", err)
		} else if n > 0 {
			s.log.Debug("убраны истёкшие сессии", "количество", n)
		}
		s.throttle.sweep()

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// checkAddr не пускает демон на адрес, видимый снаружи loopback: панель
// открывает nginx, сам демон не виден ни из интернета, ни из VPN (ADR 0008,
// частично изменённый ADR 0009 — публичным становится nginx, не демон).
func checkAddr(addr string) error {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return fmt.Errorf("%w: адрес %q не разобран: %w", ErrBadConfig, addr, err)
	}
	if !ap.Addr().IsLoopback() {
		return fmt.Errorf("%w: %s не loopback", ErrPublicListen, ap.Addr())
	}
	return nil
}

// statusWriter запоминает код ответа и факт записи заголовка.
type statusWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.written {
		w.status = status
		w.written = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.status = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

// recoverer не даёт панике в обработчике унести демон целиком: падение UI
// оставило бы без управления работающий маршрутизатор. Паника пишется в лог
// со стеком — иначе она превращается в необъяснимую 500.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw, ok := w.(*statusWriter)
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if err, isErr := v.(error); isErr && errors.Is(err, http.ErrAbortHandler) {
				// Штатный способ оборвать ответ, обрабатывается самим net/http.
				panic(v)
			}
			s.log.Error("паника в обработчике панели",
				"паника", v,
				"метод", r.Method,
				"путь", r.URL.Path,
				"стек", string(debug.Stack()))
			if ok && sw.written {
				// Заголовок уже ушёл, ответ дописать нечем.
				return
			}
			writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
		}()
		next.ServeHTTP(w, r)
	})
}

// logging пишет строку на запрос. Тело не логируется: в нём пароль.
func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.log.Debug("запрос",
			"метод", r.Method,
			"путь", r.URL.Path,
			"код", sw.status,
			"адрес", clientKey(r),
			"длительность", s.now().Sub(start))
	})
}
