package api

import (
	"context"
	"errors"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// watchRetry — пауза после неудачного прогона. Ждать полный интервал незачем:
// чаще всего прогон падает потому, что sing-box ещё поднимается.
const watchRetry = 15 * time.Second

// watchTunnels — расписание проверок состояния туннелей.
//
// Без него экран туннелей встречает пользователя пустыми полями: до этой задачи
// кэш заполнялся только нажатием «Проверить» и терялся при перезапуске.
//
// Первым делом поднимает сохранённые проверки, чтобы окно между стартом демона и
// первым прогоном не выглядело как «никогда не проверялось».
func (s *Server) watchTunnels(ctx context.Context) {
	s.loadChecks(ctx)

	for {
		wait := s.checkInterval(ctx)
		if err := s.refreshChecks(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Warn("проверка туннелей не удалась, повтор позже",
				"повтор_через", watchRetry, "ошибка", err)
			if watchRetry < wait {
				wait = watchRetry
			}
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// checkInterval читает интервал из настроек на каждом круге: изменение в панели
// применяется со следующего срабатывания, отдельного сигнала для этого не нужно.
func (s *Server) checkInterval(ctx context.Context) time.Duration {
	v, err := s.store.Settings(ctx)
	if err != nil {
		s.log.Error("интервал проверки туннелей", "ошибка", err)
		return store.DefaultSettings().TunnelCheckInterval
	}
	if v.TunnelCheckInterval <= 0 {
		return store.DefaultSettings().TunnelCheckInterval
	}
	return v.TunnelCheckInterval
}

// loadChecks поднимает сохранённые проверки в кэш.
//
// Задержка не восстанавливается: в БД её нет намеренно (ADR 0011), и показать
// старую цифру означало бы соврать точным числом. Статус со своей отметкой
// времени — честен, UI показывает время рядом.
func (s *Server) loadChecks(ctx context.Context) {
	saved, err := s.store.TunnelChecks(ctx)
	if err != nil {
		s.log.Error("чтение сохранённых проверок туннелей", "ошибка", err)
		return
	}
	for id, c := range saved {
		s.checks.put(id, tunnelCheck{Status: c.Status, At: c.CheckedAt})
	}
	if len(saved) > 0 {
		s.log.Debug("подняты сохранённые проверки туннелей", "количество", len(saved))
	}
}

// refreshChecks — один прогон по всем туннелям.
//
// Состояние снимается одним запросом `/proxies`, а не пробой каждого туннеля:
// у пула за тегом стоит группа `urltest` из сотни серверов, и активная проба
// означала бы прогон по всей сотне на каждом круге. Группу sing-box проверяет
// сам — это та же проверка, по которой он переключает трафик, и читать надо
// именно её (инвариант слоя api). Обычный outbound никто не проверяет, поэтому
// его — и только его — пробиваем сами.
func (s *Server) refreshChecks(ctx context.Context) error {
	tunnels, err := s.store.Tunnels(ctx)
	if err != nil {
		return err
	}

	proxies, err := s.proxies().Proxies(ctx)
	if err != nil {
		// sing-box не отвечает — о туннелях мы не знаем ничего. Прежние
		// результаты остаются как были: у них своя отметка времени, и она
		// честнее, чем «down» от несостоявшейся проверки.
		return err
	}

	alive := make(map[string]bool, len(tunnels))
	for _, t := range tunnels {
		alive[t.ID] = true
		if ctx.Err() != nil {
			return ctx.Err()
		}

		res, ok := s.checkOne(ctx, t, proxies)
		if !ok {
			continue
		}
		s.checks.put(t.ID, res)
		if err := s.store.SaveTunnelCheck(ctx, t.ID, res.Status, res.At); err != nil {
			s.log.Error("запись проверки туннеля", "туннель", t.ID, "ошибка", err)
		}
	}
	s.checks.keep(alive)
	return nil
}

// checkOne определяет состояние одного туннеля. Второе значение false означает
// «сказать нечего» — результат в этом круге не обновляется.
func (s *Server) checkOne(
	ctx context.Context, t store.Tunnel, proxies map[string]clash.Proxy,
) (tunnelCheck, bool) {
	res := tunnelCheck{At: s.now().UTC()}

	if !t.Enabled {
		res.Status = tunnelNotApplied
		return res, true
	}

	p, inConfig := proxies[singbox.TunnelTag(t.ID)]
	if !inConfig {
		res.Status = tunnelNotApplied
		return res, true
	}

	// Группа (`urltest` пула) проверяется самим sing-box — берём его журнал.
	if len(p.All) > 0 {
		delay, measured := p.Latency()
		if !measured {
			// Журнал пуст: sing-box ещё не успел прогнать группу. Это не
			// «down», это «пока не знаем» — прежний результат честнее.
			return tunnelCheck{}, false
		}
		res.Status = statusFor(delay)
		ms := int(delay.Milliseconds())
		res.LatencyMS = &ms
		return res, true
	}

	// Обычный outbound: его никто не проверяет, кроме нас.
	delay, err := s.clash.Delay(ctx, singbox.TunnelTag(t.ID))
	switch {
	case err == nil:
		res.Status = statusFor(delay)
		ms := int(delay.Milliseconds())
		res.LatencyMS = &ms
	case errors.Is(err, clash.ErrNotFound):
		res.Status = tunnelNotApplied
	case errors.Is(err, clash.ErrProbeFailed):
		res.Status = tunnelDown
	default:
		// Сам sing-box не отвечает или ответил непонятным — записывать туннелю
		// «down» на этом основании было бы утверждением, которого мы не делали.
		s.log.Debug("проверка туннеля по расписанию", "туннель", t.ID, "ошибка", err)
		return tunnelCheck{}, false
	}
	return res, true
}

// statusFor переводит измеренную задержку в статус. Порог тот же, что у ручной
// проверки: расходиться им нельзя, пользователь видит их в одном столбце.
func statusFor(d time.Duration) string {
	if d >= clash.SlowThreshold {
		return tunnelSlow
	}
	return tunnelUp
}
