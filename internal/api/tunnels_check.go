package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// Статусы туннеля из docs/05-api.md и мока прототипа (ui/prototype/mock-data.js).
// Отдельные от статусов диагностики: там «ok/warn/error» про проверку, здесь —
// про сам туннель.
const (
	tunnelUnknown    = "unknown"
	tunnelUp         = "up"          // задержка измерена
	tunnelSlow       = "slow"        // отвечает, но дольше clash.SlowThreshold
	tunnelDown       = "down"        // sing-box проверил и не достучался
	tunnelNotApplied = "not_applied" // тега нет в работающем конфиге
)

// tunnelCheck — результат последней проверки одного туннеля.
type tunnelCheck struct {
	Status    string
	Signals   *tunnelSignals
	LatencyMS *int
	At        time.Time
	Detail    string
	// OKAt — когда туннель отвечал в последний раз. Заполняет [checkCache.put],
	// а не тот, кто проверял: отметка переживает неудачные круги и потому
	// принадлежит кэшу, а не отдельному наблюдению.
	OKAt time.Time
}

// okStamp — отметка удачной проверки; нулевое время означает «в этот раз не
// дозвались». `slow` считается удачей: туннель отвечает, пусть и еле-еле, и
// «последний раз отвечал» про него — правда.
func okStamp(res tunnelCheck) time.Time {
	switch res.Status {
	case tunnelUp, tunnelSlow:
		return res.At
	default:
		return time.Time{}
	}
}

// checkCache — результаты проверок, сделанных с момента запуска демона.
//
// В БД они не пишутся намеренно: задержка живёт минуты и после перезапуска
// демона (а с ним и sing-box) уже ничего не значит. Пустой кэш означает
// «не проверялось», и производные поля туннеля остаются null — ноль в latency
// был бы измеренным нулём, а «down» — утверждением о непроверенном туннеле.
type checkCache struct {
	mu     sync.Mutex
	last   map[string]tunnelCheck
	dnsMu  sync.Mutex
	dnsJob *dnsObservationJob
}

func newCheckCache() *checkCache {
	return &checkCache{last: make(map[string]tunnelCheck)}
}

// put кладёт результат очередной проверки, сохраняя отметку последнего удачного
// ответа: неудачная проверка отвечает на вопрос «работает ли сейчас», а не
// стирает знание о том, когда туннель отвечал в последний раз.
func (c *checkCache) put(id string, res tunnelCheck) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ok := okStamp(res); !ok.IsZero() {
		res.OKAt = ok
		if previous := c.last[id].OKAt; previous.After(res.OKAt) {
			res.OKAt = previous
		}
	} else if res.OKAt.IsZero() {
		// Поднятая из БД отметка приходит прямо в res и своё берёт: затирать её
		// пустым кэшем на старте демона нельзя.
		res.OKAt = c.last[id].OKAt
	}
	c.last[id] = res
}

func (c *checkCache) get(id string) (tunnelCheck, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	res, ok := c.last[id]
	return res, ok
}

// keep выбрасывает результаты по туннелям, которых больше нет: удалённый в
// панели туннель не должен держать за собой запись до перезапуска демона.
func (c *checkCache) keep(alive map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.last {
		if !alive[id] {
			delete(c.last, id)
		}
	}
}

// withCheck дописывает в ответ производные поля последней проверки.
func (s *Server) withCheck(t tunnelResponse) tunnelResponse {
	res, ok := s.checks.get(t.ID)
	if !ok {
		return t
	}
	status := res.Status
	at := res.At.UTC()
	t.Status = &status
	t.Signals = res.Signals
	if res.Signals != nil {
		copy := *res.Signals
		for _, sig := range []*signal{&copy.Runtime, &copy.DNS, &copy.HTTPS, &copy.Transport} {
			if sig.ObservedAt != nil {
				age := s.now().Sub(*sig.ObservedAt)
				sig.Fresh = sig.Fresh && age >= 0 && age <= 5*time.Minute
			}
		}
		t.Signals = &copy
	}
	if t.Signals == nil {
		t.Signals = historicalSignals(res.At)
	}
	t.LatencyMS = res.LatencyMS
	t.LastCheck = &at
	if !res.OKAt.IsZero() {
		ok := res.OKAt.UTC()
		t.LastOK = &ok
	}
	return t
}

