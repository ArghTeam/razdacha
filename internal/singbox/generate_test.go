package singbox

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	"github.com/ArghTeam/razdacha/internal/store"
)

var update = flag.Bool("update", false, "перезаписать эталоны в testdata")

// fixture — состояние, покрывающее все ветки генератора: WireGuard-туннель в
// endpoints, VLESS в outbounds, правила всех трёх действий, community-список,
// свои домены и подсети, внешний .srs, правило для выбранных пиров и правило на
// выключенный туннель — оно попадает в конфиг отказом (ADR 0013).
func fixture() store.Snapshot {
	s := store.DefaultSettings()
	s.ListUpdateInterval = 24 * time.Hour
	return store.Snapshot{
		Settings: s,
		Tunnels: []store.Tunnel{
			{
				ID: "aaaa", Name: "Амстердам", Type: store.TunnelWireGuard,
				Source: store.SourceWGConf, Raw: "[Interface]", Enabled: true,
				Parsed: json.RawMessage(`{
					"address": ["10.2.0.2/32"],
					"private_key": "cHJpdmF0ZQ==",
					"peers": [{
						"address": "1.2.3.4", "port": 51820,
						"public_key": "cHVibGlj", "allowed_ips": ["0.0.0.0/0"]
					}]
				}`),
			},
			{
				ID: "bbbb", Name: "Франкфурт", Type: store.TunnelVLESS,
				Source: store.SourceURL, Raw: "vless://…", Enabled: true,
				Parsed: json.RawMessage(`{
					"server": "5.6.7.8", "server_port": 443,
					"uuid": "00000000-0000-0000-0000-000000000000", "flow": "xtls-rprx-vision"
				}`),
			},
			{
				ID: "cccc", Name: "Выключенный", Type: store.TunnelTrojan,
				Source: store.SourceURL, Raw: "trojan://…", Enabled: false,
				Parsed: json.RawMessage(`{"server": "9.9.9.9", "server_port": 443, "password": "x"}`),
			},
		},
		Rules: []store.Rule{
			{
				ID: "r1", Name: "YouTube и Google", Action: store.ActionTunnel,
				TunnelID: "aaaa", Priority: 0, Enabled: true,
				CommunityLists: []string{"youtube", "google"},
				Domains:        []string{"rutracker.org"},
				PeerScope:      store.ScopeAll,
			},
			{
				ID: "r2", Name: "Рабочий ноутбук в Франкфурт", Action: store.ActionTunnel,
				TunnelID: "bbbb", Priority: 1, Enabled: true,
				CommunityLists: []string{"youtube"},
				Subnets:        []string{"104.16.0.0/13"},
				RemoteLists:    []string{"https://example.org/my.srs", "https://example.org/plain.lst"},
				PeerScope:      store.ScopeSelected,
				PeerIDs:        []string{"p1"},
			},
			{
				ID: "r3", Name: "Банки напрямую", Action: store.ActionDirect,
				Priority: 2, Enabled: true,
				Domains:   []string{"sberbank.ru"},
				PeerScope: store.ScopeAll,
			},
			{
				ID: "r4", Name: "Реклама", Action: store.ActionBlock,
				Priority: 3, Enabled: true,
				Domains:   []string{"doubleclick.net"},
				PeerScope: store.ScopeAll,
			},
			{
				ID: "r5", Name: "Выключенное", Action: store.ActionDirect,
				Priority: 4, Enabled: false,
				Domains:   []string{"example.com"},
				PeerScope: store.ScopeAll,
			},
			{
				ID: "r6", Name: "В выключенный туннель", Action: store.ActionTunnel,
				TunnelID: "cccc", Priority: 5, Enabled: true,
				Domains:   []string{"habr.com"},
				PeerScope: store.ScopeAll,
			},
		},
		Peers: []store.Peer{
			{
				ID: "p1", Name: "Ноутбук", PublicKey: "pub", PrivateKey: "priv",
				PresharedKey: "psk", Address: "10.8.0.2", Enabled: true,
			},
			{
				ID: "p2", Name: "Телефон", PublicKey: "pub2", PrivateKey: "priv2",
				PresharedKey: "psk2", Address: "10.8.0.3", Enabled: true,
			},
		},
	}
}

func generate(t *testing.T, snap store.Snapshot) []byte {
	t.Helper()
	opts, err := Generate(snap)
	if err != nil {
		t.Fatalf("генерация: %v", err)
	}
	b, err := Marshal(opts)
	if err != nil {
		t.Fatalf("сериализация: %v", err)
	}
	return b
}

