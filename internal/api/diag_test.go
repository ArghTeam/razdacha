package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"go4.org/netipx"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/store"
)

// diagNow — момент, относительно которого считается свежесть списков.
var diagNow = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

// diagHealthy — состояние исправной системы: интерфейс поднят с MTU 1280,
// таблица залита целиком, форвардинг включён, списки свежие.
func diagHealthyWG() netstack.DiagWGState {
	return netstack.DiagWGState{
		Name: "wg0", Exists: true, Up: true, MTU: 1280,
		ListenPort: store.DefaultSettings().WGListenPort,
	}
}

func diagHealthyNft() netstack.DiagNftState {
	return netstack.DiagNftState{
		Table:  netstack.NftTable,
		Exists: true,
		Chains: []string{
			netstack.ChainMangle, netstack.ChainProxy,
			netstack.ChainForward, netstack.ChainPostrouting,
		},
		Sets:          []string{netstack.SetLocalV4, netstack.SetSubnets},
		Masquerade:    true,
		MasqueradeOIf: "eth0",
		Subnets:       diagRanges("203.0.113.0/24", "198.51.100.0/24"),
	}
}

// diagRanges — интервалы сета из записей CIDR: сет диагностика читает
// содержимым, а не числом.
func diagRanges(subnets ...string) []netipx.IPRange {
	ranges, skipped := netstack.MergeSubnets(subnets)
	if skipped > 0 {
		panic("подсеть теста не разобрана")
	}
	return ranges
}

func diagHealthyLists() DiagLists {
	return DiagLists{Sources: 3, Loaded: 3, LastRefresh: diagNow.Add(-3 * time.Hour)}
}

// diagHealthySources — все источники подключены и отдают исправную систему.
func diagHealthySources() DiagSources {
	return DiagSources{
		WG: func(context.Context) (netstack.DiagWGState, error) {
			return diagHealthyWG(), nil
		},
		Nft: func(context.Context) (netstack.DiagNftState, error) {
			return diagHealthyNft(), nil
		},
		IPForward: func(context.Context) (bool, error) { return true, nil },
		Lists:     func(context.Context) (DiagLists, error) { return diagHealthyLists(), nil },
	}
}

// diagChecks выполняет сводку и раскладывает её по идентификаторам.
func diagChecks(t *testing.T, ts *testServer, cookie *http.Cookie) (map[string]check, string) {
	t.Helper()
	resp := ts.auth(t, cookie, http.MethodGet, "/api/diag", "")
	requireCode(t, resp, http.StatusOK)

	var out diagResponse
	decodeJSONBody(t, resp, &out)
	byID := make(map[string]check, len(out.Checks))
	for _, c := range out.Checks {
		byID[c.ID] = c
	}
	return byID, out.Overall
}

// TestDiagAllGreen — при подключённых источниках проверки отвечают по
// существу, а не «данных нет».
func TestDiagAllGreen(t *testing.T) {
	ts := newTestServer(t)
	ts.diag = diagHealthySources()
	cookie := ts.login(t)

	byID, _ := diagChecks(t, ts, cookie)
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/diag/run", ""), http.StatusOK)
	for _, id := range []string{"wg", "nft", "forward", "mtu", "lists", "singbox"} {
		c, ok := byID[id]
		if !ok {
			t.Errorf("проверки %q нет в сводке", id)
			continue
		}
		if c.Status != statusOK {
			t.Errorf("%s = %q (%s), ожидался ok", id, c.Status, c.Detail)
		}
		if c.Detail == "" {
			t.Errorf("%s: ok без пояснения", id)
		}
	}
}

