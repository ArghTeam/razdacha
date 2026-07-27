package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// Параметры подтверждения и глушения. Вынесены в константы, а не в настройки:
// это не вкусовые величины, а цена ошибки в обе стороны, и объяснить
// пользователю, что выбрать, было бы нечем.
const (
	// confirmChecks — сколько проверок подряд держит состояние, прежде чем о нём
	// сообщают. При интервале в две минуты падение доезжает за шесть — для
	// VPN-шлюза это ничто, а флап короче не порождает ни одного сообщения.
	confirmChecks = 3
	// unstableWindow и unstableChanges — сколько подтверждённых смен за какое
	// окно считать «туннель нестабилен».
	unstableWindow  = time.Hour
	unstableChanges = 4
	// unstableMute — насколько замолкаем по нестабильному туннелю после того,
	// как о нём сказали.
	unstableMute = time.Hour
)

// Состояния, о которых вообще сообщают. `slow` от `up` не отличается: сообщение
// «туннель замедлился» приходило бы на каждый скачок задержки. `not_applied` —
// состояние панели, а не сети (так выглядит туннель до «Применить»), поэтому
// событием не считается и сбрасывает набранное подтверждение.
const (
	eventUp   = "up"
	eventDown = "down"
)

// eventFor переводит статус проверки в состояние, о котором можно сообщать.
// Пустая строка означает «сказать нечего».
func eventFor(status string) string {
	switch status {
	case tunnelUp, tunnelSlow:
		return eventUp
	case tunnelDown:
		return eventDown
	default:
		return ""
	}
}

// tunnelEvents — подтверждение переходов и подсчёт нестабильности.
//
// Живёт в памяти намеренно: после перезапуска демона туннель обязан снова
// набрать подтверждение, и это честнее, чем объявить переход по одному
// наблюдению. То, о чём уже сообщили, наоборот, лежит в БД — иначе перезапуск
// переобъявлял бы падение, про которое владельцу уже написали.
type tunnelEvents struct {
	mu    sync.Mutex
	state map[string]*tunnelEventState
}

type tunnelEventState struct {
	candidate string
	streak    int
	changes   []time.Time
	mutedTill time.Time
}

func newTunnelEvents() *tunnelEvents {
	return &tunnelEvents{state: make(map[string]*tunnelEventState)}
}

// verdict — что делать по итогам одной проверки.
type verdict struct {
	// Confirmed — подтверждённое состояние, пустое означает «ещё не набрано».
	Confirmed string
	// Message — что отправить; пустое означает «молчим».
	Message string
}

// observe принимает результат очередной проверки и решает, наступило ли событие.
//
// notified — состояние, о котором уже сообщали; пустое означает, что не
// сообщали ни разу.
func (e *tunnelEvents) observe(
	id, name, status, notified string, now time.Time,
) verdict {
	e.mu.Lock()
	defer e.mu.Unlock()

	st := e.state[id]
	if st == nil {
		st = &tunnelEventState{}
		e.state[id] = st
	}

	got := eventFor(status)
	if got == "" {
		// Туннель выпал из конфига или ещё не применён: набранное подтверждение
		// больше ни о чём не говорит.
		st.candidate, st.streak = "", 0
		return verdict{}
	}

	if got == st.candidate {
		st.streak++
	} else {
		st.candidate, st.streak = got, 1
	}
	if st.streak < confirmChecks {
		return verdict{}
	}
	if got == notified {
		return verdict{Confirmed: got}
	}

	// Первое наблюдение сообщается, только если оно плохое: «туннель работает»
	// сразу после настройки — не новость, а «туннель не отвечает» — новость.
	if notified == "" && got == eventUp {
		return verdict{Confirmed: got}
	}

	st.changes = append(pruneBefore(st.changes, now.Add(-unstableWindow)), now)
	if len(st.changes) > unstableChanges {
		if now.Before(st.mutedTill) {
			return verdict{Confirmed: got}
		}
		st.mutedTill = now.Add(unstableMute)
		return verdict{
			Confirmed: got,
			Message: fmt.Sprintf(
				"Туннель «%s» нестабилен: %d смен состояния за час. "+
					"Сообщения по нему приглушены на час.",
				name, len(st.changes)),
		}
	}

	return verdict{Confirmed: got, Message: eventMessage(name, got)}
}

// forget убирает состояние туннелей, которых больше нет.
func (e *tunnelEvents) forget(alive map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id := range e.state {
		if !alive[id] {
			delete(e.state, id)
		}
	}
}

// pruneBefore выбрасывает отметки старше границы окна.
func pruneBefore(times []time.Time, edge time.Time) []time.Time {
	out := times[:0]
	for _, t := range times {
		if t.After(edge) {
			out = append(out, t)
		}
	}
	return out
}

func eventMessage(name, state string) string {
	if state == eventDown {
		return fmt.Sprintf("Туннель «%s» не отвечает.", name)
	}
	return fmt.Sprintf("Туннель «%s» снова работает.", name)
}

// announce отправляет сообщение, если оповещения настроены и включены.
//
// Отказ отправки не роняет прогон проверок: расписание важнее, чем доставка
// одного сообщения, а причина уходит в лог.
func (s *Server) announce(ctx context.Context, cfg store.NotifyConfig, text string) {
	if text == "" || !cfg.Ready() {
		return
	}
	if err := s.notifier(cfg).Send(ctx, text); err != nil {
		s.log.Warn("оповещение не отправлено", "ошибка", err)
	}
}
