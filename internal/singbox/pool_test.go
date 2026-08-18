package singbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	"github.com/ArghTeam/razdacha/internal/lists"
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
		if r.DefaultOptions.RouteOptions.Outbound == TunnelTag("pppp") {
			found = true
		}
	}
	if !found {
		t.Error("правило не ссылается на тег туннеля-пула")
	}
}

// Набор серверов, не изменившийся в БД, обязан давать байт-в-байт тот же конфиг:
// иначе sameOnDisk не совпадает и sing-box перезапускается на каждом обновлении
// каталога. Пинг и подписи карточек на конфиг не влияют — в него идут только ссылки.
func TestPoolSerializationIsStable(t *testing.T) {
	servers := poolServers(t, 8)

	first := generate(t, poolFixture(servers))

	same := make([]store.PoolServer, len(servers))
	for i, s := range servers {
		s.Title = "другая подпись"
		s.Country = "Германия"
		s.PingMS += 137
		s.Misses = 1
		same[i] = s
	}
	second := generate(t, poolFixture(same))

	if string(first) != string(second) {
		t.Fatal("пинг и подпись карточки просочились в конфиг — обновление каталога будет перезапускать sing-box")
	}
}

// Отбор идёт по порядку в БД, а не пересортировкой по пингу: порядок — это приоритет,
// который ведёт слой lists, и позиция в нём равна тегу участника. Пересортировка здесь
// меняла бы состав конфига от шума измерений чужого сайта (issue #68).
func TestPoolSelectsInStoredOrder(t *testing.T) {
	servers := poolServers(t, poolMaxServers+9)

	got := selectPoolServers(servers)
	if len(got) != poolMaxServers {
		t.Fatalf("отобрано %d серверов, потолок %d", len(got), poolMaxServers)
	}
	for i, s := range got {
		if s.URL != servers[i].URL {
			t.Fatalf("на позиции %d сервер %q, в БД на ней %q", i, s.URL, servers[i].URL)
		}
	}
}

// Потолок числа серверов соблюдается: в конфиг идут первые poolMaxServers штук.
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
}

// Окно конфига одно и то же по обе стороны границы слоёв: слияние в lists держит в
// начале списка ровно тех, кого генератор потом возьмёт в конфиг. Разъехались — окно
// поедет на каждом обходе каталога вместе с тегами участников.
func TestPoolConfigWindowAgrees(t *testing.T) {
	if lists.PoolConfigServers != poolMaxServers {
		t.Fatalf("окно конфига разъехалось: lists.PoolConfigServers = %d, poolMaxServers = %d",
			lists.PoolConfigServers, poolMaxServers)
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
				if r.DefaultOptions.RouteOptions.Outbound == TunnelTag("pppp") {
					t.Error("правило ссылается на тег, которого в конфиге нет")
				}
			}
			if !hasOutbound(opts, TunnelTag("bbbb")) {
				t.Error("исправный туннель выпал из конфига вместе с пулом")
			}
		})
	}
}