// TestDiagRunSingleCheck — `POST /api/diag/run?check=<id>` прогоняет одну
// проверку. На этом держится показ хода в панели: строки обновляются по мере
// готовности, а не все разом в конце.
func TestDiagRunSingleCheck(t *testing.T) {
	ts := newTestServer(t)
	ts.diag = diagHealthySources()
	cookie := ts.login(t)

	sweep, _ := diagChecks(t, ts, cookie)
	if len(sweep) != len(diagCheckIDs) {
		t.Fatalf("в сводке %d проверок, объявлено %d", len(sweep), len(diagCheckIDs))
	}

	for _, id := range diagCheckIDs {
		resp := ts.auth(t, cookie, http.MethodPost, "/api/diag/run?check="+id, "")
		requireCode(t, resp, http.StatusOK)

		var out diagResponse
		decodeJSONBody(t, resp, &out)
		if len(out.Checks) != 1 {
			t.Fatalf("%s: проверок в ответе %d, ожидалась одна", id, len(out.Checks))
		}
		got := out.Checks[0]
		if got.ID != id {
			t.Errorf("запрошена %q, вернулась %q", id, got.ID)
		}
		if got.Status != sweep[id].Status {
			t.Errorf("%s: по одной %q, в сводке %q", id, got.Status, sweep[id].Status)
		}
		if out.Overall != got.Status {
			t.Errorf("%s: overall %q, ожидался статус единственной проверки %q",
				id, out.Overall, got.Status)
		}
		if out.CheckedAt.IsZero() {
			t.Errorf("%s: ответ без checked_at — панели нечего показать как время проверки", id)
		}
	}
}

// TestDiagRunUnknownCheck — опечатка в идентификаторе отвергается. Пустая
// сводка в ответ на неё читалась бы как «проверять нечего».
func TestDiagRunUnknownCheck(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodPost, "/api/diag/run?check=нет-такой", "")
	requireCode(t, resp, http.StatusBadRequest)
}

// TestDiagCheckedAt — время проверки приходит с сервера: панель показывает
// его как есть и не подставляет часы браузера.
func TestDiagCheckedAt(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodGet, "/api/diag", "")
	requireCode(t, resp, http.StatusOK)

	var out diagResponse
	decodeJSONBody(t, resp, &out)
	if out.CheckedAt.IsZero() {
		t.Fatal("GET /api/diag без checked_at")
	}
}

// TestDiagUnknownAlwaysExplained — неподключённый источник даёт unknown с
// объяснением, а не пустую строку: молчаливое «данных нет» скрывает и поломку,
// и невыполненную проверку.
func TestDiagUnknownAlwaysExplained(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	byID, overall := diagChecks(t, ts, cookie)
	if len(byID) != 7 {
		t.Fatalf("проверок %d, ожидалось 7", len(byID))
	}
	for _, id := range []string{"wg", "nft", "forward", "mtu", "lists"} {
		c := byID[id]
		if c.Status != statusUnknown {
			t.Errorf("%s = %q, ожидался unknown: источника нет", id, c.Status)
		}
		if c.Detail == "" {
			t.Errorf("%s: unknown без объяснения, чего не хватает", id)
		}
	}
	if !strings.Contains(byID["lists"].Detail, "планировщик") {
		t.Errorf("списки: %q не называет причину", byID["lists"].Detail)
	}
	// singbox отвечает всегда (конфиг собирается из состояния БД), поэтому
	// «в целом» остаётся ok: определённое сказать есть что. Проверяется здесь
	// именно то, ради чего тест писался, — каждый unknown объяснён.
	if overall != statusOK {
		t.Errorf("overall = %q, ожидался ok: singbox ответил", overall)
	}
}

// TestDiagSourceErrorExplained — сбой источника не превращается в «поломки
// нет»: статус unknown, а причина видна в detail.
func TestDiagSourceErrorExplained(t *testing.T) {
	ts := newTestServer(t)
	src := diagHealthySources()
	src.WG = func(context.Context) (netstack.DiagWGState, error) {
		return netstack.DiagWGState{}, errors.New("netlink недоступен")
	}
	ts.diag = src
	cookie := ts.login(t)

	byID, _ := diagChecks(t, ts, cookie)
	if byID["wg"].Status != statusUnknown || !strings.Contains(byID["wg"].Detail, "netlink недоступен") {
		t.Errorf("wg = %+v, ожидался unknown с причиной", byID["wg"])
	}
	if byID["mtu"].Status != statusUnknown || byID["mtu"].Detail == "" {
		t.Errorf("mtu = %+v: без состояния интерфейса MTU не сверить", byID["mtu"])
	}
}

// TestDiagOverallTakesWorst — сводка равна худшей из проверок.
func TestDiagOverallTakesWorst(t *testing.T) {
	ts := newTestServer(t)
	src := diagHealthySources()
	src.IPForward = func(context.Context) (bool, error) { return false, nil }
	ts.diag = src
	cookie := ts.login(t)

	byID, overall := diagChecks(t, ts, cookie)
	if byID["forward"].Status != statusError {
		t.Fatalf("forward = %+v, ожидался error", byID["forward"])
	}
	if overall != statusError {
		t.Errorf("overall = %q, ожидался error по худшей проверке", overall)
	}
}

