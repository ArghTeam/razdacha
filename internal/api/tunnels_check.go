package api

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/singbox"
)

// Статусы туннеля из docs/05-api.md и мока прототипа (ui/prototype/mock-data.js).
// Отдельные от статусов диагностики: там «ok/warn/error» про проверку, здесь —
// про сам туннель.
const (
	tunnelUp         = "up"          // задержка измерена
	tunnelSlow       = "slow"        // отвечает, но дольше clash.SlowThreshold
	tunnelDown       = "down"        // sing-box проверил и не достучался
	tunnelNotApplied = "not_applied" // тега нет в работающем конфиге
)

// tunnelCheck — результат последней проверки одного туннеля.
type tunnelCheck struct {
	Status    string
	LatencyMS *int
	At        time.Time
	Detail    string
}

// checkCache — результаты проверок, сделанных с момента запуска демона.
//
// В БД они не пишутся намеренно: задержка живёт минуты и после перезапуска
// демона (а с ним и sing-box) уже ничего не значит. Пустой кэш означает
// «не проверялось», и производные поля туннеля остаются null — ноль в latency
// был бы измеренным нулём, а «down» — утверждением о непроверенном туннеле.
type checkCache struct {
	mu   sync.Mutex
	last map[string]tunnelCheck
}

func newCheckCache() *checkCache {
	return &checkCache{last: make(map[string]tunnelCheck)}
}

func (c *checkCache) put(id string, res tunnelCheck) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	t.LatencyMS = res.LatencyMS
	t.LastCheck = &at
	return t
}

// tunnelCheckResponse — тело `POST /api/tunnels/{id}/check`.
//
// Поля те же, что у туннеля в списке, плюс объяснение: «down» без причины
// пользователю сказать нечего, а причина у sing-box одна на всю проверку.
type tunnelCheckResponse struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	LatencyMS *int      `json:"latency_ms"`
	LastCheck time.Time `json:"last_check"`
	Detail    string    `json:"detail"`
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
	default:
		delay, derr := s.clash.Delay(r.Context(), singbox.TunnelTag(t.ID))
		switch {
		case derr == nil:
			ms := int(delay.Milliseconds())
			res.LatencyMS = &ms
			// «Работает, но еле-еле» — отдельный ответ: на таком туннеле видео
			// не пойдёт, и увидеть это надо до того, как начнутся жалобы.
			if delay >= clash.SlowThreshold {
				res.Status = tunnelSlow
				res.Detail = "Туннель отвечает медленно"
			} else {
				res.Status = tunnelUp
				res.Detail = "Туннель отвечает"
			}
		case errors.Is(derr, clash.ErrNotFound):
			res.Status = tunnelNotApplied
			res.Detail = "Туннель не применён: его нет в работающем конфиге sing-box. " +
				"Нажмите «Применить»."
		case errors.Is(derr, clash.ErrProbeFailed):
			res.Status = tunnelDown
			res.Detail = "Туннель не отвечает: " + derr.Error()
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

	s.checks.put(t.ID, res)
	writeJSON(w, s.log, http.StatusOK, tunnelCheckResponse{
		ID:        t.ID,
		Status:    res.Status,
		LatencyMS: res.LatencyMS,
		LastCheck: res.At,
		Detail:    res.Detail,
	})
}
