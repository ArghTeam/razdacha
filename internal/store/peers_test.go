package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func samplePeer(name, address string) Peer {
	return Peer{
		Name:         name,
		PublicKey:    "pub-" + name,
		PrivateKey:   "priv-" + name,
		PresharedKey: "psk-" + name,
		Address:      address,
		Enabled:      true,
	}
}

func TestPeerRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	created, err := s.CreatePeer(ctx, samplePeer("iPhone", "10.8.0.2/32"))
	if err != nil {
		t.Fatalf("CreatePeer: %v", err)
	}
	if created.ID == "" || created.CreatedAt.IsZero() {
		t.Fatal("CreatePeer не заполнил id или дату создания")
	}

	got, err := s.Peer(ctx, created.ID)
	if err != nil {
		t.Fatalf("Peer: %v", err)
	}
	// Приватный и pre-shared ключи хранятся, чтобы конфиг можно было перевыдать.
	if got.PrivateKey != created.PrivateKey || got.PresharedKey != created.PresharedKey {
		t.Errorf("ключи не сохранились: %+v", got)
	}

	// Выключенный пир удаляется из wg0, но остаётся в БД.
	got.Enabled = false
	if err := s.UpdatePeer(ctx, got); err != nil {
		t.Fatalf("UpdatePeer: %v", err)
	}
	after, err := s.Peer(ctx, got.ID)
	if err != nil {
		t.Fatalf("Peer после обновления: %v", err)
	}
	if after.Enabled {
		t.Error("пир остался включённым")
	}

	if err := s.DeletePeer(ctx, after.ID); err != nil {
		t.Fatalf("DeletePeer: %v", err)
	}
	if _, err := s.Peer(ctx, after.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("после удаления ожидалась ErrNotFound, получено: %v", err)
	}
}

func TestPeerAddressIsUnique(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.CreatePeer(ctx, samplePeer("iPhone", "10.8.0.2/32")); err != nil {
		t.Fatalf("CreatePeer: %v", err)
	}
	_, err := s.CreatePeer(ctx, samplePeer("ноутбук", "10.8.0.2/32"))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid на занятый адрес, получено: %v", err)
	}
	if !strings.Contains(err.Error(), "10.8.0.2/32") {
		t.Errorf("в ошибке нет адреса: %v", err)
	}
}

func TestPeerPublicKeyIsUnique(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.CreatePeer(ctx, samplePeer("iPhone", "10.8.0.2/32")); err != nil {
		t.Fatalf("CreatePeer: %v", err)
	}
	dup := samplePeer("ноутбук", "10.8.0.3/32")
	dup.PublicKey = "pub-iPhone"
	if _, err := s.CreatePeer(ctx, dup); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid на дубль ключа, получено: %v", err)
	}
}

func TestPeerValidation(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	cases := map[string]func(*Peer){
		"пустое имя": func(p *Peer) { p.Name = "" },
		"без ключа":  func(p *Peer) { p.PrivateKey = "" },
		"без адреса": func(p *Peer) { p.Address = "" },
		"без psk":    func(p *Peer) { p.PresharedKey = "" },
	}
	for name, broken := range cases {
		t.Run(name, func(t *testing.T) {
			p := samplePeer("iPhone", "10.8.0.2/32")
			broken(&p)
			if _, err := s.CreatePeer(ctx, p); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
			}
		})
	}
}

// Ссылки на пира лежат в JSON-списке правила, внешнего ключа на них нет —
// удаление пира должно вычистить их само.
func TestDeletePeerCleansRules(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	one, err := s.CreatePeer(ctx, samplePeer("iPhone", "10.8.0.2/32"))
	if err != nil {
		t.Fatalf("CreatePeer: %v", err)
	}
	two, err := s.CreatePeer(ctx, samplePeer("ноутбук", "10.8.0.3/32"))
	if err != nil {
		t.Fatalf("CreatePeer: %v", err)
	}

	both, err := s.CreateRule(ctx, Rule{
		Name: "оба", Action: ActionDirect, Enabled: true,
		PeerScope: ScopeSelected, PeerIDs: []string{one.ID, two.ID},
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	only, err := s.CreateRule(ctx, Rule{
		Name: "только один", Action: ActionDirect, Enabled: true,
		PeerScope: ScopeSelected, PeerIDs: []string{one.ID},
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if err := s.DeletePeer(ctx, one.ID); err != nil {
		t.Fatalf("DeletePeer: %v", err)
	}

	gotBoth, err := s.Rule(ctx, both.ID)
	if err != nil {
		t.Fatalf("Rule: %v", err)
	}
	if len(gotBoth.PeerIDs) != 1 || gotBoth.PeerIDs[0] != two.ID {
		t.Errorf("пир не убран из правила: %+v", gotBoth.PeerIDs)
	}
	if gotBoth.PeerScope != ScopeSelected {
		t.Errorf("область действия правила изменилась зря: %q", gotBoth.PeerScope)
	}

	// Правило, оставшееся без пиров, переводится на всех — иначе оно молча
	// перестало бы работать.
	gotOnly, err := s.Rule(ctx, only.ID)
	if err != nil {
		t.Fatalf("Rule: %v", err)
	}
	if gotOnly.PeerScope != ScopeAll || len(gotOnly.PeerIDs) != 0 {
		t.Errorf("правило без пиров: scope = %q, пиры = %v",
			gotOnly.PeerScope, gotOnly.PeerIDs)
	}
}

func TestDeleteUnknownPeer(t *testing.T) {
	s := open(t)
	if err := s.DeletePeer(context.Background(), "нет-такого"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено: %v", err)
	}
}
