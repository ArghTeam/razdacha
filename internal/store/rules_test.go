package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func sampleRule(name, tunnelID string) Rule {
	return Rule{
		Name:           name,
		Action:         ActionTunnel,
		TunnelID:       tunnelID,
		Enabled:        true,
		CommunityLists: []string{"youtube", "google"},
		Domains:        []string{"example.org"},
		Subnets:        []string{"192.0.2.0/24"},
		PeerScope:      ScopeAll,
	}
}

// ruleIDs — идентификаторы правил в текущем порядке проверки.
func ruleIDs(t *testing.T, s *Store) []string {
	t.Helper()
	list, err := s.Rules(context.Background())
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}
	ids := make([]string, len(list))
	for i, r := range list {
		ids[i] = r.ID
		if r.Priority != i {
			t.Errorf("приоритет правила %q = %d, ожидался %d", r.Name, r.Priority, i)
		}
	}
	return ids
}

func TestRuleRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	tunnel, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	created, err := s.CreateRule(ctx, sampleRule("YouTube", tunnel.ID))
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	got, err := s.Rule(ctx, created.ID)
	if err != nil {
		t.Fatalf("Rule: %v", err)
	}
	if len(got.CommunityLists) != 2 || got.CommunityLists[0] != "youtube" {
		t.Errorf("списки сообщества не сохранились: %+v", got.CommunityLists)
	}
	if len(got.Domains) != 1 || len(got.Subnets) != 1 {
		t.Errorf("домены/подсети не сохранились: %+v", got)
	}
	if len(got.RemoteLists) != 0 || len(got.PeerIDs) != 0 {
		t.Errorf("пустые списки не должны наполняться при чтении: %+v", got)
	}
	if got.TunnelID != tunnel.ID {
		t.Errorf("tunnel_id = %q, ожидался %q", got.TunnelID, tunnel.ID)
	}

	got.Name = "YouTube и Google"
	got.Domains = append(got.Domains, "youtube.com")
	if err := s.UpdateRule(ctx, got); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	after, err := s.Rule(ctx, got.ID)
	if err != nil {
		t.Fatalf("Rule после обновления: %v", err)
	}
	if after.Name != "YouTube и Google" || len(after.Domains) != 2 {
		t.Errorf("обновление не применилось: %+v", after)
	}

	if err := s.DeleteRule(ctx, after.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	if _, err := s.Rule(ctx, after.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("после удаления ожидалась ErrNotFound, получено: %v", err)
	}
}

// Правило без туннеля: direct и block не ссылаются никуда.
func TestRuleWithoutTunnel(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	r, err := s.CreateRule(ctx, Rule{
		Name:      "Реклама",
		Action:    ActionBlock,
		Enabled:   true,
		Domains:   []string{"ads.example"},
		PeerScope: ScopeAll,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	got, err := s.Rule(ctx, r.ID)
	if err != nil {
		t.Fatalf("Rule: %v", err)
	}
	if got.TunnelID != "" {
		t.Errorf("tunnel_id = %q, ожидалась пустая строка", got.TunnelID)
	}
}

func TestRuleValidation(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	cases := map[string]Rule{
		"tunnel без туннеля":   {Name: "X", Action: ActionTunnel, PeerScope: ScopeAll},
		"block с туннелем":     {Name: "X", Action: ActionBlock, TunnelID: "t", PeerScope: ScopeAll},
		"неизвестное действие": {Name: "X", Action: "dorect", PeerScope: ScopeAll},
		"selected без пиров":   {Name: "X", Action: ActionDirect, PeerScope: ScopeSelected},
		"all со списком пиров": {
			Name: "X", Action: ActionDirect, PeerScope: ScopeAll, PeerIDs: []string{"p"},
		},
		"пустое имя": {Action: ActionDirect, PeerScope: ScopeAll},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.CreateRule(ctx, r); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
			}
		})
	}
}

func TestRuleUnknownTunnel(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	_, err := s.CreateRule(ctx, sampleRule("YouTube", "нет-такого"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено: %v", err)
	}
}

// Приоритет назначает слой хранения: правила добавляются в конец списка.
func TestCreateRuleAppendsToEnd(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	for _, name := range []string{"первое", "второе", "третье"} {
		r, err := s.CreateRule(ctx, Rule{
			Name: name, Action: ActionDirect, Enabled: true, PeerScope: ScopeAll,
		})
		if err != nil {
			t.Fatalf("CreateRule %s: %v", name, err)
		}
		_ = r
	}

	list, err := s.Rules(ctx)
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}
	want := []string{"первое", "второе", "третье"}
	for i, r := range list {
		if r.Name != want[i] || r.Priority != i {
			t.Fatalf("правило %d = %q с приоритетом %d, ожидалось %q с %d",
				i, r.Name, r.Priority, want[i], i)
		}
	}
}

