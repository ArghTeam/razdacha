package singbox

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

// chainFixture — снимок из fixture() плюс туннель WARP: он годится вторым звеном
// цепи, потому что у него source = warp (ADR 0012).
func chainFixture() store.Snapshot {
	snap := fixture()
	snap.Tunnels = append(snap.Tunnels, store.Tunnel{
		ID: "wwww", Name: "WARP", Type: store.TunnelWireGuard,
		Source: store.SourceWARP, Raw: "[Interface]", Enabled: true,
		Parsed: json.RawMessage(`{
			"address": ["172.16.0.2/32"],
			"private_key": "cHJpdmF0ZQ==",
			"peers": [{
				"address": "162.159.192.1", "port": 2408,
				"public_key": "cHVibGlj", "allowed_ips": ["0.0.0.0/0"]
			}]
		}`),
	})
	return snap
}

// chainEndpoints — endpoints конфига, разобранные до тега, типа и detour.
func chainEndpoints(t *testing.T, snap store.Snapshot) []struct {
	Tag    string `json:"tag"`
	Type   string `json:"type"`
	Detour string `json:"detour"`
} {
	t.Helper()
	var cfg struct {
		Endpoints []struct {
			Tag    string `json:"tag"`
			Type   string `json:"type"`
			Detour string `json:"detour"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal(generate(t, snap), &cfg); err != nil {
		t.Fatalf("разбор конфига: %v", err)
	}
	return cfg.Endpoints
}

// Правило без второго звена работает как раньше: detour в конфиге не появляется
// вовсе, лишних endpoint'ов нет.
func TestNoChainNoDetour(t *testing.T) {
	cfg := string(generate(t, chainFixture()))
	if strings.Contains(cfg, "detour") {
		t.Error("в конфиге без цепей появился detour")
	}
	if strings.Contains(cfg, "chain-") {
		t.Error("в конфиге без цепей появился тег звена цепи")
	}
}

// Цепь — клон второго звена с detour на тег первого; правило ссылается на клон,
// а исходный WARP остаётся без detour, иначе цепь увела бы и правила без неё.
func TestChainClonesSecondHop(t *testing.T) {
	snap := chainFixture()
	snap.Rules[0].ViaTunnelID = "wwww"

	tag := ChainTag("wwww", "aaaa")
	var clone, origin bool
	for _, e := range chainEndpoints(t, snap) {
		switch e.Tag {
		case tag:
			clone = true
			if e.Type != "wireguard" {
				t.Errorf("тип второго звена = %q, ожидался wireguard", e.Type)
			}
			if e.Detour != TunnelTag("aaaa") {
				t.Errorf("detour = %q, ожидался %q", e.Detour, TunnelTag("aaaa"))
			}
		case TunnelTag("wwww"):
			origin = true
			if e.Detour != "" {
				t.Errorf("у самого WARP появился detour %q", e.Detour)
			}
		}
	}
	if !clone {
		t.Fatalf("в конфиге нет второго звена цепи %q", tag)
	}
	if !origin {
		t.Error("исходный WARP пропал из конфига")
	}

	opts := mustGenerate(t, snap)
	var routed bool
	for _, r := range opts.Route.Rules {
		if r.DefaultOptions.RouteOptions.Outbound == tag {
			routed = true
		}
	}
	if !routed {
		t.Error("правило не ссылается на второе звено цепи")
	}
}

// Две одинаковые пары дают один outbound: тег зависит только от пары, иначе
// конфиг менялся бы от числа правил и перезапускал sing-box зря.
func TestChainDeduplicated(t *testing.T) {
	snap := chainFixture()
	snap.Rules[0].ViaTunnelID = "wwww"
	snap.Rules[1].ViaTunnelID = "wwww"
	snap.Rules[1].TunnelID = "aaaa"

	var chains int
	for _, e := range chainEndpoints(t, snap) {
		if strings.HasPrefix(e.Tag, "chain-") {
			chains++
		}
	}
	if chains != 1 {
		t.Fatalf("звеньев цепи в конфиге %d, ожидалось 1", chains)
	}

	// Разные первые звенья — разные пары, и клонов уже два.
	snap.Rules[1].TunnelID = "bbbb"
	chains = 0
	for _, e := range chainEndpoints(t, snap) {
		if strings.HasPrefix(e.Tag, "chain-") {
			chains++
		}
	}
	if chains != 2 {
		t.Fatalf("звеньев цепи в конфиге %d, ожидалось 2", chains)
	}
}

// Пул годится первым звеном: detour резолвится в саму группу urltest, а
// участника она выбирает на каждом дозвоне (ADR 0012).
func TestChainFromPool(t *testing.T) {
	snap := chainFixture()
	snap.Tunnels = append(snap.Tunnels, store.Tunnel{
		ID: "pppp", Name: "Пул", Type: store.TunnelVLESS,
		Source: store.SourcePool, Raw: "https://example.org/keys", Enabled: true,
		Pool: []store.PoolServer{{URL: "vless://00000000-0000-0000-0000-000000000000@1.1.1.1:443?security=none#a"}},
	})
	snap.Rules[0].TunnelID = "pppp"
	snap.Rules[0].ViaTunnelID = "wwww"

	tag := ChainTag("wwww", "pppp")
	for _, e := range chainEndpoints(t, snap) {
		if e.Tag != tag {
			continue
		}
		if e.Detour != TunnelTag("pppp") {
			t.Fatalf("detour = %q, ожидался тег группы %q", e.Detour, TunnelTag("pppp"))
		}
		return
	}
	t.Fatalf("в конфиге нет второго звена цепи %q", tag)
}

// Второе звено не-WARP и второе звено в никуда — повреждённое состояние, а не
// повод молча увести трафик мимо цепи.
func TestChainRejectsNonWARP(t *testing.T) {
	snap := chainFixture()
	snap.Rules[0].ViaTunnelID = "bbbb"
	if _, err := Generate(snap); err == nil {
		t.Fatal("ожидалась ошибка на второе звено, которое не WARP")
	}

	snap = chainFixture()
	snap.Rules[0].ViaTunnelID = "нет такого"
	if _, err := Generate(snap); err == nil {
		t.Fatal("ожидалась ошибка на второе звено в никуда")
	}
}

// Выключенное второе звено обрывает цепь: правило отказывает, а не уходит одним
// первым звеном — иначе ресурс увидел бы не тот адрес. И не выпадает из конфига:
// тогда его трафик ушёл бы вовсе мимо туннелей, напрямую (ADR 0013).
func TestChainRejectsWhenSecondHopDisabled(t *testing.T) {
	snap := chainFixture()
	snap.Rules[0].ViaTunnelID = "wwww"
	snap.Tunnels[len(snap.Tunnels)-1].Enabled = false

	opts := mustGenerate(t, snap)
	assertRejected(t, opts, ruleSetTag("r1"))
	for _, e := range chainEndpoints(t, snap) {
		if strings.HasPrefix(e.Tag, "chain-") {
			t.Errorf("оборванная цепь оставила в конфиге звено %q", e.Tag)
		}
	}
}

// Эталон с цепью: его же проверяет настоящий sing-box в CI — golden-тесты
// сравнивают вывод с нашими ожиданиями, а не с тем, что примет рантайм.
// Два правила ссылаются на одну пару, и в эталоне видно, что клон один.
func TestChainGolden(t *testing.T) {
	snap := chainFixture()
	snap.Rules[0].ViaTunnelID = "wwww"
	snap.Rules[1].TunnelID = "aaaa"
	snap.Rules[1].ViaTunnelID = "wwww"
	golden(t, "chain.json", generate(t, snap))
}
