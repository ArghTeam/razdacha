package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// keyPasswordHash — ключ в таблице settings, под которым лежит закодированный
// хеш пароля панели вместе с параметрами (формат PHC, см. internal/api).
//
// Ключ намеренно не входит в [Settings]: `SaveSettings` переписывает только свои
// ключи, а `Settings` игнорирует чужие, поэтому сохранение настроек из UI не
// затирает пароль и не выносит его хеш в JSON-ответ `GET /api/settings`.
const keyPasswordHash = "auth_password_hash"

// ErrNoPassword — пароль панели не задан. Публичный режим этого не допускает
// (ADR 0009): пустой и дефолтный пароль невозможны, демон не стартует.
var ErrNoPassword = errors.New("пароль панели не задан")

// Session — выданная сессия панели. В БД лежит хеш токена, а не сам токен:
// доступ к файлу БД не должен давать готовые cookie.
type Session struct {
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// PasswordHash возвращает закодированный хеш пароля панели.
// Если пароль не задан, ошибка оборачивает [ErrNoPassword].
func (s *Store) PasswordHash(ctx context.Context) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, keyPasswordHash).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && value == "") {
		return "", ErrNoPassword
	}
	if err != nil {
		return "", fmt.Errorf("чтение пароля панели: %w", err)
	}
	return value, nil
}

// SetPasswordHash записывает закодированный хеш пароля панели. Хеширование —
// дело слоя api: хранение не выбирает алгоритм и не видит пароля в открытом виде.
//
// Все выданные сессии при этом сбрасываются: смена пароля обязана выкидывать
// того, кто увёл cookie.
func (s *Store) SetPasswordHash(ctx context.Context, encoded string) error {
	if encoded == "" {
		return fmt.Errorf("%w: пустой хеш пароля", ErrInvalid)
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			keyPasswordHash, encoded); err != nil {
			return fmt.Errorf("запись пароля панели: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
			return fmt.Errorf("сброс сессий: %w", err)
		}
		return nil
	})
}

// CreateSession сохраняет сессию по хешу её токена.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	if sess.TokenHash == "" {
		return fmt.Errorf("%w: пустой хеш токена сессии", ErrInvalid)
	}
	if !sess.ExpiresAt.After(sess.CreatedAt) {
		return fmt.Errorf("%w: срок сессии в прошлом", ErrInvalid)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, created_at, expires_at) VALUES (?, ?, ?)`,
		sess.TokenHash, sess.CreatedAt.Unix(), sess.ExpiresAt.Unix()); err != nil {
		return fmt.Errorf("создание сессии: %w", err)
	}
	return nil
}

// Session возвращает сессию по хешу токена. Истёкшие сессии не отдаются:
// проверка срока живёт здесь, чтобы её нельзя было забыть у вызывающего.
func (s *Store) Session(ctx context.Context, tokenHash string, now time.Time) (Session, error) {
	var createdAt, expiresAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT created_at, expires_at FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, now.Unix()).Scan(&createdAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("сессия: %w", ErrNotFound)
	}
	if err != nil {
		return Session{}, fmt.Errorf("чтение сессии: %w", err)
	}
	return Session{
		TokenHash: tokenHash,
		CreatedAt: time.Unix(createdAt, 0).UTC(),
		ExpiresAt: time.Unix(expiresAt, 0).UTC(),
	}, nil
}

// ExtendSession продлевает срок сессии.
func (s *Store) ExtendSession(ctx context.Context, tokenHash string, expiresAt time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`,
		expiresAt.Unix(), tokenHash); err != nil {
		return fmt.Errorf("продление сессии: %w", err)
	}
	return nil
}

// DeleteSession удаляет сессию. Отсутствие записи ошибкой не считается: выход
// по протухшей cookie для пользователя выглядит как обычный выход.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("удаление сессии: %w", err)
	}
	return nil
}

// DeleteExpiredSessions чистит истёкшие сессии. Вызывается по расписанию:
// без уборки таблица растёт на каждый вход и не уменьшается никогда.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("уборка истёкших сессий: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("уборка истёкших сессий: %w", err)
	}
	return n, nil
}