// golden сравнивает результат с эталоном. Ни сеть, ни root не нужны: генератор —
// чистая функция от снимка состояния.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("запись эталона: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение эталона: %v (перезаписать: go test ./internal/singbox -update)", err)
	}
	if string(got) != string(want) {
		t.Errorf("конфиг разошёлся с %s\n--- получено ---\n%s", path, got)
	}
}

func TestGenerateGolden(t *testing.T) {
	golden(t, "full.json", generate(t, fixture()))
}

func TestGenerateEmpty(t *testing.T) {
	golden(t, "empty.json", generate(t, store.Snapshot{Settings: store.DefaultSettings()}))
}

func TestGenerateDeterministic(t *testing.T) {
	first := generate(t, fixture())
	second := generate(t, fixture())
	if string(first) != string(second) {
		t.Fatal("два прогона на одном состоянии дали разный конфиг")
	}
}

// TestRuleOrder: порядок route.rules определяется priority, а не порядком в
// снимке — первое совпадение выигрывает.
func TestRuleOrder(t *testing.T) {
	snap := fixture()
	snap.Rules[0], snap.Rules[3] = snap.Rules[3], snap.Rules[0]
	golden(t, "full.json", generate(t, snap))
}

// TestUnavailableTunnelRejects держит ADR 0013: правило, которому некуда идти,
// остаётся в конфиге отказом — и в route.rules, и в dns.rules. Выпади оно, его
// трафик забрал бы route.final, то есть ушёл бы напрямую с адреса сервера, а
// домены без DNS-записи резолвились бы в настоящие адреса.
func TestUnavailableTunnelRejects(t *testing.T) {
	baseline := len(mustGenerate(t, fixture()).Route.Rules)
	tests := []struct {
		name string
		mut  func(*store.Snapshot)
	}{
		{
			name: "туннель выключен",
			mut: func(s *store.Snapshot) {
				s.Rules[0].TunnelID = "cccc"
			},
		},
		{
			name: "у туннеля-пула нет пригодных серверов",
			mut: func(s *store.Snapshot) {
				s.Tunnels = append(s.Tunnels, store.Tunnel{
					ID: "pppp", Name: "Пул", Type: store.TunnelVLESS,
					Source: store.SourcePool, Raw: "https://example.org/keys", Enabled: true,
				})
				s.Rules[0].TunnelID = "pppp"
			},
		},
		{
			name: "все выбранные пиры выключены",
			mut: func(s *store.Snapshot) {
				s.Rules[0].PeerScope = store.ScopeSelected
				s.Rules[0].PeerIDs = []string{"p2"}
				s.Peers[1].Enabled = false
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := fixture()
			tt.mut(&snap)
			opts := mustGenerate(t, snap)
			if len(opts.Route.Rules) != baseline {
				t.Fatalf("правил %d, ожидалось %d: правило выпало из конфига",
					len(opts.Route.Rules), baseline)
			}
			assertRejected(t, opts, ruleSetTag("r1"))
		})
	}
}

// assertRejected: правило с набором tag есть в обоих наборах правил и в обоих
// отказывает, никуда не маршрутизируясь.
func assertRejected(t *testing.T, opts option.Options, tag string) {
	t.Helper()
	var inRoute, inDNS bool
	for _, r := range opts.Route.Rules {
		if !contains(r.DefaultOptions.RuleSet, tag) {
			continue
		}
		inRoute = true
		if r.DefaultOptions.Action != C.RuleActionTypeReject {
			t.Errorf("route.rules: действие %q, ожидался reject", r.DefaultOptions.Action)
		}
		if out := r.DefaultOptions.RouteOptions.Outbound; out != "" {
			t.Errorf("route.rules: правило ушло в %q вместо отказа", out)
		}
	}
	for _, r := range opts.DNS.Rules {
		if !contains(r.DefaultOptions.RuleSet, tag) {
			continue
		}
		inDNS = true
		if r.DefaultOptions.Action != C.RuleActionTypeReject {
			t.Errorf("dns.rules: действие %q, ожидался reject", r.DefaultOptions.Action)
		}
	}
	if !inRoute {
		t.Error("правила нет в route.rules — его трафик заберёт final, то есть прямой выход")
	}
	if !inDNS {
		t.Error("правила нет в dns.rules — его домены резолвятся в настоящие адреса")
	}
}

