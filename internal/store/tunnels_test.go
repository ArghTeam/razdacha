package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func sampleTunnel(name string) Tunnel {
	return Tunnel{
		Name:    name,
		Type:    TunnelVLESS,
		Source:  SourceURL,
		Raw:     "vless://uuid@example.org:443?type=tcp&security=tls#" + name,
		Parsed:  []byte(`{"type":"vless"}`),
		Enabled: true,
	}
}

func TestTunnelRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	created, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if created.ID == "" || created.CreatedAt.IsZero() {
		t.Fatal("CreateTunnel не заполнил id или дату создания")
	}

	got, err := s.Tunnel(ctx, created.ID)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	if got.Name != created.Name || got.Type != TunnelVLESS || got.Raw != created.Raw {
		t.Errorf("прочитан не тот туннель: %+v", got)
	}
	if string(got.Parsed) != `{"type":"vless"}` {
		t.Errorf("parsed = %s", got.Parsed)
	}
	if !got.Enabled {
		t.Error("enabled потерялся при чтении")
	}

	got.Name = "запасной"
	got.Enabled = false
	if err := s.UpdateTunnel(ctx, got); err != nil {
		t.Fatalf("UpdateTunnel: %v", err)
	}
	after, err := s.Tunnel(ctx, got.ID)
	if err != nil {
		t.Fatalf("Tunnel после обновления: %v", err)
	}
	if after.Name != "запасной" || after.Enabled {
		t.Errorf("обновление не применилось: %+v", after)
	}
	if !after.CreatedAt.Equal(created.CreatedAt.UTC().Truncate(0)) &&
		after.CreatedAt.Unix() != created.CreatedAt.Unix() {
		t.Errorf("дата создания изменилась: %v → %v", created.CreatedAt, after.CreatedAt)
	}

	if err := s.DeleteTunnel(ctx, got.ID); err != nil {
		t.Fatalf("DeleteTunnel: %v", err)
	}
	if _, err := s.Tunnel(ctx, got.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("после удаления ожидалась ErrNotFound, получено: %v", err)
	}
}

func TestTunnelNameIsUnique(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.CreateTunnel(ctx, sampleTunnel("основной")); err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	_, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid на дубль имени, получено: %v", err)
	}
	if !strings.Contains(err.Error(), "основной") {
		t.Errorf("в ошибке нет имени туннеля: %v", err)
	}
}

func TestTunnelValidation(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	cases := map[string]func(*Tunnel){
		"пустое имя":      func(v *Tunnel) { v.Name = "" },
		"неизвестный тип": func(v *Tunnel) { v.Type = "wireguad" },
		"пустой конфиг":   func(v *Tunnel) { v.Raw = "" },
		"parsed не JSON":  func(v *Tunnel) { v.Parsed = []byte("{") },
	}
	for name, broken := range cases {
		t.Run(name, func(t *testing.T) {
			v := sampleTunnel("основной")
			broken(&v)
			if _, err := s.CreateTunnel(ctx, v); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
			}
		})
	}
}

// Удаление туннеля, на который ссылается правило, отклоняется внятной ошибкой,
// а не тихо ломает маршрутизацию.
func TestDeleteTunnelInUseIsRejected(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	tunnel, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if _, err := s.CreateRule(ctx, sampleRule("YouTube и Google", tunnel.ID)); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	err = s.DeleteTunnel(ctx, tunnel.ID)
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("ожидалась ErrInUse, получено: %v", err)
	}
	if !strings.Contains(err.Error(), "YouTube и Google") {
		t.Errorf("в ошибке не названо правило: %v", err)
	}
	if _, err := s.Tunnel(ctx, tunnel.ID); err != nil {
		t.Errorf("туннель всё-таки удалён: %v", err)
	}
}

// Та же защита уровнем ниже: ON DELETE RESTRICT не даёт удалить туннель в обход слоя.
func TestDeleteTunnelRestrictedBySchema(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	tunnel, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if _, err := s.CreateRule(ctx, sampleRule("Соцсети", tunnel.ID)); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM tunnels WHERE id = ?`, tunnel.ID); err == nil {
		t.Fatal("прямое удаление прошло — ON DELETE RESTRICT не работает")
	}
}

func TestDeleteUnknownTunnel(t *testing.T) {
	s := open(t)
	if err := s.DeleteTunnel(context.Background(), "нет-такого"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено: %v", err)
	}
}
