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
	// UI — статика панели с корнем на index.html. Пустой означает встроенную
	// сборку из ui/dist; тесты подставляют свою.
	UI fs.FS
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
	sleep   func(context.Context, time.Duration)
	ui      fs.FS
	handler http.Handler
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
		addr:   addr,
		store:  cfg.Store,
		log:    cfg.Logger,
		now:    cfg.Now,
		ui:     cfg.UI,
		verify: make(chan struct{}, maxVerifications),
	}
	if s.ui == nil {
		s.ui = ui.Files()
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.throttle = newThrottle(s.now)
	s.sleep = wait
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