// Уникальность rules.priority соблюдается при переупорядочивании: прямая перестановка
// столкнулась бы на промежуточном значении.
func TestReorderRules(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	for _, name := range []string{"a", "b", "c", "d"} {
		if _, err := s.CreateRule(ctx, Rule{
			Name: name, Action: ActionDirect, Enabled: true, PeerScope: ScopeAll,
		}); err != nil {
			t.Fatalf("CreateRule %s: %v", name, err)
		}
	}
	ids := ruleIDs(t, s)

	// Разворот — худший случай для построчной проверки уникального индекса.
	reversed := []string{ids[3], ids[2], ids[1], ids[0]}
	if err := s.ReorderRules(ctx, reversed); err != nil {
		t.Fatalf("ReorderRules: %v", err)
	}
	got := ruleIDs(t, s)
	for i := range reversed {
		if got[i] != reversed[i] {
			t.Fatalf("порядок после перестановки = %v, ожидался %v", got, reversed)
		}
	}

	// Обмен соседей — второй случай, где приоритеты пересекаются.
	swapped := []string{got[1], got[0], got[2], got[3]}
	if err := s.ReorderRules(ctx, swapped); err != nil {
		t.Fatalf("ReorderRules (обмен соседей): %v", err)
	}
	if after := ruleIDs(t, s); after[0] != swapped[0] || after[1] != swapped[1] {
		t.Fatalf("порядок после обмена = %v, ожидался %v", after, swapped)
	}
}

