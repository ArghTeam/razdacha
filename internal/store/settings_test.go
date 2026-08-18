package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Пустая БД отдаёт дефолты, а не нули: MTU 1280 — требование ADR 0004.
func TestSettingsDefaults(t *testing.T) {
	s := open(t)

	got, err := s.Settings(context.Background())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	want := DefaultSettings()
	if got != want {
		t.Fatalf("настройки по умолчанию = %+v, ожидались %+v", got, want)
	}
	if got.ClientMTU != 1280 {
		t.Errorf("MTU по умолчанию = %d, ожидался 1280", got.ClientMTU)
	}
	if got.WGServerAddress != "10.8.0.1" {
		t.Errorf("адрес сервера = %q, ожидался 10.8.0.1", got.WGServerAddress)
	}
	if got.PoolUpdateInterval != time.Hour {
		t.Errorf("интервал пула по умолчанию = %s, ожидался 1ч", got.PoolUpdateInterval)
	}
}

// Нижняя граница интервала пула допустима: диапазон отсекается ниже неё, а не на ней.
func TestSettingsPoolIntervalFloor(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	v := DefaultSettings()
	v.PoolUpdateInterval = MinPoolUpdateInterval
	if err := s.SaveSettings(ctx, v); err != nil {
		t.Fatalf("интервал пула %s отвергнут: %v", MinPoolUpdateInterval, err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	want := DefaultSettings()
	want.EndpointHost = "vpn.example.org"
	want.WGListenPort = 51821
	want.DNSType = "dot"
	want.DNSUpstream = "one.one.one.one"
	want.ListUpdateInterval = 6 * time.Hour
	want.PoolUpdateInterval = 12 * time.Hour
	want.LogLevel = "info"

	if err := s.SaveSettings(ctx, want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err := s.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got != want {
		t.Fatalf("прочитано %+v, ожидалось %+v", got, want)
	}

	// Повторное сохранение перезаписывает, а не дублирует ключи.
	want.LogLevel = "debug"
	if err := s.SaveSettings(ctx, want); err != nil {
		t.Fatalf("повторный SaveSettings: %v", err)
	}
	if got, err = s.Settings(ctx); err != nil || got.LogLevel != "debug" {
		t.Fatalf("после перезаписи: %+v, ошибка %v", got, err)
	}
}

// Неизвестный ключ в БД (откат на прежнюю версию демона) не ломает чтение.
func TestSettingsIgnoresUnknownKey(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES ('будущая_настройка', 'да')`); err != nil {
		t.Fatalf("вставка ключа: %v", err)
	}
	if _, err := s.Settings(ctx); err != nil {
		t.Fatalf("Settings: %v", err)
	}
}

func TestSettingsValidation(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	cases := map[string]func(*Settings){
		"порт вне диапазона":  func(v *Settings) { v.WGListenPort = 0 },
		"MTU вне диапазона":   func(v *Settings) { v.ClientMTU = 9000 },
		"MTU ниже 1280":       func(v *Settings) { v.ClientMTU = 1000 },
		"MTU выше 1420":       func(v *Settings) { v.ClientMTU = 1500 },
		"пустой пул":          func(v *Settings) { v.WGPool = "" },
		"пустой апстрим DNS":  func(v *Settings) { v.DNSUpstream = "" },
		"неизвестный DNS":     func(v *Settings) { v.DNSType = "quic" },
		"неизвестный уровень": func(v *Settings) { v.LogLevel = "trace" },
		"нулевой интервал":    func(v *Settings) { v.ListUpdateInterval = 0 },
		"интервал пула ниже floor": func(v *Settings) {
			v.PoolUpdateInterval = MinPoolUpdateInterval - time.Minute
		},
		"нулевой интервал пула": func(v *Settings) { v.PoolUpdateInterval = 0 },
		// Опечатка в адресе доезжала до старта демона и не давала поднять шлюз
		// (аудит от 2026-07, пункт 11).
		"пул не подсеть":          func(v *Settings) { v.WGPool = "10.8.0.0" },
		"пул с пробелом":          func(v *Settings) { v.WGPool = " 10.8.0.0/24" },
		"пул IPv6":                func(v *Settings) { v.WGPool = "fd00::/64" },
		"адрес сервера с буквой":  func(v *Settings) { v.WGServerAddress = "10.8.0.o" },
		"адрес сервера — подсеть": func(v *Settings) { v.WGServerAddress = "10.8.0.1/24" },
		"адрес сервера IPv6":      func(v *Settings) { v.WGServerAddress = "fd00::1" },
		"адрес сервера вне пула":  func(v *Settings) { v.WGServerAddress = "10.9.0.1" },
	}
	for name, broken := range cases {
		t.Run(name, func(t *testing.T) {
			v := DefaultSettings()
			broken(&v)
			if err := s.SaveSettings(ctx, v); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
			}
		})
	}
}

// Испорченное числовое значение в БД даёт внятную ошибку, а не тихий ноль.
func TestSettingsBrokenNumber(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, 'много')`, keyClientMTU); err != nil {
		t.Fatalf("вставка ключа: %v", err)
	}
	if _, err := s.Settings(ctx); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
	}
}

// 1420 из подсказки ADR 0004 остаётся допустимым: диапазон сужен снизу, а не
// схлопнут в одно значение.
func TestSettingsMTUBounds(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	for _, mtu := range []int{MinClientMTU, 1400, MaxClientMTU} {
		v := DefaultSettings()
		v.ClientMTU = mtu
		if err := s.SaveSettings(ctx, v); err != nil {
			t.Fatalf("MTU %d отвергнут: %v", mtu, err)
		}
	}
}
