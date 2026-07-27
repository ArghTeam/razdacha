package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func savedCheck(t *testing.T, s *Store, ctx context.Context, id string) (TunnelCheck, bool) {
	t.Helper()
	all, err := s.TunnelChecks(ctx)
	if err != nil {
		t.Fatalf("TunnelChecks: %v", err)
	}
	c, ok := all[id]
	return c, ok
}

func TestTunnelCheckRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	tun, err := s.CreateTunnel(ctx, sampleTunnel("Нидерланды"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	at := time.Date(2026, 7, 27, 6, 40, 0, 0, time.UTC)
	if err := s.SaveTunnelCheck(ctx, tun.ID, "down", at); err != nil {
		t.Fatalf("SaveTunnelCheck: %v", err)
	}

	got, ok := savedCheck(t, s, ctx, tun.ID)
	if !ok {
		t.Fatal("проверка не сохранилась")
	}
	if got.Status != "down" || !got.CheckedAt.Equal(at) {
		t.Errorf("сохранено %+v, ожидалось down в %v", got, at)
	}

	// Повторная запись обновляет ту же строку, а не заводит вторую.
	later := at.Add(2 * time.Minute)
	if err := s.SaveTunnelCheck(ctx, tun.ID, "up", later); err != nil {
		t.Fatalf("SaveTunnelCheck: %v", err)
	}
	got, _ = savedCheck(t, s, ctx, tun.ID)
	if got.Status != "up" || !got.CheckedAt.Equal(later) {
		t.Errorf("после обновления %+v, ожидалось up в %v", got, later)
	}
}

// Удалённый туннель не должен держать за собой запись о проверке: без каскада
// она пережила бы туннель и всплыла бы на новом с тем же идентификатором.
func TestTunnelCheckCascadesOnDelete(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	tun, err := s.CreateTunnel(ctx, sampleTunnel("Нидерланды"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	if err := s.SaveTunnelCheck(ctx, tun.ID, "up", time.Now()); err != nil {
		t.Fatalf("SaveTunnelCheck: %v", err)
	}
	if err := s.DeleteTunnel(ctx, tun.ID); err != nil {
		t.Fatalf("DeleteTunnel: %v", err)
	}
	if _, ok := savedCheck(t, s, ctx, tun.ID); ok {
		t.Error("запись о проверке пережила туннель")
	}
}

// Проверка несуществующего туннеля не заводит запись-сироту: её некому было бы
// убрать, и она всплыла бы на туннеле с тем же идентификатором.
func TestTunnelCheckRejectsUnknownTunnel(t *testing.T) {
	s := open(t)
	err := s.SaveTunnelCheck(context.Background(), "нет-такого", "up", time.Now())
	if err == nil {
		t.Fatal("ожидалась ошибка на неизвестный туннель")
	}
}

func TestTunnelCheckRejectsEmpty(t *testing.T) {
	s := open(t)
	err := s.SaveTunnelCheck(context.Background(), "", "up", time.Now())
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("ошибка = %v, ожидалась ErrInvalid", err)
	}
}

// Интервал проверки не может быть сколь угодно малым: каждый прогон пробивает
// обычные туннели настоящим запросом.
func TestTunnelCheckIntervalLowerBound(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	v := DefaultSettings()
	v.TunnelCheckInterval = 5 * time.Second
	if err := s.SaveSettings(ctx, v); !errors.Is(err, ErrInvalid) {
		t.Errorf("ошибка = %v, ожидалась ErrInvalid", err)
	}

	v.TunnelCheckInterval = 90 * time.Second
	if err := s.SaveSettings(ctx, v); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err := s.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.TunnelCheckInterval != 90*time.Second {
		t.Errorf("интервал = %v, ожидалось 90s", got.TunnelCheckInterval)
	}
}
