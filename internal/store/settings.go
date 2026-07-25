package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// Settings — единственная запись настроек. В БД лежит как key/value, чтобы добавление
// поля не требовало миграции; отсутствующие ключи берутся из [DefaultSettings].
type Settings struct {
	WGListenPort       int           `json:"wg_listen_port"`
	WGPool             string        `json:"wg_pool"`
	WGServerAddress    string        `json:"wg_server_address"`
	EndpointHost       string        `json:"endpoint_host"`
	ClientMTU          int           `json:"client_mtu"`
	DNSUpstream        string        `json:"dns_upstream"`
	DNSType            string        `json:"dns_type"`
	WANInterface       string        `json:"wan_interface"`
	ListUpdateInterval time.Duration `json:"list_update_interval"`
	LogLevel           string        `json:"log_level"`
}

// DefaultSettings — дефолты из docs/02-data-model.md. MTU 1280 — не «по умолчанию
// системы», а требование ADR 0004; пустые endpoint_host и wan_interface означают
// автодетект на слое netstack.
func DefaultSettings() Settings {
	return Settings{
		WGListenPort:       51820,
		WGPool:             "10.8.0.0/24",
		WGServerAddress:    "10.8.0.1",
		EndpointHost:       "",
		ClientMTU:          1280,
		DNSUpstream:        "1.1.1.1",
		DNSType:            "udp",
		WANInterface:       "",
		ListUpdateInterval: 24 * time.Hour,
		LogLevel:           "warn",
	}
}

// Ключи в таблице settings. Переименование ключа — миграция, а не правка константы.
const (
	keyWGListenPort       = "wg_listen_port"
	keyWGPool             = "wg_pool"
	keyWGServerAddress    = "wg_server_address"
	keyEndpointHost       = "endpoint_host"
	keyClientMTU          = "client_mtu"
	keyDNSUpstream        = "dns_upstream"
	keyDNSType            = "dns_type"
	keyWANInterface       = "wan_interface"
	keyListUpdateInterval = "list_update_interval"
	keyLogLevel           = "log_level"
)

// Settings читает настройки, подставляя дефолты вместо отсутствующих ключей.
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	return settings(ctx, s.db)
}

func settings(ctx context.Context, q querier) (Settings, error) {
	rows, err := q.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return Settings{}, fmt.Errorf("чтение настроек: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := DefaultSettings()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return Settings{}, fmt.Errorf("чтение настроек: %w", err)
		}
		if err := out.set(key, value); err != nil {
			return Settings{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return Settings{}, fmt.Errorf("чтение настроек: %w", err)
	}
	return out, nil
}

// SaveSettings перезаписывает все настройки целиком.
func (s *Store) SaveSettings(ctx context.Context, v Settings) error {
	if err := v.validate(); err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		for key, value := range v.values() {
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

// values раскладывает настройки по ключам таблицы.
func (v Settings) values() map[string]string {
	return map[string]string{
		keyWGListenPort:       strconv.Itoa(v.WGListenPort),
		keyWGPool:             v.WGPool,
		keyWGServerAddress:    v.WGServerAddress,
		keyEndpointHost:       v.EndpointHost,
		keyClientMTU:          strconv.Itoa(v.ClientMTU),
		keyDNSUpstream:        v.DNSUpstream,
		keyDNSType:            v.DNSType,
		keyWANInterface:       v.WANInterface,
		keyListUpdateInterval: strconv.FormatInt(int64(v.ListUpdateInterval/time.Second), 10),
		keyLogLevel:           v.LogLevel,
	}
}

// set применяет одно значение из БД. Неизвестные ключи игнорируются: откат на
// предыдущую версию демона не должен ронять чтение настроек.
func (v *Settings) set(key, value string) error {
	number := func(dst *int) error {
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%w: настройка %s = %q не число", ErrInvalid, key, value)
		}
		*dst = n
		return nil
	}

	switch key {
	case keyWGListenPort:
		return number(&v.WGListenPort)
	case keyClientMTU:
		return number(&v.ClientMTU)
	case keyListUpdateInterval:
		var seconds int
		if err := number(&seconds); err != nil {
			return err
		}
		v.ListUpdateInterval = time.Duration(seconds) * time.Second
	case keyWGPool:
		v.WGPool = value
	case keyWGServerAddress:
		v.WGServerAddress = value
	case keyEndpointHost:
		v.EndpointHost = value
	case keyDNSUpstream:
		v.DNSUpstream = value
	case keyDNSType:
		v.DNSType = value
	case keyWANInterface:
		v.WANInterface = value
	case keyLogLevel:
		v.LogLevel = value
	}
	return nil
}

// validate проверяет настройки до записи.
func (v Settings) validate() error {
	if v.WGListenPort < 1 || v.WGListenPort > 65535 {
		return fmt.Errorf("%w: порт WireGuard %d вне диапазона 1–65535", ErrInvalid, v.WGListenPort)
	}
	if v.ClientMTU < 576 || v.ClientMTU > 1500 {
		return fmt.Errorf("%w: MTU %d вне диапазона 576–1500", ErrInvalid, v.ClientMTU)
	}
	if v.WGPool == "" || v.WGServerAddress == "" {
		return fmt.Errorf("%w: пул адресов и адрес сервера обязательны", ErrInvalid)
	}
	if v.DNSUpstream == "" {
		return fmt.Errorf("%w: апстрим DNS обязателен", ErrInvalid)
	}
	switch v.DNSType {
	case "udp", "dot", "doh":
	default:
		return fmt.Errorf("%w: неизвестный тип DNS %q", ErrInvalid, v.DNSType)
	}
	switch v.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("%w: неизвестный уровень логов %q", ErrInvalid, v.LogLevel)
	}
	if v.ListUpdateInterval <= 0 {
		return fmt.Errorf("%w: интервал обновления списков должен быть положительным", ErrInvalid)
	}
	return nil
}
