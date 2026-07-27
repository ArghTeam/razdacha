package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// warpConf — конфиг WARP в том виде, в котором он лежит в raw: endpoint на
// cloudflareclient.com, по нему туннель и узнаётся.
const warpConf = `[Interface]
PrivateKey = qJ+8kZ0d0Xh0j0m3v0nQ0m0Z0Q0m0Z0Q0m0Z0Q0m0Xk=
Address = 172.16.0.2/32
MTU = 1280

[Peer]
PublicKey = bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=
AllowedIPs = 0.0.0.0/0
Endpoint = engage.cloudflareclient.com:2408
`

func warpTunnel(name string) Tunnel {
	return Tunnel{
		Name:    name,
		Type:    TunnelWireGuard,
		Source:  SourceWARP,
		Raw:     warpConf,
		Parsed:  []byte(`{"type":"wireguard"}`),
		Enabled: true,
	}
}

// WARP — форма конфига, а не протокол: туннель хранится и читается как обычный
// WireGuard, отличается только source.
func TestWARPTunnelRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	created, err := s.CreateTunnel(ctx, warpTunnel("WARP"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	got, err := s.Tunnel(ctx, created.ID)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	if got.Source != SourceWARP || got.Type != TunnelWireGuard {
		t.Errorf("форма %q, тип %q — ожидались warp и wireguard", got.Source, got.Type)
	}
}

// Протокол у WARP один: source описывает происхождение ключей, а не канал.
func TestWARPTunnelRejectsOtherType(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	bad := warpTunnel("WARP")
	bad.Type = TunnelVLESS
	_, err := s.CreateTunnel(ctx, bad)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid для WARP типа vless, получено: %v", err)
	}
}

// Встроенной бывает только запись пула: WARP заводит пользователь кнопкой, и
// неудаляемым он быть не должен.
func TestWARPTunnelNotBuiltin(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	bad := warpTunnel("WARP")
	bad.Builtin = true
	if _, err := s.CreateTunnel(ctx, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid для встроенного WARP, получено: %v", err)
	}
}

// Миграция 7 переводит на source = warp туннели, заведённые вставленным .conf до
// появления признака: иначе цепочка (ADR 0012) не увидела бы их вторым звеном, а
// пользователю пришлось бы пересохранять туннель руками.
func TestMigrationMarksPastedWARP(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "razdacha.db")

	s := openAt(t, path)
	warp, err := s.CreateTunnel(ctx, Tunnel{
		Name: "WARP руками", Type: TunnelWireGuard, Source: SourceWGConf,
		Raw: warpConf, Parsed: []byte(`{"type":"wireguard"}`), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	plain, err := s.CreateTunnel(ctx, Tunnel{
		Name: "Свой сервер", Type: TunnelWireGuard, Source: SourceWGConf,
		Raw:     strings.Replace(warpConf, "engage.cloudflareclient.com", "vpn.example.org", 1),
		Parsed:  []byte(`{"type":"wireguard"}`),
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	// Откат версии схемы к предыдущему шагу: так БД выглядит до этого релиза.
	// Вместе с версией откатывается и таблица правил: шаг 8 добавляет колонку
	// via_tunnel_id, и на таблице, где она уже есть, повторный ALTER упал бы.
	// Правил в этом тесте нет, поэтому таблица пересоздаётся пустой.
	if _, err := s.db.ExecContext(ctx, `
DROP TABLE rules;
CREATE TABLE rules (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, action TEXT NOT NULL,
  tunnel_id TEXT REFERENCES tunnels(id) ON DELETE RESTRICT,
  priority INTEGER NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  community_lists TEXT NOT NULL DEFAULT '[]', domains TEXT NOT NULL DEFAULT '[]',
  subnets TEXT NOT NULL DEFAULT '[]', remote_lists TEXT NOT NULL DEFAULT '[]',
  peer_scope TEXT NOT NULL DEFAULT 'all', peer_ids TEXT NOT NULL DEFAULT '[]',
  resolve_real_ip INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX rules_priority ON rules(priority);
PRAGMA user_version = 6;`); err != nil {
		t.Fatalf("подмена версии схемы: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE tunnels SET source = 'wg_conf'`); err != nil {
		t.Fatalf("возврат формы конфига: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again := openAt(t, path)
	got, err := again.Tunnel(ctx, warp.ID)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	if got.Source != SourceWARP {
		t.Errorf("форма конфига WARP = %q, ожидалась warp", got.Source)
	}
	// Чужой WireGuard остаётся чужим: признак ставится по хосту endpoint, а не
	// по типу туннеля.
	other, err := again.Tunnel(ctx, plain.ID)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	if other.Source != SourceWGConf {
		t.Errorf("форма конфига обычного WireGuard = %q, ожидалась wg_conf", other.Source)
	}
}
