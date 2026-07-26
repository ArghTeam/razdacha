package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// fakeProxies подставляет ответ Clash API вместо настоящего рантайма.
type fakeProxies struct {
	proxies map[string]clash.Proxy
	err     error
	calls   int
}

func (f *fakeProxies) Proxies(context.Context) (map[string]clash.Proxy, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.proxies, nil
}

// fakeRefresher подставляет расписание пулов.
type fakeRefresher struct {
	changed bool
	err     error
	ids     []string
}

func (f *fakeRefresher) RefreshTunnel(_ context.Context, id string) (bool, error) {
	f.ids = append(f.ids, id)
	return f.changed, f.err
}

// poolTunnel заводит в БД туннель-пул с n серверами. Пинг убывает с номером,
// поэтому отбор лучших предсказуем.
func poolTunnel(t *testing.T, st *store.Store, name string, n int, enabled bool) store.Tunnel {
	t.Helper()
	ctx := context.Background()

	created, err := st.CreateTunnel(ctx, store.Tunnel{
		Name:    name,
		Type:    store.TunnelVLESS,
		Source:  store.SourcePool,
		Raw:     "https://vpnkeys.me/protocol/vless",
		Enabled: enabled,
	})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	servers := make([]store.PoolServer, 0, n)
	for i := range n {
		servers = append(servers, store.PoolServer{
			URL: fmt.Sprintf("vless://%040d-0000-0000-0000-000000000000@10.0.0.%d:443"+
				"?encryption=none&security=tls&type=tcp&sni=example.com", i, i+1),
			Title:   fmt.Sprintf("Сервер %d", i),
			Country: "Netherlands",
			PingMS:  100 + i,
		})
	}
	at := time.Date(2026, 7, 26, 3, 52, 0, 0, time.UTC)
	if err := st.UpdateTunnelPool(ctx, created.ID, servers, at); err != nil {
		t.Fatalf("UpdateTunnelPool: %v", err)
	}
	created.Pool, created.PoolUpdatedAt = servers, at
	return created
}

// Блок pool отдаётся из БД: каталог, число серверов и время обхода не требуют
// живого sing-box, и без него карточка всё равно должна быть осмысленной.
func TestPoolBlockFromStore(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := poolTunnel(t, ts.st, "Бесплатные VLESS", 20, true)

	list := listTunnels(t, ts, cookie)
	if len(list) != 1 {
		t.Fatalf("туннелей в списке %d, ожидался один", len(list))
	}
	got := list[0]
	if got.Pool == nil {
		t.Fatal("блока pool в ответе нет")
	}
	if got.Type != store.TunnelVLESS || got.Source != store.SourcePool {
		t.Errorf("тип %q, форма %q — пул это vless из каталога", got.Type, got.Source)
	}
	if got.Pool.CatalogURL != tun.Raw {
		t.Errorf("каталог %q, ожидался %q", got.Pool.CatalogURL, tun.Raw)
	}
	if got.Pool.ServersTotal != 20 {
		t.Errorf("серверов %d, ожидалось 20", got.Pool.ServersTotal)
	}
	// Живого sing-box нет, поэтому число живых и текущий сервер неизвестны — и
	// это null, а не ноль: ноль был бы утверждением о непроверенном пуле.
	if got.Pool.ServersAlive != nil {
		t.Errorf("живых %v, ожидался null без Clash API", *got.Pool.ServersAlive)
	}
	if got.Pool.Current != nil {
		t.Errorf("текущий сервер %v, ожидался null без Clash API", *got.Pool.Current)
	}
	if got.Pool.UpdatedAt == nil || got.Pool.NextUpdateAt == nil {
		t.Fatal("время обхода каталога не заполнено")
	}
	if d := got.Pool.NextUpdateAt.Sub(*got.Pool.UpdatedAt); d != lists.DefaultPoolInterval {
		t.Errorf("следующий обход через %v, ожидалось %v", d, lists.DefaultPoolInterval)
	}
}