func TestReorderRulesRejectsPartialOrder(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	for _, name := range []string{"a", "b"} {
		if _, err := s.CreateRule(ctx, Rule{
			Name: name, Action: ActionDirect, Enabled: true, PeerScope: ScopeAll,
		}); err != nil {
			t.Fatalf("CreateRule %s: %v", name, err)
		}
	}
	ids := ruleIDs(t, s)

	if err := s.ReorderRules(ctx, ids[:1]); !errors.Is(err, ErrInvalid) {
		t.Errorf("неполный порядок: ожидалась ErrInvalid, получено: %v", err)
	}
	if err := s.ReorderRules(ctx, []string{ids[0], ids[0]}); !errors.Is(err, ErrInvalid) {
		t.Errorf("дубль в порядке: ожидалась ErrInvalid, получено: %v", err)
	}
	if err := s.ReorderRules(ctx, []string{ids[0], "чужой"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("чужой id: ожидалась ErrNotFound, получено: %v", err)
	}
	if after := ruleIDs(t, s); after[0] != ids[0] || after[1] != ids[1] {
		t.Errorf("отклонённая перестановка изменила порядок: %v", after)
	}
}

// После удаления в приоритетах не остаётся дыр.
func TestDeleteRuleCompactsPriorities(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	for _, name := range []string{"a", "b", "c"} {
		if _, err := s.CreateRule(ctx, Rule{
			Name: name, Action: ActionDirect, Enabled: true, PeerScope: ScopeAll,
		}); err != nil {
			t.Fatalf("CreateRule %s: %v", name, err)
		}
	}
	ids := ruleIDs(t, s)

	if err := s.DeleteRule(ctx, ids[0]); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	// ruleIDs сам проверяет, что приоритеты идут 0..N-1 без дыр.
	rest := ruleIDs(t, s)
	if len(rest) != 2 || rest[0] != ids[1] || rest[1] != ids[2] {
		t.Fatalf("после удаления остались %v, ожидались %v", rest, ids[1:])
	}

	// Новое правило встаёт в конец, а не на освободившийся приоритет.
	if _, err := s.CreateRule(ctx, Rule{
		Name: "d", Action: ActionDirect, Enabled: true, PeerScope: ScopeAll,
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if final := ruleIDs(t, s); len(final) != 3 {
		t.Fatalf("после добавления %d правил, ожидалось 3", len(final))
	}
}

// UpdateRule не трогает приоритет: порядок меняется только через ReorderRules.
func TestUpdateRuleKeepsPriority(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	for _, name := range []string{"a", "b"} {
		if _, err := s.CreateRule(ctx, Rule{
			Name: name, Action: ActionDirect, Enabled: true, PeerScope: ScopeAll,
		}); err != nil {
			t.Fatalf("CreateRule %s: %v", name, err)
		}
	}
	ids := ruleIDs(t, s)

	second, err := s.Rule(ctx, ids[1])
	if err != nil {
		t.Fatalf("Rule: %v", err)
	}
	second.Priority = 0 // попытка переставить правило через обновление
	if err := s.UpdateRule(ctx, second); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	if after := ruleIDs(t, s); after[0] != ids[0] || after[1] != ids[1] {
		t.Errorf("UpdateRule изменил порядок: %v, ожидался %v", after, ids)
	}
}

// Второе звено цепи (ADR 0012) переживает запись и чтение, а очистка поля
// возвращает правило к работе одним туннелем.
func TestRuleChainRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	first, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	warp, err := s.CreateTunnel(ctx, warpTunnel("WARP"))
	if err != nil {
		t.Fatalf("CreateTunnel WARP: %v", err)
	}

	r := sampleRule("YouTube", first.ID)
	r.ViaTunnelID = warp.ID
	created, err := s.CreateRule(ctx, r)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	got, err := s.Rule(ctx, created.ID)
	if err != nil {
		t.Fatalf("Rule: %v", err)
	}
	if got.ViaTunnelID != warp.ID {
		t.Fatalf("via_tunnel_id = %q, ожидался %q", got.ViaTunnelID, warp.ID)
	}

	got.ViaTunnelID = ""
	if err := s.UpdateRule(ctx, got); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	again, err := s.Rule(ctx, created.ID)
	if err != nil {
		t.Fatalf("Rule: %v", err)
	}
	if again.ViaTunnelID != "" {
		t.Errorf("второе звено не снялось: %q", again.ViaTunnelID)
	}
}

// Туннель, стоящий вторым звеном, держится так же крепко, как первым: удаление
// отклоняется и называет правило.
func TestDeleteTunnelUsedAsSecondHop(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	first, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	warp, err := s.CreateTunnel(ctx, warpTunnel("WARP"))
	if err != nil {
		t.Fatalf("CreateTunnel WARP: %v", err)
	}
	r := sampleRule("YouTube", first.ID)
	r.ViaTunnelID = warp.ID
	if _, err := s.CreateRule(ctx, r); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	err = s.DeleteTunnel(ctx, warp.ID)
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("ожидалась ErrInUse, получено: %v", err)
	}
	if !strings.Contains(err.Error(), "YouTube") {
		t.Errorf("в сообщении нет имени правила: %v", err)
	}
}

// Второе звено бывает только у правила в туннель: «напрямую» и «блокировать»
// наружу не выходят, и цепи там взяться неоткуда.
func TestRuleChainRejectedForNonTunnelAction(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	r := Rule{Name: "X", Action: ActionDirect, PeerScope: ScopeAll, ViaTunnelID: "t"}
	if _, err := s.CreateRule(ctx, r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
	}
}

// resolve_real_ip выключает FakeIP, и маршрут правила остаётся на подсетях.
// Правило без единого их источника не маршрутизирует ничего и не сохраняется:
// иначе трафик молча ушёл бы напрямую (аудит от 2026-07, пункт 8).
func TestRuleResolveRealIPWithoutSubnets(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	tun, errTun := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if errTun != nil {
		t.Fatalf("CreateTunnel: %v", errTun)
	}

	r := Rule{
		Name: "Свой сервис", Action: ActionTunnel, TunnelID: tun.ID,
		Enabled: true, PeerScope: ScopeAll, ResolveRealIP: true,
		Domains: []string{"example.org"},
	}
	if _, err := s.CreateRule(ctx, r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
	}

	// Подсети руками — правило рабочее.
	withSubnets := r
	withSubnets.Subnets = []string{"192.0.2.0/24"}
	created, err := s.CreateRule(ctx, withSubnets)
	if err != nil {
		t.Fatalf("CreateRule с подсетями: %v", err)
	}

	// Список тоже источник подсетей: их заливает в nft-сет слой lists.
	fromList := r
	fromList.RemoteLists = []string{"https://example.org/list.txt"}
	if _, err := s.CreateRule(ctx, fromList); err != nil {
		t.Fatalf("CreateRule со списком: %v", err)
	}

	// Снятие подсетей правкой запрещено ровно так же, как их отсутствие при создании.
	created.Subnets = nil
	if err := s.UpdateRule(ctx, created); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid при снятии подсетей, получено: %v", err)
	}
}