// TestDiagOverallRank — «неизвестно» хуже ok, но лучше предупреждения.
func TestDiagOverallRank(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		want     string
	}{
		{"всё зелено", []string{statusOK, statusOK}, statusOK},
		// Непроведённая проверка не перебивает зелёные: заголовок «состояние
		// неизвестно» над шестью галочками бесполезен и приучает его не читать.
		{"неизвестное не перебивает ok", []string{statusOK, statusUnknown}, statusOK},
		{"неизвестно всё — неизвестно и в целом", []string{statusUnknown, statusUnknown}, statusUnknown},
		{"предупреждение хуже неизвестного", []string{statusUnknown, statusWarn}, statusWarn},
		{"ошибка хуже всего", []string{statusWarn, statusError, statusUnknown}, statusError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checks := make([]check, 0, len(tc.statuses))
			for _, s := range tc.statuses {
				checks = append(checks, check{Status: s})
			}
			if got := overall(checks); got != tc.want {
				t.Errorf("overall = %q, ожидался %q", got, tc.want)
			}
		})
	}
}

// TestDiagWGCheck — интерфейс проверяется по существу: поднят, слушает
// настроенный порт, пиров на нём столько же, сколько включено в базе.
func TestDiagWGCheck(t *testing.T) {
	snap := store.Snapshot{
		Settings: store.DefaultSettings(),
		Peers:    []store.Peer{{Enabled: true}, {Enabled: true}, {Enabled: false}},
	}
	healthy := diagHealthyWG()
	healthy.Peers = 2

	cases := []struct {
		name  string
		state netstack.DiagWGState
		want  string
	}{
		{"исправен", healthy, statusOK},
		{"интерфейса нет", netstack.DiagWGState{Name: "wg0"}, statusError},
		{"не поднят", netstack.DiagWGState{Name: "wg0", Exists: true, MTU: 1280}, statusError},
		{"чужой порт", netstack.DiagWGState{
			Name: "wg0", Exists: true, Up: true, MTU: 1280, ListenPort: 51821,
		}, statusError},
		{"пиры разъехались", netstack.DiagWGState{
			Name: "wg0", Exists: true, Up: true, MTU: 1280,
			ListenPort: snap.Settings.WGListenPort, Peers: 1,
		}, statusWarn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := wgCheck(tc.state, nil, snap)
			if c.Status != tc.want {
				t.Errorf("статус %q, ожидался %q (%s)", c.Status, tc.want, c.Detail)
			}
			if c.Detail == "" {
				t.Error("проверка без пояснения")
			}
		})
	}
}

// TestDiagNftCheck — таблица на месте целиком либо названа неполной поимённо.
func TestDiagNftCheck(t *testing.T) {
	partial := diagHealthyNft()
	partial.Chains = []string{netstack.ChainMangle}
	partial.Sets = []string{netstack.SetLocalV4}

	if c := nftCheck(diagHealthyNft(), nil, store.Snapshot{}); c.Status != statusOK {
		t.Errorf("полная таблица = %q (%s)", c.Status, c.Detail)
	}
	if c := nftCheck(netstack.DiagNftState{}, nil, store.Snapshot{}); c.Status != statusError {
		t.Errorf("отсутствие таблицы = %q, ожидался error", c.Status)
	}
	c := nftCheck(partial, nil, store.Snapshot{})
	if c.Status != statusError {
		t.Fatalf("неполная таблица = %q, ожидался error", c.Status)
	}
	for _, want := range []string{netstack.ChainProxy, netstack.SetSubnets} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("в %q не назван недостающий объект %q", c.Detail, want)
		}
	}
}

