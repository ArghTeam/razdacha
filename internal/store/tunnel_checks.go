package store

import (
	"context"
	"fmt"
	"time"
)

// TunnelCheck — сохранённое наблюдение о туннеле: чем он был в последний раз,
// когда его проверяли.
//
// Задержки здесь нет намеренно. Она живёт минуты, а после перезапуска sing-box
// не значит уже ничего — показать её вместе со старой отметкой времени значило
// бы соврать точной цифрой. Статус же остаётся фактом: «был down в 06:40» —
// полезное знание, пока рядом стоит время (ADR 0011).
type TunnelCheck struct {
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checked_at"`
	// OKAt — когда туннель отвечал в последний раз. Нулевое время означает
	// «удачных проверок не записано»: из Status и CheckedAt это время не
	// выводится, у молчащего туннеля CheckedAt — время свежей неудачи.
	OKAt time.Time `json:"ok_at"`
	// Notified — статус, о котором уже сообщили наружу. Пустой означает, что не
	// сообщали ни разу: это не то же самое, что «сообщали про up».
	Notified string `json:"notified"`
}

// SaveTunnelCheck записывает результат проверки одного туннеля.
//
// okAt — время удачного ответа; нулевое означает «в этот раз не дозвались», и
// прежняя отметка остаётся нетронутой. Что считать удачей, решает вызывающий:
// статусы здесь лежат строками, и толковать их хранилище не должно.
//
// Запись идёт по внешнему ключу на tunnels, поэтому проверка туннеля, удалённого
// между опросом и записью, отваливается ошибкой, а не заводит запись-сироту.
func (s *Store) SaveTunnelCheck(
	ctx context.Context, tunnelID, status string, at, okAt time.Time,
) error {
	if tunnelID == "" || status == "" {
		return fmt.Errorf("%w: проверка туннеля без идентификатора или статуса", ErrInvalid)
	}
	var ok int64
	if !okAt.IsZero() {
		ok = okAt.UTC().Unix()
	}
	// notified_status не трогается: о чём сообщили — отдельное знание, и
	// очередная проверка не должна его сбрасывать.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tunnel_checks (tunnel_id, status, checked_at, ok_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(tunnel_id) DO UPDATE SET
		   status = excluded.status, checked_at = excluded.checked_at,
		   ok_at = CASE WHEN excluded.ok_at > 0 THEN excluded.ok_at ELSE tunnel_checks.ok_at END`,
		tunnelID, status, at.UTC().Unix(), ok)
	if err != nil {
		return fmt.Errorf("запись проверки туннеля %s: %w", tunnelID, err)
	}
	return nil
}

// TunnelChecks отдаёт последние проверки по всем туннелям, ключ — идентификатор
// туннеля. Отсутствие ключа означает «не проверялся»: пустой записи для такого
// туннеля не заводится, иначе «не проверялся» стало бы неотличимо от статуса.
func (s *Store) TunnelChecks(ctx context.Context) (map[string]TunnelCheck, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tunnel_id, status, checked_at, ok_at, notified_status FROM tunnel_checks`)
	if err != nil {
		return nil, fmt.Errorf("чтение проверок туннелей: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]TunnelCheck)
	for rows.Next() {
		var (
			id       string
			status   string
			at       int64
			okAt     int64
			notified string
		)
		if err := rows.Scan(&id, &status, &at, &okAt, &notified); err != nil {
			return nil, fmt.Errorf("чтение проверок туннелей: %w", err)
		}
		c := TunnelCheck{
			Status: status, CheckedAt: time.Unix(at, 0).UTC(), Notified: notified,
		}
		// Нулевая отметка остаётся нулевым временем, а не 1970 годом: «удачных
		// проверок не записано» обязано отличаться от даты.
		if okAt > 0 {
			c.OKAt = time.Unix(okAt, 0).UTC()
		}
		out[id] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("чтение проверок туннелей: %w", err)
	}
	return out, nil
}

// SetNotifiedStatus запоминает, о каком состоянии туннеля уже сообщили наружу.
//
// Пишется и при выключенных оповещениях: иначе включение рассылало бы разом всё,
// что накопилось, хотя пользователь это уже видел в панели.
func (s *Store) SetNotifiedStatus(ctx context.Context, tunnelID, status string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tunnel_checks SET notified_status = ? WHERE tunnel_id = ?`,
		status, tunnelID)
	if err != nil {
		return fmt.Errorf("запись сообщённого статуса %s: %w", tunnelID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("%w: проверок туннеля %s ещё нет", ErrNotFound, tunnelID)
	}
	return nil
}