// tunnelCheckResponse — тело `POST /api/tunnels/{id}/check`.
//
// Поля те же, что у туннеля в списке, плюс объяснение: «down» без причины
// пользователю сказать нечего, а причина у sing-box одна на всю проверку.
type tunnelCheckResponse struct {
	Signals   *tunnelSignals `json:"signals"`
	ID        string         `json:"id"`
	Status    string         `json:"status"`
	LatencyMS *int           `json:"latency_ms"`
	LastCheck time.Time      `json:"last_check"`
	Detail    string         `json:"detail"`
}

// handleCheckTunnel — `POST /api/tunnels/{id}/check`.
//
// Проверку делает sing-box по Clash API: тег строится из id ровно так же, как в
// генераторе ([singbox.TunnelTag]), иначе проверялся бы не тот туннель.
//
// Три исхода различаются намеренно. Туннеля нет в работающем конфиге —
// «не применён», это состояние панели, а не сети: так бывает у только что
// созданного туннеля до нажатия «Применить». Туннель есть, но цель не открылась —
// «down». Не отвечает сам sing-box — 503: о туннеле мы при этом не знаем ничего,
// и записывать ему «down» означало бы соврать.
func (s *Server) handleCheckTunnel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	t, err := s.store.Tunnel(r.Context(), id)
	if err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}

	res := tunnelCheck{At: s.now().UTC()}
	switch {
	case !t.Enabled:
		// Выключенный туннель в конфиг не попадает (buildTunnels), спрашивать
		// о нём sing-box незачем — ответ был бы «не найден».
		res.Status = tunnelNotApplied
		res.Detail = "Туннель выключен, в конфиге sing-box его нет"
	case t.Source == store.SourcePool:
		proxies, err := s.proxies().Proxies(r.Context())
		if err != nil {
			writeError(w, s.log, http.StatusServiceUnavailable, codeNotReady, "Runtime observation unavailable")
			return
		}
		measured, available := s.checkOne(r.Context(), t, proxies)
		if available {
			res = measured
		} else {
			res.Status = tunnelUnknown
			res.Detail = "No pool history observation available"
		}
	default:
		delay, derr := s.clash.Delay(r.Context(), singbox.TunnelTag(t.ID))
		res.At = s.now().UTC()
		switch {
		case derr == nil:
			ms := int(delay.Milliseconds())
			res.LatencyMS = &ms
			// «Работает, но еле-еле» — отдельный ответ: на таком туннеле видео
			// не пойдёт, и увидеть это надо до того, как начнутся жалобы.
			if delay >= clash.SlowThreshold {
				res.Status = tunnelSlow
				res.Detail = "HTTPS round trip succeeded slowly; transport health remains unknown"
			} else {
				res.Status = tunnelUp
				res.Detail = "HTTPS round trip succeeded; transport health remains unknown"
			}
		case errors.Is(derr, clash.ErrNotFound):
			res.Status = tunnelNotApplied
			res.Detail = "Туннель не применён: его нет в работающем конфиге sing-box. " +
				"Нажмите «Применить»."
		case errors.Is(derr, clash.ErrProbeFailed):
			res.Status = tunnelUnknown
			res.Detail = "HTTPS probe failed; DNS, TCP, TLS and target causes are not distinguished. Transport health is unknown."
		case errors.Is(derr, clash.ErrUnavailable):
			// Прошлый результат в кэше остаётся как был: у него своя отметка
			// времени, и она честнее, чем «down» от неудавшейся проверки.
			s.log.Error("проверка туннеля", "туннель", t.ID, "ошибка", derr)
			writeError(w, s.log, http.StatusServiceUnavailable, codeNotReady,
				"Проверить не удалось: sing-box не отвечает по Clash API. "+
					"Похоже, он не запущен или конфигурация ещё не применена.")
			return
		default:
			s.log.Error("проверка туннеля", "туннель", t.ID, "ошибка", derr)
			writeError(w, s.log, http.StatusBadGateway, codeInternal,
				"Проверить не удалось: "+derr.Error())
			return
		}
	}

	res.Signals = s.observeSignals(r.Context(), res)
	if t.Source == store.SourcePool {
		res.Signals.HTTPS.Scope = "runtime_pool_history"
		res.Signals.HTTPS.Detail = "Existing pool history; not proof of every member"
	}
	s.checks.put(t.ID, res)
	writeJSON(w, s.log, http.StatusOK, tunnelCheckResponse{
		Signals:   res.Signals,
		ID:        t.ID,
		Status:    res.Status,
		LatencyMS: res.LatencyMS,
		LastCheck: res.At,
		Detail:    res.Detail,
	})
}