// TestDiagNftSubnets — сет сверяется с подсетями правил по содержимому.
//
// Правило, чьих подсетей в сете нет, — это трафик мимо туннеля, и проверка
// обязана назвать и правило, и подсеть. Совпадения чисел мало: сет с тем же
// числом интервалов, но чужими адресами, исправным не считается.
func TestDiagNftSubnets(t *testing.T) {
	snap := store.Snapshot{Rules: []store.Rule{
		{
			Name: "Стриминг", Enabled: true, Action: store.ActionTunnel,
			Subnets: []string{"203.0.113.128/25", "198.51.100.0/24"},
		},
		{
			Name: "Выключенное", Enabled: false, Action: store.ActionTunnel,
			Subnets: []string{"192.0.2.0/24"},
		},
		{
			Name: "Блокировка", Enabled: true, Action: store.ActionBlock,
			Subnets: []string{"192.0.2.0/24"},
		},
	}}

	// Подсеть правила лежит внутри более широкого интервала списка — это
	// покрытие, а не расхождение.
	if c := nftCheck(diagHealthyNft(), nil, snap); c.Status != statusOK {
		t.Errorf("покрытые подсети = %q (%s)", c.Status, c.Detail)
	}

	stale := diagHealthyNft()
	stale.Subnets = diagRanges("203.0.113.0/24", "192.0.2.0/24")
	c := nftCheck(stale, nil, snap)
	if c.Status != statusError {
		t.Fatalf("отставший сет = %q, ожидался error (%s)", c.Status, c.Detail)
	}
	for _, want := range []string{"Стриминг", "198.51.100.0/24"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("в %q не названо %q", c.Detail, want)
		}
	}
	if strings.Contains(c.Detail, "Выключенное") || strings.Contains(c.Detail, "Блокировка") {
		t.Errorf("в %q спрошено за подсети, которых в сете и не бывает", c.Detail)
	}
}

// TestDiagSingboxCheck — сводка различает три судьбы правила: маршрут, отказ и
// пропуск. Успех Generate сам по себе ни о чём не говорит (issue #123).
func TestDiagSingboxCheck(t *testing.T) {
	settings := store.DefaultSettings()
	working := store.Rule{
		ID: "r1", Name: "Прямое", Enabled: true, Action: store.ActionDirect,
		Domains: []string{"example.org"},
	}
	denied := store.Rule{
		ID: "r2", Name: "Стриминг", Enabled: true, Action: store.ActionTunnel,
		TunnelID: "t1", Domains: []string{"netflix.com"},
	}
	empty := store.Rule{
		ID: "r3", Name: "Пустое", Enabled: true, Action: store.ActionDirect,
	}
	off := store.Tunnel{ID: "t1", Name: "Нидерланды", Enabled: false}

	cases := []struct {
		name string
		snap store.Snapshot
		want string
		says []string
	}{
		{
			name: "правило работает",
			snap: store.Snapshot{Settings: settings, Rules: []store.Rule{working}},
			want: statusOK,
		},
		{
			name: "туннель выключен",
			snap: store.Snapshot{
				Settings: settings, Tunnels: []store.Tunnel{off},
				Rules: []store.Rule{denied},
			},
			want: statusWarn,
			says: []string{"Стриминг", "туннель выключен"},
		},
		{
			name: "правило не попало в конфиг",
			snap: store.Snapshot{Settings: settings, Rules: []store.Rule{working, empty}},
			want: statusError,
			says: []string{"Пустое", "напрямую"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := singboxCheck(tc.snap)
			if c.Status != tc.want {
				t.Fatalf("статус %q, ожидался %q (%s)", c.Status, tc.want, c.Detail)
			}
			for _, want := range tc.says {
				if !strings.Contains(c.Detail, want) {
					t.Errorf("в %q не названо %q", c.Detail, want)
				}
			}
		})
	}
}

// TestDiagForwardCheck — форвардинг и masquerade проверяются вместе: без
// обратного пути включённый ip_forward клиентам не помогает.
func TestDiagForwardCheck(t *testing.T) {
	noMasq := diagHealthyNft()
	noMasq.Masquerade = false

	cases := []struct {
		name    string
		forward bool
		nft     netstack.DiagNftState
		nftErr  error
		want    string
	}{
		{"исправно", true, diagHealthyNft(), nil, statusOK},
		{"форвардинг выключен", false, diagHealthyNft(), nil, statusError},
		{"нет masquerade", true, noMasq, nil, statusError},
		{"нет таблицы", true, netstack.DiagNftState{}, nil, statusError},
		{"nft недоступен", true, netstack.DiagNftState{}, errDiagNoSource, statusUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := forwardCheck(tc.forward, nil, tc.nft, tc.nftErr)
			if c.Status != tc.want {
				t.Errorf("статус %q, ожидался %q (%s)", c.Status, tc.want, c.Detail)
			}
			if c.Detail == "" {
				t.Error("проверка без пояснения")
			}
		})
	}
}

