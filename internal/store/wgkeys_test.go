package store

import (
	"context"
	"testing"
)

// Ключа нет — пустая строка, а не ошибка: «интерфейс ни разу не поднимали» это
// штатное состояние первого запуска.
func TestServerPrivateKeyEmptyByDefault(t *testing.T) {
	got, err := open(t).ServerPrivateKey(context.Background())
	if err != nil {
		t.Fatalf("ServerPrivateKey: %v", err)
	}
	if got != "" {
		t.Errorf("ключ сервера = %q, ожидалась пустая строка", got)
	}
}

// Ключ сервера переживает сохранение настроек из панели: SaveSettings пишет
// только свои ключи, а Settings игнорирует чужие.
func TestServerPrivateKeySurvivesSaveSettings(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	const key = "c2VydmVyLXByaXZhdGUta2V5LTMyLWJ5dGVzLTAwMD0="

	if err := s.SetServerPrivateKey(ctx, key); err != nil {
		t.Fatalf("SetServerPrivateKey: %v", err)
	}
	settings := DefaultSettings()
	settings.EndpointHost = "vpn.example.com"
	if err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := s.ServerPrivateKey(ctx)
	if err != nil {
		t.Fatalf("ServerPrivateKey: %v", err)
	}
	if got != key {
		t.Errorf("ключ сервера = %q, ожидался %q", got, key)
	}

	// И не утекает в настройки, которые уходят в панель.
	read, err := s.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if read != settings {
		t.Errorf("настройки = %+v, ожидались %+v", read, settings)
	}
}

func TestSetServerPrivateKeyRejectsEmpty(t *testing.T) {
	if err := open(t).SetServerPrivateKey(context.Background(), ""); err == nil {
		t.Fatal("пустой ключ сервера принят")
	}
}