// Живое состояние берётся из Clash API: живых считает сам urltest, и выбранный им
// участник переводится в имя и страну сервера.
func TestPoolLiveStateFromClash(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := poolTunnel(t, ts.st, "Бесплатные VLESS", 20, true)

	members := singbox.PoolMembers(tun)
	if len(members) == 0 {
		t.Fatal("участников группы нет")
	}
	// Живыми объявляем три участника, одного из них группа выбрала.
	var chosen string
	proxies := map[string]clash.Proxy{}
	i := 0
	for tag := range members {
		if i < 3 {
			proxies[tag] = clash.Proxy{
				Name:    tag,
				History: []clash.History{{Time: time.Now(), Delay: uint16(210 + i)}},
			}
			if chosen == "" {
				chosen = tag
			}
		} else {
			// Проверен и не ответил: Clash пишет неудаче нулевую задержку.
			proxies[tag] = clash.Proxy{Name: tag, History: []clash.History{{Delay: 0}}}
		}
		i++
	}
	proxies[singbox.TunnelTag(tun.ID)] = clash.Proxy{
		Name: singbox.TunnelTag(tun.ID), Type: "URLTest", Now: chosen,
	}
	ts.poolProxies = &fakeProxies{proxies: proxies}

	got := listTunnels(t, ts, cookie)[0]
	if got.Pool.ServersAlive == nil || *got.Pool.ServersAlive != 3 {
		t.Fatalf("живых %v, ожидалось 3", got.Pool.ServersAlive)
	}
	if got.Pool.Current == nil {
		t.Fatal("текущий сервер не определён, хотя группа его выбрала")
	}
	if want := members[chosen]; got.Pool.Current.Name != want.Title {
		t.Errorf("текущий сервер %q, ожидался %q", got.Pool.Current.Name, want.Title)
	}
	if got.Pool.Current.LatencyMS == nil || *got.Pool.Current.LatencyMS != 210 {
		t.Errorf("задержка текущего %v, ожидалось 210", got.Pool.Current.LatencyMS)
	}
}

// Выключенный пул статистики не получает и за ней не ходит: цифры живых серверов
// у туннеля, которого нет в конфиге, — ложь, а не ноль.
func TestPoolDisabledHasNoLiveState(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	poolTunnel(t, ts.st, "Пул про запас", 20, false)

	fake := &fakeProxies{proxies: map[string]clash.Proxy{}}
	ts.poolProxies = fake

	got := listTunnels(t, ts, cookie)[0]
	if got.Pool == nil {
		t.Fatal("блока pool в ответе нет")
	}
	if got.Pool.ServersTotal != 20 {
		t.Errorf("серверов %d, ожидалось 20: состав каталога сохраняется и у выключенного",
			got.Pool.ServersTotal)
	}
	if got.Pool.ServersAlive != nil || got.Pool.Current != nil {
		t.Error("у выключенного пула появилась статистика живых серверов")
	}
	if fake.calls != 0 {
		t.Errorf("Clash API опрошен %d раз ради одного выключенного пула", fake.calls)
	}
}

// Недоступный Clash API оставляет живые поля пустыми, но не роняет весь список:
// иначе остановленный sing-box прятал бы от пользователя и остальные туннели.
func TestPoolClashUnavailableKeepsList(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	poolTunnel(t, ts.st, "Бесплатные VLESS", 20, true)

	ts.poolProxies = &fakeProxies{err: clash.ErrUnavailable}

	got := listTunnels(t, ts, cookie)[0]
	if got.Pool == nil || got.Pool.ServersTotal != 20 {
		t.Fatal("список потерян из-за недоступного Clash API")
	}
	if got.Pool.ServersAlive != nil {
		t.Error("живые серверы посчитаны без Clash API")
	}
}