// TestDiagMTUCheck — 1280 обязаны быть с обеих сторон (ADR 0004): расхождение
// между интерфейсом и клиентским конфигом — ошибка, а не предупреждение.
func TestDiagMTUCheck(t *testing.T) {
	settings := store.DefaultSettings()
	wrongClient := settings
	wrongClient.ClientMTU = 1420

	wrongLink := diagHealthyWG()
	wrongLink.MTU = 1420

	if c := mtuCheck(diagHealthyWG(), nil, settings); c.Status != statusOK {
		t.Errorf("1280 с обеих сторон = %q (%s)", c.Status, c.Detail)
	}
	if c := mtuCheck(diagHealthyWG(), nil, wrongClient); c.Status != statusError {
		t.Errorf("MTU клиентов 1420 = %q, ожидался error", c.Status)
	}
	if c := mtuCheck(wrongLink, nil, settings); c.Status != statusError {
		t.Errorf("MTU wg0 1420 = %q, ожидался error", c.Status)
	}
	c := mtuCheck(netstack.DiagWGState{}, errDiagNoSource, settings)
	if c.Status != statusUnknown || c.Detail == "" {
		t.Errorf("без источника = %+v, ожидался объяснённый unknown", c)
	}
}

// TestDiagListsCheck — свежесть кэша и честное «источника нет».
func TestDiagListsCheck(t *testing.T) {
	settings := store.DefaultSettings() // интервал обновления — сутки

	cases := []struct {
		name  string
		lists DiagLists
		err   error
		want  string
	}{
		{"свежие", diagHealthyLists(), nil, statusOK},
		{"списков нет", DiagLists{}, nil, statusOK},
		{"ни разу не обновлялись", DiagLists{Sources: 2}, nil, statusWarn},
		{"протухли", DiagLists{
			Sources: 2, Loaded: 2, LastRefresh: diagNow.Add(-72 * time.Hour),
		}, nil, statusWarn},
		{"скачались не все", DiagLists{
			Sources: 3, Loaded: 1, LastRefresh: diagNow.Add(-time.Hour),
		}, nil, statusWarn},
		{"источника нет", DiagLists{}, errDiagNoSource, statusUnknown},
		{"сбой источника", DiagLists{}, errors.New("кэш не прочитан"), statusUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := listsCheck(tc.lists, tc.err, settings, diagNow)
			if c.Status != tc.want {
				t.Errorf("статус %q, ожидался %q (%s)", c.Status, tc.want, c.Detail)
			}
			if c.Detail == "" {
				t.Error("проверка без пояснения")
			}
		})
	}
}

// TestDiagTunnelsCheck — сводка по туннелям показывает измеренное и честно
// называет непроверенное: «не проверялся» не то же самое, что «отвечает».
func TestDiagTunnelsCheck(t *testing.T) {
	tunnels := []store.Tunnel{
		{ID: "t1", Name: "Нидерланды", Enabled: true},
		{ID: "t2", Name: "Выключенный"},
	}

	if c := tunnelsCheck(nil, newCheckCache()); c.Status != statusOK || c.Detail == "" {
		t.Errorf("без туннелей = %+v, ожидался объяснённый ok", c)
	}

	empty := newCheckCache()
	if c := tunnelsCheck(tunnels, empty); c.Status != statusUnknown || c.Detail == "" {
		t.Errorf("непроверенные туннели = %+v, ожидался объяснённый unknown", c)
	}

	up := newCheckCache()
	up.put("t1", tunnelCheck{Status: tunnelUp})
	if c := tunnelsCheck(tunnels, up); c.Status != statusOK {
		t.Errorf("отвечающий туннель = %q (%s), ожидался ok", c.Status, c.Detail)
	}

	down := newCheckCache()
	down.put("t1", tunnelCheck{Status: tunnelDown})
	c := tunnelsCheck(tunnels, down)
	if c.Status != statusWarn || !strings.Contains(c.Detail, "Нидерланды") {
		t.Errorf("недоступный туннель = %+v, ожидался warn с именем", c)
	}
}
