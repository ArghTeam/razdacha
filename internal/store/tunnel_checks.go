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
	// Notified — статус, о котором уже сообщили наружу. Пустой означает, что не
	// сообщали ни разу: это не то же самое, что «сообщали про up».
	Notified string `json:"notified"`
}

// SaveTunnelCheck записывает результат проверки одного туннеля.
//
// Запись идёт по внешнему ключу на tunnels, поэтому проверка туннеля, удалённого
// между опросом и записью, отваливается ошибкой, а не заводит запись-сироту.
func (s *Store) SaveTunnelCheck(ctx context.Context, tunnelID, status string, at time.Time) error {
	if tunnelID == "" || status == "" {
		return fmt.Errorf("%w: проверка туннеля без идентификатора или статуса", ErrInvalid)
	}
	// notified_status не трогается: о чём сообщили — отдельное знание, и
	// очередная проверка не должна его сбрасывать.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tunnel_checks (tunnel_id, status, checked_at) VALUES (?, ?, ?)
		 ON CONFLICT(tunnel_id) DO UPDATE SET
		   status = excluded.status, checked_at = excluded.checked_at`,
		tunnelID, status, at.UTC().Unix())
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
		`SELECT tunnel_id, status, checked_at, notified_status FROM tunnel_checks`)
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
			notified string
		)
		if err := rows.Scan(&id, &status, &at, &notified); err != nil {
			return nil, fmt.Errorf("чтение проверок туннелей: %w", err)
		}
		out[id] = TunnelCheck{
			Status: status, CheckedAt: time.Unix(at, 0).UTC(), Notified: notified,
		}
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