// Ключ из каталога с `allowInsecure=1` попадает в конфиг с проверкой сертификата:
// согласие на перехват в туннеле никто не давал, ссылку демон взял сам с чужой
// страницы (issue #128). Ручная вставка того же ключа поведения не меняет.
func TestPoolIgnoresInsecureFromCatalog(t *testing.T) {
	const link = "vless://11111111-2222-3333-4444-555555555555@203.0.113.7:443" +
		"?security=tls&sni=example.org&allowInsecure=1#NL-1"

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	group, ok := buildPool(store.Tunnel{
		ID: "pppp", Name: "Пул", Pool: []store.PoolServer{{URL: link, Title: "VLESS (NL) 1"}},
	}, log)
	if !ok {
		t.Fatal("пул не собрался")
	}

	var member *option.VLESSOutboundOptions
	for _, ob := range group {
		if ob.Tag != poolMemberTag("pppp", 0) {
			continue
		}
		o, isVLESS := ob.Options.(*option.VLESSOutboundOptions)
		if !isVLESS {
			t.Fatalf("у участника пула не те опции: %T", ob.Options)
		}
		member = o
	}
	if member == nil {
		t.Fatal("участника пула нет в группе")
	}
	if member.TLS == nil || !member.TLS.Enabled {
		t.Fatal("у участника пула нет TLS")
	}
	if member.TLS.Insecure {
		t.Error("ключ из каталога отключил проверку сертификата")
	}
	if !strings.Contains(buf.String(), "проверка оставлена включённой") {
		t.Errorf("в логе нет следа снятого флага: %s", buf.String())
	}

	// Тот же ключ, вставленный руками: разбор одинаков для всех источников, и
	// гасить флаг в парсере нельзя — там источник неизвестен.
	res, err := Parse(link)
	if err != nil {
		t.Fatalf("разбор ссылки: %v", err)
	}
	manual, isVLESS := res.Outbound.Options.(*option.VLESSOutboundOptions)
	if !isVLESS {
		t.Fatalf("не те опции при ручном разборе: %T", res.Outbound.Options)
	}
	if manual.TLS == nil || !manual.TLS.Insecure {
		t.Error("ручная вставка потеряла allowInsecure — это решение пользователя")
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

// crawl изображает один обход каталога: карточки приходят в другом порядке, с другим
// пингом и другими подписями, а часть ссылок не приходит вовсе. Так ведёт себя любой
// каталог: состав страницы от обхода к обходу свой, и часть карточек не отдаётся.
func crawl(servers []store.PoolServer, skip ...string) []store.PoolServer {
	skipped := make(map[string]bool, len(skip))
	for _, u := range skip {
		skipped[u] = true
	}
	out := make([]store.PoolServer, 0, len(servers))
	for i := len(servers) - 1; i >= 0; i-- {
		s := servers[i]
		if skipped[s.URL] {
			continue
		}
		s.PingMS += 137
		s.Title = "подпись этого обхода"
		out = append(out, s)
	}
	return out
}

// urlsByTag — какой сервер стоит за каждым тегом участника группы.
func urlsByTag(t store.Tunnel) map[string]string {
	out := make(map[string]string)
	for tag, s := range PoolMembers(t) {
		out[tag] = s.URL
	}
	return out
}

// Обход каталога, отличающийся от предыдущего на несколько ссылок, не перезапускает
// sing-box: перезагрузка идёт через reload-or-restart и рвёт соединения во всех
// туннелях. Набор ссылок каталога пляшет на каждом запросе, и совпадающий обход —
// случай, которого на живом сайте не бывает (issue #68). Слияние идёт через
// lists.MergePool: повторять его логику в тесте значило бы дать ей разъехаться с той,
// что работает в демоне.
func TestPoolApplyWithoutReloadOnCatalogDrift(t *testing.T) {
	catalog := poolServers(t, poolMaxServers+4)
	check, reload := &fakeChecker{}, &fakeReloader{}
	a, _ := applier(t, check, reload)

	stored, changed := lists.MergePool(nil, crawl(catalog))
	if !changed || len(stored) != len(catalog) {
		t.Fatalf("первый обход дал %d серверов, changed=%v", len(stored), changed)
	}
	res, err := a.Apply(context.Background(), poolFixture(stored))
	if err != nil {
		t.Fatalf("первое применение: %v", err)
	}
	if !res.Changed || !res.Reloaded {
		t.Fatalf("первое применение: changed=%v reloaded=%v", res.Changed, res.Reloaded)
	}
	before := urlsByTag(poolFixture(stored).Tunnels[0])

	// Второй обход: одна ссылка каталога не пришла, зато пришла новая — и с лучшим
	// пингом, чем у всех известных. Ни то, ни другое не имеет права трогать окно
	// конфига: место в нём занято живыми серверами.
	newcomer := store.PoolServer{
		URL:     corpusURLs(t)[len(catalog)],
		Country: "Финляндия",
		Title:   "новая карточка",
		PingMS:  1,
	}
	fresh := append(crawl(catalog, stored[len(stored)-1].URL), newcomer)

	next, changed := lists.MergePool(stored, fresh)
	if !changed {
		t.Fatal("новая ссылка каталога не попала в БД")
	}
	res, err = a.Apply(context.Background(), poolFixture(next))
	if err != nil {
		t.Fatalf("повторное применение: %v", err)
	}
	if res.Changed || res.Reloaded {
		t.Fatalf("набор, отличающийся на пару ссылок, перезапустил sing-box: changed=%v reloaded=%v",
			res.Changed, res.Reloaded)
	}
	if reload.calls != 1 {
		t.Fatalf("reload вызван %d раз, ожидался один", reload.calls)
	}
	if got := urlsByTag(poolFixture(next).Tunnels[0]); !sameTags(got, before) {
		t.Errorf("состав группы поехал: было %v, стало %v", before, got)
	}
}

// Исчезнувший из каталога участник заменяется, и только он: остальные участники окна
// остаются на своих тегах. Сдвиг тега — это другой конфиг, то есть тот же перезапуск.
func TestPoolReplacesOnlyMissingServer(t *testing.T) {
	catalog := poolServers(t, poolMaxServers+4)
	check, reload := &fakeChecker{}, &fakeReloader{}
	a, _ := applier(t, check, reload)

	stored, _ := lists.MergePool(nil, crawl(catalog))
	if _, err := a.Apply(context.Background(), poolFixture(stored)); err != nil {
		t.Fatalf("первое применение: %v", err)
	}
	before := urlsByTag(poolFixture(stored).Tunnels[0])

	// Пропадает участник окна, а не сервер из хвоста.
	victim := stored[3]
	var victimTag string
	for tag, u := range before {
		if u == victim.URL {
			victimTag = tag
		}
	}
	if victimTag == "" {
		t.Fatalf("сервер %q не участник группы", victim.URL)
	}

	// Одиночный пропуск карточки заменой не считается: сайт отдаёт часть карточек без
	// ссылки на каждом обходе. Замена приходит после нескольких обходов подряд.
	var replaced bool
	for i := 0; i < 10 && !replaced; i++ {
		stored, _ = lists.MergePool(stored, crawl(catalog, victim.URL))
		res, err := a.Apply(context.Background(), poolFixture(stored))
		if err != nil {
			t.Fatalf("обход %d: %v", i+1, err)
		}
		replaced = res.Changed
		if i == 0 && replaced {
			t.Fatal("пропуск карточки на одном обходе сразу сменил состав конфига")
		}
	}
	if !replaced {
		t.Fatal("исчезнувший сервер так и не уступил место")
	}

	after := urlsByTag(poolFixture(stored).Tunnels[0])
	if len(after) != len(before) {
		t.Fatalf("участников стало %d, было %d", len(after), len(before))
	}
	for tag, was := range before {
		got, ok := after[tag]
		if !ok {
			t.Errorf("тег %q исчез из группы", tag)
			continue
		}
		if tag == victimTag {
			if got == was {
				t.Errorf("исчезнувший сервер остался в конфиге под тегом %q", tag)
			}
			continue
		}
		if got != was {
			t.Errorf("тег %q переехал с %q на %q", tag, was, got)
		}
	}

	// Место занимает лучший по пингу из остальных. Пинг в poolServers убывает с
	// номером, серверов на четыре больше окна, значит вне конфига остались первые
	// четыре, и лучший из них — четвёртый.
	if want := catalog[len(catalog)-poolMaxServers-1].URL; after[victimTag] != want {
		t.Errorf("место занял %q, а лучший по пингу из остальных — %q",
			after[victimTag], want)
	}
}

func sameTags(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for tag, url := range a {
		if b[tag] != url {
			return false
		}
	}
	return true
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

// В лог уходит адрес, а не ссылка: журнал демона читают шире, чем БД, а в ссылке
// UUID ключа (issue #124).
func TestPoolWarnHasNoKey(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	const link = "vless://@203.0.113.7:443#Тест" // без UUID: парсер такую не осилит
	_, ok := buildPool(store.Tunnel{
		ID: "pppp", Name: "Пул", Pool: []store.PoolServer{{URL: link, Title: "VLESS (NL) 42"}},
	}, log)
	if ok {
		t.Fatal("битая ссылка попала в конфиг")
	}

	out := buf.String()
	if strings.Contains(out, "11111111-2222-3333-4444-555555555555") || strings.Contains(out, "vless://") {
		t.Errorf("ключ уехал в лог: %s", out)
	}
	if !strings.Contains(out, "203.0.113.7:443") {
		t.Errorf("в логе нет адреса сервера: %s", out)
	}
}

// serverAddr отдаёт адрес только тогда, когда это действительно адрес: у `ss://`
// в host лежит base64 со всем содержимым ключа, и отдавать его нельзя.
func TestServerAddr(t *testing.T) {
	cases := map[string]string{
		"vless://11111111-2222-3333-4444-555555555555@203.0.113.7:443?x=1": "203.0.113.7:443",
		"trojan://пароль@example.org:8443":                                 "example.org:8443",
		"socks5://10.0.0.1:1080":                                           "10.0.0.1:1080",
		// Ссылка целиком в base64 — адреса в ней не видно, и гадать нельзя.
		"ss://YWVzLTI1Ni1nY206cGFzc0AxLjIuMy40Ojg0ODg=#tag": unknownAddr,
		"мусор":     unknownAddr,
		"vless://":  unknownAddr,
		"":          unknownAddr,
		"vless://@": unknownAddr,
	}
	for in, want := range cases {
		if got := serverAddr(in); got != want {
			t.Errorf("serverAddr(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}