// Кнопка «Обновить» обходит каталог и отдаёт туннель одним ответом.
func TestRefreshPool(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := poolTunnel(t, ts.st, "Бесплатные VLESS", 20, true)

	fake := &fakeRefresher{changed: true}
	ts.pools = fake

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/refresh", "")
	requireCode(t, resp, http.StatusOK)
	if len(fake.ids) != 1 || fake.ids[0] != tun.ID {
		t.Fatalf("обновлены пулы %v, ожидался один %s", fake.ids, tun.ID)
	}
	var got tunnelResponse
	decodeJSONBody(t, resp, &got)
	if got.Pool == nil || got.Pool.CatalogURL != tun.Raw {
		t.Error("ответ не содержит блока pool: карточке пришлось бы делать второй запрос")
	}
}

// Обновление каталога у обычного туннеля — 400, а не молчаливый успех.
func TestRefreshPoolRejectsPlainTunnel(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	ts.pools = &fakeRefresher{}

	created, err := ts.st.CreateTunnel(context.Background(), store.Tunnel{
		Name: "Резерв", Type: store.TunnelSOCKS, Source: store.SourceURL,
		Raw: "socks5://10.0.0.1:1080", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+created.ID+"/refresh", "")
	requireCode(t, resp, http.StatusBadRequest)
}

// Без поднятого расписания ручка честно говорит, что обновить нечем.
func TestRefreshPoolWithoutSchedule(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := poolTunnel(t, ts.st, "Бесплатные VLESS", 4, true)

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/refresh", "")
	requireCode(t, resp, http.StatusServiceUnavailable)
}

// Пул создаётся через ручку, а не в обход неё.
//
// Тест намеренно идёт полным путём «пользователь вставил URL каталога»: пулы,
// заведённые прямо в store, скрыли от нас то, что Parse не узнавал http(s), и
// создать пул было нечем при готовых store, singbox, lists и панели (issue #66).
func TestCreatePoolFromCatalogURL(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	body := `{"name":"Бесплатные VLESS","raw":"https://vpnkeys.me/protocol/vless"}`
	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels", body)
	requireCode(t, resp, http.StatusCreated)

	var got tunnelResponse
	decodeJSONBody(t, resp, &got)
	if got.Type != store.TunnelVLESS || got.Source != store.SourcePool {
		t.Fatalf("создан туннель type=%q source=%q, ожидались vless и pool", got.Type, got.Source)
	}
	if got.Pool == nil {
		t.Fatal("в ответе нет блока pool")
	}
	if got.Pool.CatalogURL != "https://vpnkeys.me/protocol/vless" {
		t.Errorf("каталог %q", got.Pool.CatalogURL)
	}
	if got.Pool.ServersTotal != 0 || got.Pool.UpdatedAt != nil {
		t.Error("у только что созданного пула уже есть серверы и время обхода")
	}

	// Пул обязан доехать до списка: правило навешивается на него как на туннель.
	list := listTunnels(t, ts, cookie)
	if len(list) != 1 || list[0].Pool == nil {
		t.Fatalf("в списке %d туннелей, пула среди них нет", len(list))
	}
}

// poolServers читает `GET /api/tunnels/{id}/pool` и отдаёт заодно сырое тело:
// по нему проверяется, что ключи наружу не уехали.
func poolServers(t *testing.T, ts *testServer, cookie *http.Cookie, id string) (poolServersResponse, string) {
	t.Helper()
	resp := ts.auth(t, cookie, http.MethodGet, "/api/tunnels/"+id+"/pool", "")
	requireCode(t, resp, http.StatusOK)
	var out poolServersResponse
	decodeJSONBody(t, resp, &out)
	return out, resp.body
}

// Ручка деталей отдаёт весь каталог, но живость — только для ротации: в конфиг идут
// лучшие poolMaxServers, остальных никто не проверял, и у них alive и latency строго
// null. Ссылки vless:// наружу не уходят вовсе — в них UUID ключа.
func TestPoolServersRotationAndRest(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := poolTunnel(t, ts.st, "Бесплатные VLESS", 20, true)

	members := singbox.PoolMembers(tun)
	// Двух участников объявляем живыми, один из них выбран группой; остальные
	// проверены и не ответили.
	var chosen string
	proxies := map[string]clash.Proxy{}
	i := 0
	for tag := range members {
		switch {
		case i < 2:
			proxies[tag] = clash.Proxy{
				Name:    tag,
				History: []clash.History{{Time: time.Now(), Delay: uint16(120 + i)}},
			}
			if chosen == "" {
				chosen = tag
			}
		default:
			proxies[tag] = clash.Proxy{Name: tag, History: []clash.History{{Delay: 0}}}
		}
		i++
	}
	proxies[singbox.TunnelTag(tun.ID)] = clash.Proxy{
		Name: singbox.TunnelTag(tun.ID), Type: "URLTest", Now: chosen,
	}
	ts.poolProxies = &fakeProxies{proxies: proxies}

	got, body := poolServers(t, ts, cookie, tun.ID)
	if got.CatalogURL != tun.Raw {
		t.Errorf("каталог %q, ожидался %q", got.CatalogURL, tun.Raw)
	}
	if got.UpdatedAt == nil {
		t.Error("время обхода каталога не отдано")
	}
	if len(got.Servers) != 20 {
		t.Fatalf("серверов в ответе %d, ожидалось 20 — весь каталог", len(got.Servers))
	}
	if strings.Contains(body, "vless://") {
		t.Error("в ответе есть ссылки vless:// — наружу уехал UUID ключа")
	}

	var rotation, current, alive, restWithState int
	for _, s := range got.Servers {
		if !s.InRotation {
			// Пустота не заполняется выдумкой: false был бы утверждением о
			// непроверенном сервере, ноль — «ноль миллисекунд».
			if s.Alive != nil || s.LatencyMS != nil {
				restWithState++
			}
			if s.Current {
				t.Error("сервер вне ротации объявлен текущим")
			}
			continue
		}
		rotation++
		if s.Current {
			current++
		}
		if s.Alive != nil && *s.Alive {
			alive++
		}
	}
	if rotation != len(members) {
		t.Errorf("в ротации %d серверов, у генератора конфига их %d", rotation, len(members))
	}
	if restWithState != 0 {
		t.Errorf("у %d серверов вне ротации заполнена живость", restWithState)
	}
	if alive != 2 {
		t.Errorf("живых в ротации %d, ожидалось 2", alive)
	}
	if current != 1 {
		t.Errorf("текущих серверов %d, ожидался один", current)
	}
	// Ротация идёт впереди: мёртвые и непроверенные в начале списка сбивали бы с толку.
	if !got.Servers[0].InRotation || got.Servers[0].LatencyMS == nil {
		t.Errorf("первым в списке %+v, ожидался живой участник ротации", got.Servers[0])
	}
	if got.Servers[len(got.Servers)-1].InRotation {
		t.Error("последним в списке участник ротации, а не остаток каталога")
	}
}

// Без живого sing-box живость неизвестна у всех, включая ротацию.
func TestPoolServersWithoutClash(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := poolTunnel(t, ts.st, "Бесплатные VLESS", 20, true)
	ts.poolProxies = &fakeProxies{err: clash.ErrUnavailable}

	got, _ := poolServers(t, ts, cookie, tun.ID)
	for _, s := range got.Servers {
		if s.Alive != nil || s.LatencyMS != nil || s.Current {
			t.Fatalf("живость посчитана без Clash API: %+v", s)
		}
	}
}

// Выключенный пул за живым состоянием не ходит: его нет в конфиге.
func TestPoolServersDisabledSkipsClash(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := poolTunnel(t, ts.st, "Пул про запас", 20, false)
	fake := &fakeProxies{proxies: map[string]clash.Proxy{}}
	ts.poolProxies = fake

	got, _ := poolServers(t, ts, cookie, tun.ID)
	if len(got.Servers) != 20 {
		t.Errorf("серверов в ответе %d, ожидалось 20: состав каталога сохраняется", len(got.Servers))
	}
	if fake.calls != 0 {
		t.Errorf("Clash API опрошен %d раз ради выключенного пула", fake.calls)
	}
}

// У обычного туннеля списка серверов нет — 400, а не пустой список.
func TestPoolServersRejectsPlainTunnel(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	created, err := ts.st.CreateTunnel(context.Background(), store.Tunnel{
		Name: "Резерв", Type: store.TunnelSOCKS, Source: store.SourceURL,
		Raw: "socks5://10.0.0.1:1080", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	requireCode(t, ts.auth(t, cookie, http.MethodGet, "/api/tunnels/"+created.ID+"/pool", ""),
		http.StatusBadRequest)
}

// Встроенный пул не удаляется: иначе панель прячет кнопку, а API продолжает
// разрешать. Выключение — штатный PATCH, оно работать обязано.
func TestDeleteBuiltinPoolIsRejected(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	pool, created, err := ts.st.EnsureBuiltinPool(context.Background(),
		"Бесплатные VLESS", lists.DefaultPoolCatalogURL)
	if err != nil || !created {
		t.Fatalf("EnsureBuiltinPool: %v (created=%v)", err, created)
	}

	resp := ts.auth(t, cookie, http.MethodDelete, "/api/tunnels/"+pool.ID, "")
	requireCode(t, resp, http.StatusConflict)
	if !strings.Contains(resp.body, "выключить") {
		t.Errorf("отказ не объясняет, что делать вместо удаления: %s", resp.body)
	}

	list := listTunnels(t, ts, cookie)
	if len(list) != 1 || !list[0].Builtin {
		t.Fatalf("встроенный пул пропал из списка или потерял признак: %+v", list)
	}
	if list[0].Enabled {
		t.Error("встроенный пул заведён включённым: свежая установка сама пошла бы за ключами")
	}

	requireCode(t, ts.auth(t, cookie, http.MethodPatch, "/api/tunnels/"+pool.ID, `{"enabled":true}`),
		http.StatusOK)
	after := listTunnels(t, ts, cookie)[0]
	if !after.Enabled || !after.Builtin {
		t.Errorf("включение встроенного пула не сработало: %+v", after)
	}
}

// Пул, заведённый пользователем, удаляется как обычный туннель: запрет касается
// только встроенного.
func TestDeleteUserPoolIsAllowed(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels",
		`{"name":"Свой пул","raw":"https://example.org/keys"}`)
	requireCode(t, resp, http.StatusCreated)
	var created tunnelResponse
	decodeJSONBody(t, resp, &created)
	if created.Builtin {
		t.Error("пул пользователя помечен встроенным")
	}

	requireCode(t, ts.auth(t, cookie, http.MethodDelete, "/api/tunnels/"+created.ID, ""),
		http.StatusOK)
}

// Превью показывает пул до сохранения и не жалуется на пустой конфиг.
func TestParsePreviewShowsPool(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/parse",
		`{"raw":"https://vpnkeys.me/protocol/vless"}`)
	requireCode(t, resp, http.StatusOK)

	var got parsePreview
	decodeJSONBody(t, resp, &got)
	if got.Source != store.SourcePool || got.Type != store.TunnelVLESS {
		t.Fatalf("превью: type=%q source=%q", got.Type, got.Source)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("превью пула ругается: %v", got.Warnings)
	}
}

// Битая ссылка на каталог — внятная ошибка, а не пустой пул.
func TestCreatePoolRejectsBadCatalogURL(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	for _, raw := range []string{
		"https://",
		"http://user:pass@vpnkeys.me/protocol/vless",
	} {
		resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels",
			`{"name":"Пул","raw":"`+raw+`"}`)
		if resp.code != http.StatusBadRequest {
			t.Errorf("%q: код %d, ожидался 400; тело %s", raw, resp.code, resp.body)
		}
	}
}
