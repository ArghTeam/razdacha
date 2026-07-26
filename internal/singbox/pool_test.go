package singbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	"github.com/ArghTeam/razdacha/internal/store"
)

// poolFixture — снимок с одним туннелем-пулом и правилом на него.
func poolFixture(servers []store.PoolServer) store.Snapshot {
	return store.Snapshot{
		Settings: store.DefaultSettings(),
		Tunnels: []store.Tunnel{{
			ID: "pppp", Name: "Пул vless", Type: store.TunnelVLESS,
			Source: store.SourcePool, Raw: "https://vpnkeys.me/protocol/vless",
			Enabled: true, Pool: servers,
		}},
		Rules: []store.Rule{{
			ID: "r1", Name: "YouTube", Action: store.ActionTunnel,
			TunnelID: "pppp", Priority: 0, Enabled: true,
			CommunityLists: []string{"youtube"},
			PeerScope:      store.ScopeAll,
		}},
	}
}

// poolServers собирает набор из корпуса живых ключей с заданными пингами.
func poolServers(t *testing.T, n int) []store.PoolServer {
	t.Helper()
	urls := corpusURLs(t)
	if len(urls) < n {
		t.Fatalf("в корпусе %d ключей, нужно %d", len(urls), n)
	}
	out := make([]store.PoolServer, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, store.PoolServer{
			URL:     urls[i],
			Country: "Нидерланды",
			Title:   fmt.Sprintf("сервер %d", i),
			// Пинг убывает с номером: лучшими окажутся последние в списке, то
			// есть отбор точно не сводится к «взять первые».
			PingMS: 1000 - i,
		})
	}
	return out
}

