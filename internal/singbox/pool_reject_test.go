package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

// poisonChecker изображает `sing-box check`, который отвергает любой outbound, чей
// адрес сервера попал в bad. Ошибку отдаёт ровно в том виде, что рантайм:
// `initialize outbound[N]: …` (box.go). Так проверяется разбор индекса и
// выкидывание именно того участника, без установленного sing-box.
type poisonChecker struct {
	bad   map[string]bool
	calls int
	// servers — адреса серверов в конфиге на каждом вызове по порядку: тест
	// смотрит, кто дошёл до последней проверки.
	servers [][]string
}

func (c *poisonChecker) Check(_ context.Context, path string) error {
	c.calls++
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg struct {
		Outbounds []struct {
			Tag    string `json:"tag"`
			Server string `json:"server"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	seen := make([]string, 0, len(cfg.Outbounds))
	for _, ob := range cfg.Outbounds {
		if ob.Server != "" {
			seen = append(seen, ob.Server)
		}
	}
	c.servers = append(c.servers, seen)
	for i, ob := range cfg.Outbounds {
		if c.bad[ob.Server] {
			return fmt.Errorf("%w: initialize outbound[%d]: сервер %s рантаймом не собран",
				ErrCheckFailed, i, ob.Server)
		}
	}
	return nil
}

// vlessURL собирает разбираемую ссылку с заданным хостом сервера: хост управляем,
// поэтому poisonChecker отвергает ровно нужного участника.
func vlessURL(host, tag string) string {
	return fmt.Sprintf(
		"vless://11111111-2222-3333-4444-555555555555@%s:443?security=tls&sni=example.org#%s",
		host, tag)
}

// poolOf собирает пул из хостов: каждый становится отдельным сервером в порядке,
// который равен приоритету отбора.
func poolOf(hosts ...string) []store.PoolServer {
	out := make([]store.PoolServer, 0, len(hosts))
	for i, h := range hosts {
		out = append(out, store.PoolServer{
			URL:   vlessURL(h, fmt.Sprintf("s%d", i)),
			Title: fmt.Sprintf("сервер %d", i),
		})
	}
	return out
}

// serversInConfig — адреса участников пула, доехавших до конфига на диске.
func serversInConfig(t *testing.T, path string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение конфига: %v", err)
	}
	var cfg struct {
		Outbounds []struct {
			Tag    string `json:"tag"`
			Server string `json:"server"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("разбор конфига: %v", err)
	}
	out := make(map[string]bool)
	for _, ob := range cfg.Outbounds {
		if ob.Server != "" {
			out[ob.Server] = true
		}
	}
	return out
}

// Битый участник пула не роняет применение: он выкидывается, конфиг пересобирается,
// исправные серверы остаются в строю, а окно доберётся годным из остатка (ADR 0019).
func TestApplyDropsRejectedPoolMember(t *testing.T) {
	// Восемнадцать серверов при потолке шестнадцать: один в окне битый, за окном
	// есть годный запас на добор.
	hosts := make([]string, 0, poolMaxServers+2)
	for i := 0; i < poolMaxServers+2; i++ {
		hosts = append(hosts, fmt.Sprintf("good%d.example", i))
	}
	// Битый стоит в окне (позиция 5), а не в хвосте: его обязано вычистить.
	hosts[5] = "bad.example"

	snap := poolFixture(poolOf(hosts...))
	check := &poisonChecker{bad: map[string]bool{"bad.example": true}}
	reload := &fakeReloader{}
	a, path := applier(t, check, reload)

	res, err := a.Apply(context.Background(), snap)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Changed || !res.Reloaded {
		t.Fatalf("changed=%v reloaded=%v, ожидалось true/true", res.Changed, res.Reloaded)
	}
	// Ровно два вызова check: первый спотыкается о битого, второй проходит.
	if check.calls != 2 {
		t.Fatalf("check вызван %d раз, ожидалось 2 (отказ + пересборка)", check.calls)
	}
	if reload.calls != 1 {
		t.Fatalf("reload вызван %d раз, ожидался 1", reload.calls)
	}

	got := serversInConfig(t, path)
	if got["bad.example"] {
		t.Error("битый участник остался в конфиге")
	}
	if len(got) != poolMaxServers {
		t.Fatalf("в конфиге %d участников, ожидался добор до %d", len(got), poolMaxServers)
	}
	// Добор: за окном стояли good16 и good17 — один из них занял место битого.
	if !got["good16.example"] {
		t.Error("окно не добралось годным из остатка пула")
	}
}

// Битый ручной туннель (source != pool) по-прежнему валит применение целиком:
// прежний конфиг остаётся, ошибка доходит как есть, повтора нет (ADR 0019).
func TestApplyKeepsRejectingBadManualTunnel(t *testing.T) {
	snap := poolFixture(poolOf("good0.example", "good1.example", "good2.example"))
	// Рядом — ручной туннель, чей сервер рантайм отвергает.
	snap.Tunnels = append(snap.Tunnels, store.Tunnel{
		ID: "mmmm", Name: "Ручной", Type: store.TunnelVLESS,
		Source: store.SourceURL, Raw: "vless://…", Enabled: true,
		Parsed: []byte(`{"server":"bad-manual.example","server_port":443,
			"uuid":"11111111-2222-3333-4444-555555555555"}`),
	})
	snap.Rules = append(snap.Rules, store.Rule{
		ID: "r2", Name: "Соцсети", Action: store.ActionTunnel,
		TunnelID: "mmmm", Priority: 1, Enabled: true,
		CommunityLists: []string{"meta"}, PeerScope: store.ScopeAll,
	})

	check := &poisonChecker{bad: map[string]bool{"bad-manual.example": true}}
	reload := &fakeReloader{}
	a, path := applier(t, check, reload)

	res, err := a.Apply(context.Background(), snap)
	if !errors.Is(err, ErrCheckFailed) {
		t.Fatalf("ошибка %v, ожидалась ErrCheckFailed", err)
	}
	if res.Changed || res.Reloaded {
		t.Fatalf("changed=%v reloaded=%v, ожидалось false/false", res.Changed, res.Reloaded)
	}
	// Никакого перебора участников: чужую ошибку не глотаем, повтора нет.
	if check.calls != 1 {
		t.Fatalf("check вызван %d раз, ожидался 1 (без перебора)", check.calls)
	}
	if reload.calls != 0 {
		t.Fatalf("reload вызван %d раз, ожидался 0", reload.calls)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("битый конфиг всё же записан на диск")
	}
}

// В лог выкинутого участника уходит адрес хост:порт, а не ссылка с ключом (#124).
func TestApplyDropLogHasNoKey(t *testing.T) {
	snap := poolFixture(poolOf("good0.example", "bad.example", "good2.example"))
	check := &poisonChecker{bad: map[string]bool{"bad.example": true}}
	reload := &fakeReloader{}

	var buf strings.Builder
	a, _ := applier(t, check, reload)
	a.Log = slog.New(slog.NewTextHandler(&buf, nil))

	if _, err := a.Apply(context.Background(), snap); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "11111111-2222-3333-4444-555555555555") || strings.Contains(out, "vless://") {
		t.Errorf("ключ уехал в лог: %s", out)
	}
	if !strings.Contains(out, "bad.example:443") {
		t.Errorf("в логе нет адреса выкинутого участника: %s", out)
	}
}

// Терпимость строго к outbound-участнику пула: endpoint[N] членом пула не бывает
// (пул всегда outbound), а несовпавшая ошибка — не повод что-либо выкидывать.
func TestRejectedPoolMemberIsSelective(t *testing.T) {
	snap := poolFixture(poolOf("good0.example", "good1.example"))
	opts := mustGenerate(t, snap)

	cases := map[string]error{
		"endpoint отвергнут":   wrapCheck("initialize endpoint[0]: что-то"),
		"без индекса":          wrapCheck("route rule 0: unknown outbound"),
		"outbound не участник": wrapCheck("initialize outbound[0]: direct-out"), // индекс 0 — direct-out
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := rejectedPoolMember(opts, snap, err); ok {
				t.Errorf("ошибка %q принята за отказ участника пула", err)
			}
		})
	}
}
