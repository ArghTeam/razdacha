package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// keyWGServerPrivate — ключ в таблице settings, под которым лежит приватный ключ
// сервера WireGuard в base64.
//
// Как и хеш пароля, он намеренно не входит в [Settings]: `SaveSettings` пишет
// только свои ключи, а `Settings` игнорирует чужие, поэтому сохранение настроек
// из панели не затирает ключ сервера и не выносит его в JSON-ответ
// `GET /api/settings`. Наружу уходит только публичная половина.
const keyWGServerPrivate = "wg_server_private_key"

// ServerPrivateKey возвращает приватный ключ сервера WireGuard.
// Пустая строка означает, что ключ ещё не заведён — интерфейс ни разу не поднимали.
func (s *Store) ServerPrivateKey(ctx context.Context) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, keyWGServerPrivate).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("чтение приватного ключа сервера: %w", err)
	}
	return value, nil
}

// SetServerPrivateKey сохраняет приватный ключ сервера WireGuard. Генерация —
// дело слоя netstack: хранение не выбирает алгоритм и не проверяет формат ключа.
func (s *Store) SetServerPrivateKey(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("%w: пустой приватный ключ сервера", ErrInvalid)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		keyWGServerPrivate, key); err != nil {
		return fmt.Errorf("запись приватного ключа сервера: %w", err)
	}
	return nil
}