func corpusURLs(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile("testdata/vpnkeys-vless-urls.txt")
	if err != nil {
		t.Fatalf("чтение корпуса ключей: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// urltestOf находит группу пула в конфиге.
func urltestOf(t *testing.T, opts option.Options, tunnelID string) *option.URLTestOutboundOptions {
	t.Helper()
	for _, ob := range opts.Outbounds {
		if ob.Tag != TunnelTag(tunnelID) {
			continue
		}
		if ob.Type != C.TypeURLTest {
			t.Fatalf("тег туннеля занял outbound типа %q, а не urltest", ob.Type)
		}
		o, ok := ob.Options.(*option.URLTestOutboundOptions)
		if !ok {
			t.Fatalf("у группы пула не те опции: %T", ob.Options)
		}
		return o
	}
	return nil
}

// Пул разворачивается в участников плюс группу под тегом туннеля — тем самым, на
// который ссылается правило (ADR 0010).
func TestPoolBuildsGroupUnderTunnelTag(t *testing.T) {
	snap := poolFixture(poolServers(t, 3))
	opts := mustGenerate(t, snap)

	group := urltestOf(t, opts, "pppp")
	if group == nil {
		t.Fatal("группы пула нет в конфиге")
	}
	if len(group.Outbounds) != 3 {
		t.Fatalf("в группе %d участников, ожидалось 3: %v", len(group.Outbounds), group.Outbounds)
	}
	if group.Interval == 0 || group.IdleTimeout == 0 || group.Tolerance == 0 {
		t.Errorf("параметры проверки не заданы: %+v", group)
	}
	if group.Interval >= group.IdleTimeout {
		t.Errorf("интервал проверки %v не меньше idle_timeout %v", group.Interval, group.IdleTimeout)
	}

	// Каждый участник — vless-outbound с тегом из группы, и ни один не носит тег
	// самого туннеля: иначе правило уехало бы в один сервер вместо группы.
	tags := make(map[string]string)
	for _, ob := range opts.Outbounds {
		tags[ob.Tag] = ob.Type
	}
	for _, tag := range group.Outbounds {
		typ, ok := tags[tag]
		if !ok {
			t.Errorf("участник %q объявлен в группе, но outbound'а с таким тегом нет", tag)
		}
		if typ != C.TypeVLESS {
			t.Errorf("участник %q имеет тип %q, ожидался vless", tag, typ)
		}
		if !strings.HasPrefix(tag, TunnelTag("pppp")+"-") {
			t.Errorf("тег участника %q не производный от тега туннеля", tag)
		}
	}

	// Правило по-прежнему ссылается на один тег.
	found := false
	for _, r := range opts.Route.Rules {
		if r.DefaultOptions.RuleAction.RouteOptions.Outbound == TunnelTag("pppp") {
			found = true
		}
	}
	if !found {
		t.Error("правило не ссылается на тег туннеля-пула")
	}
}

// Неизменившийся набор серверов обязан давать байт-в-байт тот же конфиг: иначе
// sameOnDisk не совпадает и sing-box перезапускается на каждом обновлении каталога.
func TestPoolSerializationIsStable(t *testing.T) {
	servers := poolServers(t, 8)

	first := generate(t, poolFixture(servers))

	// Тот же набор, но пришедший из БД в другом порядке и с другими подписями:
	// на конфиг влияют только ссылки.
	shuffled := make([]store.PoolServer, len(servers))
	for i, s := range servers {
		s.Title = "другая подпись"
		s.Country = "Германия"
		shuffled[len(servers)-1-i] = s
	}
	second := generate(t, poolFixture(shuffled))

	if string(first) != string(second) {
		t.Fatal("порядок серверов в БД просочился в конфиг — обновление каталога будет перезапускать sing-box")
	}
}

// Потолок числа серверов соблюдается, и в конфиг попадают лучшие по пингу.
func TestPoolCapsServers(t *testing.T) {
	servers := poolServers(t, poolMaxServers+9)
	opts := mustGenerate(t, poolFixture(servers))

	group := urltestOf(t, opts, "pppp")
	if group == nil {
		t.Fatal("группы пула нет в конфиге")
	}
	if len(group.Outbounds) != poolMaxServers {
		t.Fatalf("в группе %d участников, потолок %d", len(group.Outbounds), poolMaxServers)
	}

	// Пинг в poolServers убывает с номером, значит отобраться должны последние.
	selected := selectPoolServers(servers)
	best := make(map[string]bool, len(selected))
	for _, s := range selected {
		best[s.URL] = true
	}
	for _, s := range servers[:9] {
		if best[s.URL] {
			t.Errorf("в отбор попал сервер с пингом %d, хотя есть быстрее", s.PingMS)
		}
	}
}

// Пул без пригодных серверов не ломает генерацию всего конфига: он выпадает
// вместе со своими правилами, остальные туннели остаются.
func TestPoolWithoutServersIsSkipped(t *testing.T) {
	cases := map[string][]store.PoolServer{
		"пустой пул": nil,
		"ссылки не разбираются": {
			{URL: "vless://"},
			{URL: "мусор"},
		},
	}
	for name, servers := range cases {
		t.Run(name, func(t *testing.T) {
			snap := poolFixture(servers)
			// Рядом — исправный туннель со своим правилом: он обязан выжить.
			snap.Tunnels = append(snap.Tunnels, store.Tunnel{
				ID: "bbbb", Name: "Франкфурт", Type: store.TunnelVLESS,
				Source: store.SourceURL, Raw: "vless://…", Enabled: true,
				Parsed: []byte(`{"server":"5.6.7.8","server_port":443,
					"uuid":"00000000-0000-0000-0000-000000000000"}`),
			})
			snap.Rules = append(snap.Rules, store.Rule{
				ID: "r2", Name: "Соцсети", Action: store.ActionTunnel,
				TunnelID: "bbbb", Priority: 1, Enabled: true,
				CommunityLists: []string{"meta"},
				PeerScope:      store.ScopeAll,
			})

			opts := mustGenerate(t, snap)
			if group := urltestOf(t, opts, "pppp"); group != nil {
				t.Error("пул без серверов попал в конфиг")
			}
			for _, r := range opts.Route.Rules {
				if r.DefaultOptions.RuleAction.RouteOptions.Outbound == TunnelTag("pppp") {
					t.Error("правило ссылается на тег, которого в конфиге нет")
				}
			}
			if !hasOutbound(opts, TunnelTag("bbbb")) {
				t.Error("исправный туннель выпал из конфига вместе с пулом")
			}
		})
	}
}

// Выключенный пул в конфиг не попадает вовсе.
func TestPoolDisabled(t *testing.T) {
	snap := poolFixture(poolServers(t, 3))
	snap.Tunnels[0].Enabled = false
	opts := mustGenerate(t, snap)

	if group := urltestOf(t, opts, "pppp"); group != nil {
		t.Error("выключенный пул попал в конфиг")
	}
	for _, ob := range opts.Outbounds {
		if strings.HasPrefix(ob.Tag, TunnelTag("pppp")) {
			t.Errorf("участник выключенного пула попал в конфиг: %q", ob.Tag)
		}
	}
}

// Весь корпус живых ключей каталога разбирается и сериализуется: разъехавшийся
// парсер proxy-URL молча выкинул бы серверы из пула.
func TestPoolCorpusParses(t *testing.T) {
	urls := corpusURLs(t)
	servers := make([]store.PoolServer, 0, len(urls))
	for i, u := range urls {
		servers = append(servers, store.PoolServer{URL: u, PingMS: i + 1})
	}

	group, ok := buildPool(store.Tunnel{
		ID: "pppp", Name: "Пул vless", Source: store.SourcePool, Pool: servers,
	}, testLogger())
	if !ok {
		t.Fatal("пул из корпуса ключей оказался пустым")
	}
	if len(group) != poolMaxServers+1 {
		t.Fatalf("в группе %d записей, ожидалось %d участников плюс urltest",
			len(group), poolMaxServers)
	}

	// Каждый отобранный ключ обязан разобраться: пропуск здесь означал бы, что
	// потолок набирается не лучшими серверами, а первыми разобравшимися.
	for _, s := range selectPoolServers(servers) {
		if _, err := Parse(s.URL); err != nil {
			t.Errorf("ключ корпуса не разобран: %s: %v", s.URL, err)
		}
	}
}

// Обновление каталога, не изменившее набор серверов, не перезапускает sing-box:
// перезагрузка идёт через reload-or-restart и рвёт соединения во всех туннелях.
func TestPoolApplyWithoutReload(t *testing.T) {
	servers := poolServers(t, 8)
	check, reload := &fakeChecker{}, &fakeReloader{}
	a, _ := applier(t, check, reload)

	res, err := a.Apply(context.Background(), poolFixture(servers))
	if err != nil {
		t.Fatalf("первое применение: %v", err)
	}
	if !res.Changed || !res.Reloaded {
		t.Fatalf("первое применение: changed=%v reloaded=%v", res.Changed, res.Reloaded)
	}

	// Тот же набор, пришедший из каталога в другом порядке и с другим пингом.
	again := make([]store.PoolServer, len(servers))
	for i, s := range servers {
		s.PingMS += 137
		again[len(servers)-1-i] = s
	}
	res, err = a.Apply(context.Background(), poolFixture(again))
	if err != nil {
		t.Fatalf("повторное применение: %v", err)
	}
	if res.Changed || res.Reloaded {
		t.Fatalf("неизменившийся набор перезапустил sing-box: changed=%v reloaded=%v",
			res.Changed, res.Reloaded)
	}
	if reload.calls != 1 {
		t.Fatalf("reload вызван %d раз, ожидался один", reload.calls)
	}
}

// testLogger глушит предупреждения о пропущенных серверах: в тестах они ожидаемы.
func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func hasOutbound(opts option.Options, tag string) bool {
	for _, ob := range opts.Outbounds {
		if ob.Tag == tag {
			return true
		}
	}
	return false
}