// Правило без единого условия совпадения — единственный оставшийся пропуск: его
// не выразить ни маршрутом, ни отказом, потому что совпадать оно будет со всем.
func TestRuleWithoutConditionsSkipped(t *testing.T) {
	baseline := len(mustGenerate(t, fixture()).Route.Rules)
	snap := fixture()
	snap.Rules[0].CommunityLists = nil
	snap.Rules[0].Domains = nil

	opts := mustGenerate(t, snap)
	for _, r := range opts.Route.Rules {
		if contains(r.DefaultOptions.RuleSet, ruleSetTag("r1")) {
			t.Fatal("правило без условий попало в конфиг")
		}
	}
	if len(opts.Route.Rules) != baseline-1 {
		t.Fatalf("правил %d, ожидалось %d", len(opts.Route.Rules), baseline-1)
	}
}

// Отказ правилу, у которого выключены все выбранные пиры, достаётся ровно этим
// пирам: пустой source_ip_cidr означал бы «для всех» и оборвал бы чужой трафик.
func TestRejectKeepsDisabledPeerSources(t *testing.T) {
	snap := fixture()
	snap.Rules[0].PeerScope = store.ScopeSelected
	snap.Rules[0].PeerIDs = []string{"p2"}
	snap.Peers[1].Enabled = false

	opts := mustGenerate(t, snap)
	for _, r := range opts.Route.Rules {
		if !contains(r.DefaultOptions.RuleSet, ruleSetTag("r1")) {
			continue
		}
		if !reflect.DeepEqual([]string(r.DefaultOptions.SourceIPCIDR), []string{"10.8.0.3/32"}) {
			t.Fatalf("source_ip_cidr = %v, ожидался адрес выбранного пира",
				r.DefaultOptions.SourceIPCIDR)
		}
		return
	}
	t.Fatal("правила нет в route.rules")
}

// TestDNSRulesPerAction: FakeIP получают только правила в туннель, block — отказ
// на DNS (иначе его домены резолвятся в настоящие адреса и уходят мимо блокировки),
// direct и resolve_real_ip — ничего.
func TestDNSRulesPerAction(t *testing.T) {
	snap := fixture()
	snap.Rules[1].ResolveRealIP = true
	opts := mustGenerate(t, snap)

	got := make(map[string]string)
	for _, r := range opts.DNS.Rules {
		for _, tag := range r.DefaultOptions.RuleSet {
			if !strings.HasPrefix(tag, "rule-") {
				continue
			}
			got[tag] = r.DefaultOptions.Action + ":" +
				r.DefaultOptions.RouteOptions.Server
		}
	}
	want := map[string]string{
		ruleSetTag("r1"): "route:" + TagDNSFakeIP,
		ruleSetTag("r4"): "reject:",
		ruleSetTag("r6"): "reject:", // туннель выключен — отказ, а не пропуск
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DNS-правила: %v, ожидалось %v", got, want)
	}
}

