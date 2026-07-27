package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Ключи уведомлений в таблице settings.
//
// Как хеш пароля и установочные факты, они лежат в той же таблице, но вне
// [Settings]: иначе токен бота переписывался бы из `PATCH /api/settings` и
// уезжал бы обратно в `GET /api/settings`. Секрет из API не возвращается вовсе.
const (
	keyNotifyEnabled  = "notify_enabled"
	keyNotifyToken    = "notify_telegram_token"
	keyNotifyChatID   = "notify_telegram_chat_id"
	notifyEnabledTrue = "1"
)

// NotifyConfig — настройки оповещений. Транспорт один (телеграм), поэтому поля
// лежат плоско: интерфейс под второй транспорт до появления второго был бы
// догадкой о том, каким он будет.
type NotifyConfig struct {
	Enabled bool
	Token   string
	ChatID  string
}

// Ready отвечает, есть ли чем и куда отправлять. Включённые уведомления без
// токена или чата — не рабочая конфигурация, а незаконченная настройка.
func (c NotifyConfig) Ready() bool {
	return c.Enabled && c.Token != "" && c.ChatID != ""
}

// NotifyConfig читает настройки оповещений. Отсутствующие ключи означают
// «не настроено» — это не ошибка.
func (s *Store) NotifyConfig(ctx context.Context) (NotifyConfig, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value FROM settings WHERE key IN (?, ?, ?)`,
		keyNotifyEnabled, keyNotifyToken, keyNotifyChatID)
	if err != nil {
		return NotifyConfig{}, fmt.Errorf("чтение настроек оповещений: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out NotifyConfig
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return NotifyConfig{}, fmt.Errorf("чтение настроек оповещений: %w", err)
		}
		switch key {
		case keyNotifyEnabled:
			out.Enabled = value == notifyEnabledTrue
		case keyNotifyToken:
			out.Token = value
		case keyNotifyChatID:
			out.ChatID = value
		}
	}
	if err := rows.Err(); err != nil {
		return NotifyConfig{}, fmt.Errorf("чтение настроек оповещений: %w", err)
	}
	return out, nil
}

// SaveNotifyConfig записывает настройки оповещений целиком.
//
// Включить уведомления без токена или чата нельзя: такая настройка выглядит
// рабочей в интерфейсе и молча ничего не шлёт.
func (s *Store) SaveNotifyConfig(ctx context.Context, c NotifyConfig) error {
	c.Token = strings.TrimSpace(c.Token)
	c.ChatID = strings.TrimSpace(c.ChatID)
	if c.Enabled && (c.Token == "" || c.ChatID == "") {
		return fmt.Errorf("%w: для включения нужны токен бота и идентификатор чата", ErrInvalid)
	}

	enabled := ""
	if c.Enabled {
		enabled = notifyEnabledTrue
	}
	values := map[string]string{
		keyNotifyEnabled: enabled,
		keyNotifyToken:   c.Token,
		keyNotifyChatID:  c.ChatID,
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		for key, value := range values {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO settings (key, value) VALUES (?, ?)
				 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
				key, value); err != nil {
				return fmt.Errorf("запись настройки %s: %w", key, err)
			}
		}
		return nil
	})
}
