package api

import (
	"context"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// backupTick — как часто расписание смотрит, не пора ли отправлять. Отдельный
// от интервала отправки: интервал живёт часами, а изменение настройки в панели
// должно применяться раньше, чем через сутки.
const backupTick = time.Minute

// backupRetry — пауза после неудачной отправки. Ждать полный интервал незачем:
// чаще всего отправка падает из-за сети, которая возвращается раньше.
const backupRetry = 15 * time.Minute

// watchBackup — расписание отправки копии состояния в телеграм (ADR 0016).
//
// Выключено по умолчанию и не включается без парольной фразы: наружу копия
// уходит только зашифрованной, потому что в ней приватные ключи всех пиров.
//
// Время последней отправки лежит в БД, а не в памяти: иначе перезапуск демона
// означал бы новую копию в чате, а перезапуск раз в час — копию каждый час.
func (s *Server) watchBackup(ctx context.Context) {
	ticker := time.NewTicker(backupTick)
	defer ticker.Stop()
	// Время последней попытки живёт в памяти, а не в БД: оно нужно только
	// затем, чтобы неудача не повторялась каждую минуту, и после перезапуска
	// демона попробовать снова — правильное поведение.
	var lastTry time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.backupRound(ctx, &lastTry)
	}
}

// backupRound — один взгляд на расписание. Ничего не должен — значит ничего и
// не делает: наружу демон ходит только тогда, когда его об этом попросили.
func (s *Server) backupRound(ctx context.Context, lastTry *time.Time) {
	cfg, err := s.store.BackupConfig(ctx)
	if err != nil {
		s.log.Error("чтение расписания копии", "ошибка", err)
		return
	}
	if !cfg.Ready() {
		return
	}
	notifyCfg, err := s.store.NotifyConfig(ctx)
	if err != nil {
		s.log.Error("чтение настроек оповещений", "ошибка", err)
		return
	}
	if notifyCfg.Token == "" || notifyCfg.ChatID == "" {
		// Включить расписание без чата панель не даёт, но настройки телеграма
		// правятся отдельной ручкой и могут опустеть после. Молчать об этом
		// нельзя: пользователь считает, что копии уходят.
		s.log.Warn("копия состояния не отправлена: бот телеграма не настроен")
		return
	}
	now := s.now()
	if !backupDue(cfg, now) {
		return
	}
	// Неудача не двигает отметку в БД, поэтому без этой паузы отправка
	// повторялась бы каждую минуту, пока сеть не вернётся.
	if !lastTry.IsZero() && now.Sub(*lastTry) < backupRetry {
		return
	}
	*lastTry = now

	if err := s.sendBackup(ctx, cfg, notifyCfg); err != nil {
		// Текст отказа уже записан в БД и виден в панели; сюда он попадает без
		// фразы и без содержимого копии — их в ошибках транспорта нет.
		s.log.Warn("копия состояния не отправлена", "ошибка", err, "повтор_через", backupRetry)
		return
	}
	s.log.Info("копия состояния отправлена в телеграм")
}

// backupDue отвечает, пора ли отправлять.
//
// Никогда не отправляли — пора: иначе включённое расписание молчало бы первый
// интервал, и владелец решил бы, что оно не работает. Неудачная попытка времени
// не двигает, поэтому повтор идёт через [backupRetry] от неё, а не через сутки.
func backupDue(cfg store.BackupConfig, now time.Time) bool {
	if cfg.LastSentAt.IsZero() {
		return true
	}
	return !now.Before(cfg.LastSentAt.Add(cfg.Interval))
}