// TestParsedFeedsGenerate держит стык двух половин слоя: то, что [Parse] кладёт в
// Tunnel.Parsed, генератор обязан принимать без доработки напильником. Тег и type
// в конфиг проставляет генератор, поэтому из parsed они выбрасываются.
func TestParsedFeedsGenerate(t *testing.T) {
	wg, err := os.ReadFile(filepath.Join("testdata", "wireguard-minimal.conf"))
	if err != nil {
		t.Fatalf("чтение фикстуры WireGuard: %v", err)
	}
	inputs := []string{
		"vless://00000000-0000-0000-0000-000000000000@5.6.7.8:443?security=tls&sni=example.org#Франкфурт",
		string(wg),
	}

	snap := store.Snapshot{Settings: store.DefaultSettings()}
	for i, raw := range inputs {
		res, err := Parse(raw)
		if err != nil {
			t.Fatalf("разбор входа %d: %v", i, err)
		}
		id := fmt.Sprintf("t%d", i)
		snap.Tunnels = append(snap.Tunnels, store.Tunnel{
			ID: id, Name: id, Type: res.Type, Source: res.Source,
			Raw: raw, Parsed: res.Parsed, Enabled: true,
		})
		snap.Rules = append(snap.Rules, store.Rule{
			ID: "r" + id, Name: id, Action: store.ActionTunnel, TunnelID: id,
			Priority: i, Enabled: true,
			Domains: []string{"example.org"}, PeerScope: store.ScopeAll,
		})
	}

	var cfg struct {
		Endpoints []map[string]any `json:"endpoints"`
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(generate(t, snap), &cfg); err != nil {
		t.Fatalf("разбор конфига: %v", err)
	}
	if len(cfg.Outbounds) != 2 { // direct-out и vless
		t.Fatalf("outbounds: %d, ожидалось 2", len(cfg.Outbounds))
	}
	if cfg.Outbounds[1]["type"] != "vless" || cfg.Outbounds[1]["tag"] != TunnelTag("t0") {
		t.Errorf("outbound = %v", cfg.Outbounds[1])
	}
	if len(cfg.Endpoints) != 1 {
		t.Fatalf("endpoints: %d, ожидался 1", len(cfg.Endpoints))
	}
	if cfg.Endpoints[0]["type"] != "wireguard" || cfg.Endpoints[0]["tag"] != TunnelTag("t1") {
		t.Errorf("endpoint = %v", cfg.Endpoints[0])
	}
}

func TestMissingTunnelIsError(t *testing.T) {
	snap := fixture()
	snap.Rules[0].TunnelID = "нет такого"
	if _, err := Generate(snap); err == nil {
		t.Fatal("ожидалась ошибка на ссылку в никуда")
	}
}

func TestDisabledPeerNotInSources(t *testing.T) {
	snap := fixture()
	snap.Rules[1].PeerIDs = []string{"p1", "p2"}
	snap.Peers[1].Enabled = false
	opts, err := Generate(snap)
	if err != nil {
		t.Fatalf("генерация: %v", err)
	}
	for _, r := range opts.Route.Rules {
		if contains(r.DefaultOptions.SourceIPCIDR, "10.8.0.3/32") {
			t.Fatal("адрес выключенного пира попал в source_ip_cidr")
		}
	}
}

// TestIPv4Only держит первый слой отключения IPv6 (ADR 0005): стратегия ipv4_only
// и FakeIP без inet6_range.
func TestIPv4Only(t *testing.T) {
	var cfg struct {
		DNS struct {
			Strategy string `json:"strategy"`
			Servers  []struct {
				Type       string `json:"type"`
				Inet4Range string `json:"inet4_range"`
				Inet6Range string `json:"inet6_range"`
			} `json:"servers"`
		} `json:"dns"`
	}
	if err := json.Unmarshal(generate(t, fixture()), &cfg); err != nil {
		t.Fatalf("разбор конфига: %v", err)
	}
	if cfg.DNS.Strategy != "ipv4_only" {
		t.Errorf("strategy = %q, ожидалось ipv4_only", cfg.DNS.Strategy)
	}
	var seen bool
	for _, s := range cfg.DNS.Servers {
		if s.Type != "fakeip" {
			continue
		}
		seen = true
		if s.Inet4Range != fakeIPRange.String() {
			t.Errorf("inet4_range = %q, ожидалось %s", s.Inet4Range, fakeIPRange)
		}
		if s.Inet6Range != "" {
			t.Errorf("объявлен inet6_range = %q", s.Inet6Range)
		}
	}
	if !seen {
		t.Error("в конфиге нет сервера fakeip")
	}
}

// TestWireGuardIsUserspace держит ADR 0002: WireGuard живёт в endpoints и не
// привязывается к kernel-интерфейсу.
func TestWireGuardIsUserspace(t *testing.T) {
	var cfg struct {
		Endpoints []struct {
			Type          string `json:"type"`
			Tag           string `json:"tag"`
			BindInterface string `json:"bind_interface"`
			System        bool   `json:"system"`
		} `json:"endpoints"`
		Outbounds []struct {
			Type string `json:"type"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(generate(t, fixture()), &cfg); err != nil {
		t.Fatalf("разбор конфига: %v", err)
	}
	if len(cfg.Endpoints) != 1 {
		t.Fatalf("endpoints: получено %d, ожидался 1", len(cfg.Endpoints))
	}
	e := cfg.Endpoints[0]
	if e.Type != "wireguard" || e.Tag != TunnelTag("aaaa") {
		t.Errorf("endpoint = %+v", e)
	}
	if e.BindInterface != "" || e.System {
		t.Error("WireGuard привязан к интерфейсу ядра")
	}
	for _, o := range cfg.Outbounds {
		if o.Type == "wireguard" {
			t.Error("WireGuard оказался в outbounds")
		}
	}
}

func mustGenerate(t *testing.T, snap store.Snapshot) option.Options {
	t.Helper()
	opts, err := Generate(snap)
	if err != nil {
		t.Fatalf("генерация: %v", err)
	}
	return opts
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