// signal describes only the evidence actually available from the controller.
type signal struct {
	State      string     `json:"state"`
	Scope      string     `json:"scope"`
	ObservedAt *time.Time `json:"observed_at"`
	Fresh      bool       `json:"fresh"`
	Detail     string     `json:"detail"`
}
type tunnelSignals struct {
	Runtime   signal `json:"runtime"`
	DNS       signal `json:"dns"`
	HTTPS     signal `json:"https"`
	Transport signal `json:"transport"`
}

func historicalSignals(at time.Time) *tunnelSignals {
	return &tunnelSignals{
		Runtime:   signal{State: "unknown", Scope: "controller"},
		DNS:       signal{State: "unknown", Scope: "runtime_resolver"},
		HTTPS:     signal{State: "unknown", Scope: "historical_round_trip", ObservedAt: &at, Detail: "Historical observation; no current runtime evidence"},
		Transport: signal{State: "unknown", Scope: "wireguard_transport", Detail: "No independent handshake or packet observation"},
	}
}

func (s *Server) observeSignals(ctx context.Context, res tunnelCheck) *tunnelSignals {
	out := historicalSignals(res.At)
	if res.Status != tunnelNotApplied {
		at := s.now().UTC()
		out.Runtime = signal{State: "reachable", Scope: "controller", ObservedAt: &at, Fresh: true}
	}
	out.HTTPS = signal{
		State: res.Status, Scope: "runtime_applied_exit_single_target_https", ObservedAt: &res.At, Fresh: s.now().Sub(res.At) >= 0 && s.now().Sub(res.At) <= 5*time.Minute,
		Detail: "One IP-literal HTTPS target; does not prove all protocols or clients",
	}
	if res.Status == tunnelNotApplied {
		return out
	}
	out.DNS = s.observeRuntimeDNS(ctx)
	return out
}

// sing-box 1.12.25 starts /dns/query with context.Background and a ten-second
// timeout. Canceling the controller request does not stop that runtime work.
const (
	runtimeDNSBudget = 10 * time.Second
	dnsHTTPBudget    = 2 * time.Second
)

type dnsObservationJob struct {
	done        chan struct{}
	observation signal
}

func (s *Server) observeRuntimeDNS(ctx context.Context) signal {
	unknown := signal{State: "unknown", Scope: "runtime_resolver", Detail: "Runtime resolver observation; not per-tunnel DNS"}
	if ctx.Err() != nil {
		return unknown
	}
	cache := s.checks
	cache.dnsMu.Lock()
	job := cache.dnsJob
	if job == nil {
		job = &dnsObservationJob{done: make(chan struct{})}
		cache.dnsJob = job
		go s.runDNSObservation(cache, job)
	}
	cache.dnsMu.Unlock()
	select {
	case <-ctx.Done():
		return unknown
	case <-job.done:
		return job.observation
	}
}

func (s *Server) runDNSObservation(cache *checkCache, job *dnsObservationJob) {
	started := time.Now()
	// Waiters cannot cancel or release this job's runtime capacity.
	ctx, cancel := context.WithTimeout(context.Background(), dnsHTTPBudget)
	addrs, err := s.clash.ResolveA(ctx, "www.gstatic.com")
	cancel()
	at := s.now().UTC()
	job.observation = signal{State: "unknown", Scope: "runtime_resolver", ObservedAt: &at, Fresh: true, Detail: "Runtime resolver observation; not per-tunnel DNS"}
	switch {
	case err == nil && len(addrs) > 0:
		job.observation.State = "answered"
	case errors.Is(err, clash.ErrResolveFailed):
		var resolveErr *clash.ResolveError
		if errors.As(err, &resolveErr) && resolveErr.Refused() {
			job.observation.State = "rejected"
		} else {
			job.observation.State = "failed"
		}
	}
	close(job.done)
	// Only a known resolver response establishes completion. On uncertain HTTP
	// failure retain the identity through the runtime budget plus HTTP dispatch
	// margin: sing-box can start its background timer near the HTTP deadline.
	var resolveErr *clash.ResolveError
	if err != nil && !errors.As(err, &resolveErr) {
		if remaining := runtimeDNSBudget + dnsHTTPBudget - time.Since(started); remaining > 0 {
			time.Sleep(remaining)
		}
	}
	cache.dnsMu.Lock()
	if cache.dnsJob == job {
		cache.dnsJob = nil
	}
	cache.dnsMu.Unlock()
}
