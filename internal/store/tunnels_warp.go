package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// WARPRegistration — устройство, которое Cloudflare завёл по запросу демона.
//
// Живёт рядом с туннелем, но **не внутри** [Tunnel]: тот целиком уезжает в ответ
// API и на экран пользователю, а токен — секрет. По той же причине вне [Settings]
// лежат токен телеграма и хеш пароля.
//
// Пара нужна ровно для одного: снять устройство у Cloudflare, когда туннель
// удаляют. Ни конфиг, ни маршрутизация её не читают, в [Snapshot] она не входит.
type WARPRegistration struct {
	// DeviceID — идентификатор устройства, он же путь `/reg/{device_id}`.
	DeviceID string
	// AccessToken — токен устройства для заголовка `Authorization: Bearer`.
	AccessToken string
	// CreatedAt — когда устройство зарегистрировали.
	CreatedAt time.Time
}

// CreateWARPTunnel заводит туннель WARP вместе с его регистрацией, одной
// транзакцией.
//
// Двумя вызовами это делать нельзя: упавшая вторая запись оставила бы туннель,
// устройство которого уже не снять, — ровно та потеря, ради которой пара и
// хранится (issue #107).
func (s *Store) CreateWARPTunnel(ctx context.Context, t Tunnel, reg WARPRegistration) (Tunnel, error) {
	if t.Source != SourceWARP {
		return Tunnel{}, fmt.Errorf("%w: регистрация Cloudflare бывает только у туннеля WARP, а не %q",
			ErrInvalid, t.Source)
	}
	if reg.DeviceID == "" || reg.AccessToken == "" {
		return Tunnel{}, fmt.Errorf("%w: у регистрации WARP пустой идентификатор устройства или токен",
			ErrInvalid)
	}
	if err := t.validate(); err != nil {
		return Tunnel{}, err
	}
	if t.ID == "" {
		t.ID = newID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	if reg.CreatedAt.IsZero() {
		reg.CreatedAt = t.CreatedAt
	}

	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, insertTunnelSQL, tunnelArgs(t, "[]")...); err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: туннель с именем %q уже есть", ErrInvalid, t.Name)
			}
			return fmt.Errorf("добавление туннеля %q: %w", t.Name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO warp_registrations (tunnel_id, device_id, access_token, created_at)
			 VALUES (?, ?, ?, ?)`,
			t.ID, reg.DeviceID, reg.AccessToken, reg.CreatedAt.Unix()); err != nil {
			return fmt.Errorf("запись регистрации WARP для туннеля %q: %w", t.Name, err)
		}
		return nil
	})
	if err != nil {
		return Tunnel{}, err
	}
	return t, nil
}

// WARPRegistration отдаёт регистрацию туннеля. Второе значение — есть ли она
// вообще: у WARP, вставленного руками `.conf`, и у заведённого до появления
// таблицы её нет, и это не ошибка, а «снимать нечего».
func (s *Store) WARPRegistration(ctx context.Context, tunnelID string) (WARPRegistration, bool, error) {
	var (
		reg       WARPRegistration
		createdAt int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT device_id, access_token, created_at FROM warp_registrations WHERE tunnel_id = ?`,
		tunnelID).Scan(&reg.DeviceID, &reg.AccessToken, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return WARPRegistration{}, false, nil
	case err != nil:
		return WARPRegistration{}, false, fmt.Errorf("чтение регистрации WARP туннеля %s: %w", tunnelID, err)
	}
	reg.CreatedAt = time.Unix(createdAt, 0).UTC()
	return reg, true, nil
}
