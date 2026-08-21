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

// fakeRefresher подставляет расписание пулов. Запоминает пул целиком: ручка обязана
// передавать то, что прочитала из БД, а не идентификатор для поиска в чужом наборе.
type fakeRefresher struct {
	changed bool
	err     error
	got     []lists.PoolTunnel
}

func (f *fakeRefresher) RefreshPool(_ context.Context, t lists.PoolTunnel) (bool, error) {
	f.got = append(f.got, t)
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
	tun := poolTunnel(t, ts.st, "Бесплатные ключи", 20, true)

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
	tun := poolTunnel(t, ts.st, "Бесплатные ключи", 20, true)

	members := singbox.PoolMembers(tun, store.PoolFilter{})
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
	poolTunnel(t, ts.st, "Бесплатные ключи", 20, true)

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
//
// Пул уходит расписанию целиком, прямо из БД: набор расписания сверяется с БД раз в
// полминуты, и обход по требованию не должен ждать этого такта (issue #74). Своего
// набора у fakeRefresher нет вовсе — на нём это и видно.
func TestRefreshPool(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := poolTunnel(t, ts.st, "Бесплатные ключи", 20, true)

	fake := &fakeRefresher{changed: true}
	ts.pools = fake

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/refresh", "")
	requireCode(t, resp, http.StatusOK)
	if len(fake.got) != 1 || fake.got[0].ID != tun.ID {
		t.Fatalf("обновлены пулы %v, ожидался один %s", fake.got, tun.ID)
	}
	// Состав и каталог передаются вместе с пулом: слияние идёт с тем, что лежит в БД
	// сейчас, а не с копией, снятой расписанием когда-то раньше.
	if fake.got[0].CatalogURL != tun.Raw || len(fake.got[0].Servers) != 20 {
		t.Errorf("расписанию передан пул %+v без каталога или состава", fake.got[0])
	}
	var got tunnelResponse
	decodeJSONBody(t, resp, &got)
	if got.Pool == nil || got.Pool.CatalogURL != tun.Raw {
		t.Error("ответ не содержит блока pool: карточке пришлось бы делать второй запрос")
	}
}

// Обход по требованию работает на пуле, о котором расписание ещё не знает: тот же
// случай, что «завели пул и сразу нажали Обновить каталог» (issue #74). Ответ «пула
// нет» тем более не выдаётся за «туннель не найден».
func TestRefreshPoolBeforeScheduleKnowsIt(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	// Расписание уже поднято и знает про другой пул — но не про этот.
	fake := &fakeRefresher{}
	ts.pools = fake
	fresh := poolTunnel(t, ts.st, "Бесплатные ключи", 4, false)

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+fresh.ID+"/refresh", "")
	requireCode(t, resp, http.StatusOK)
	if len(fake.got) != 1 || fake.got[0].ID != fresh.ID {
		t.Fatalf("обновлены пулы %v, ожидался свежий %s", fake.got, fresh.ID)
	}
}

// Каталог без драйвера — ошибка пользователя, а не сбой демона: адрес вписал человек.
// Запись того же адреса отвечает `400` с этим же текстом, и обход обязан сходиться с
// ней (issue #156). Текст ошибки при этом не меняется — он и раньше был верным.
func TestRefreshPoolUnknownCatalogIsBadRequest(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := poolTunnel(t, ts.st, "Бесплатные ключи", 4, true)

	ts.pools = &fakeRefresher{err: fmt.Errorf("каталог пула %q: %w: разборщика для example.com нет",
		tun.Name, lists.ErrNoPoolDriver)}

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/refresh", "")
	requireCode(t, resp, http.StatusBadRequest)

	var got errorResponse
	decodeJSONBody(t, resp, &got)
	if got.Code != codeBadRequest {
		t.Errorf("код ошибки %q, ожидался %q", got.Code, codeBadRequest)
	}
	if !strings.Contains(got.Error, "разборщика для example.com нет") ||
		!strings.HasPrefix(got.Error, "Не удалось обойти каталог: ") {
		t.Errorf("текст ошибки изменился: %q", got.Error)
	}
}

// Идущий обход того же каталога — `409`, а не `502`: демон исправен, а состав пула
// обновит тот обход, который уже идёт.
func TestRefreshPoolBusyCatalogIsConflict(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := poolTunnel(t, ts.st, "Бесплатные ключи", 4, true)

	ts.pools = &fakeRefresher{err: fmt.Errorf("каталог пула %q: %w: https://keys.example/c",
		tun.Name, lists.ErrPoolCrawlBusy)}

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/refresh", "")
	requireCode(t, resp, http.StatusConflict)

	var got errorResponse
	decodeJSONBody(t, resp, &got)
	if got.Code != codeConflict {
		t.Errorf("код ошибки %q, ожидался %q", got.Code, codeConflict)
	}
}

// Сбой обхода, не названный сентинелом, остаётся сбоем: `502`, а не пользовательский
// отказ. Иначе оборванная сеть выглядела бы как ошибка того, кто нажал кнопку.
func TestRefreshPoolCrawlFailureStaysBadGateway(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := poolTunnel(t, ts.st, "Бесплатные ключи", 4, true)

	ts.pools = &fakeRefresher{err: fmt.Errorf("каталог пула %q: соединение оборвано", tun.Name)}

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/refresh", "")
	requireCode(t, resp, http.StatusBadGateway)
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
	tun := poolTunnel(t, ts.st, "Бесплатные ключи", 4, true)

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tun.ID+"/refresh", "")
	requireCode(t, resp, http.StatusServiceUnavailable)
}

// Пулы заводит демон — по одному на страну (ADR 0017): ссылка на каталог в
// `POST /api/tunnels` — отказ, а не свой пул.
//
// Раньше этот тест закреплял обратное: пул создавался ровно этим путём (issue #66).
// Уточнение объёма #71 отменило создание пулов руками, а #171 снял и допущение
// «пул один» — но разбор ссылки на каталог никуда не делся: ответ говорит про
// готовые пулы, а не «конфиг не разобран».
func TestCreatePoolFromCatalogURLIsRejected(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	body := `{"name":"Свой пул","raw":"https://vpnkeys.me/protocol/vless"}`
	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels", body)
	requireCode(t, resp, http.StatusConflict)
	if !strings.Contains(resp.body, "включите встроенный") {
		t.Errorf("отказ не говорит, что делать вместо создания: %s", resp.body)
	}

	if list := listTunnels(t, ts, cookie); len(list) != 0 {
		t.Fatalf("в БД появилось %d туннелей, ожидался отказ без создания", len(list))
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
	// Каталог заведомо шире окна: тогда есть и ротация (окно), и остаток за ней.
	total := lists.PoolConfigServers + 4
	tun := poolTunnel(t, ts.st, "Бесплатные ключи", total, true)

	members := singbox.PoolMembers(tun, store.PoolFilter{})
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
	if len(got.Servers) != total {
		t.Fatalf("серверов в ответе %d, ожидалось %d — весь каталог", len(got.Servers), total)
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
	tun := poolTunnel(t, ts.st, "Бесплатные ключи", 20, true)
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

	pool, err := ts.st.CreateTunnel(context.Background(), store.Tunnel{
		Name:    "🇳🇱 Нидерланды",
		Type:    store.TunnelShadowsocks,
		Source:  store.SourcePool,
		Raw:     lists.DefaultPoolCatalogURL,
		Country: "NL",
		Enabled: false,
		Builtin: true,
	})
	if err != nil {
		t.Fatalf("заведение встроенного пула: %v", err)
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

// Встроенный общий пул виден в списке и заперт как встроенный (ADR 0018). Второй пул
// ссылкой на каталог не завести, удалить встроенный нельзя, а включить и обновить —
// можно.
func TestBuiltinPoolListedAndLocked(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	ts.pools = &fakeRefresher{changed: true}

	if _, err := ts.st.EnsureBuiltinPool(context.Background(),
		lists.DefaultPoolCatalogURL, store.TunnelShadowsocks); err != nil {
		t.Fatalf("EnsureBuiltinPool: %v", err)
	}

	list := listTunnels(t, ts, cookie)
	if len(list) != 1 {
		t.Fatalf("туннелей в списке %d, ожидался один встроенный пул", len(list))
	}
	pool := list[0]
	if !pool.Builtin {
		t.Errorf("пул %q не помечен встроенным", pool.Name)
	}
	if pool.Source != store.SourcePool {
		t.Errorf("пул %q формы %q, ожидался pool", pool.Name, pool.Source)
	}
	if pool.Enabled {
		t.Errorf("пул %q заведён включённым: свежая установка сама пошла бы за ключами", pool.Name)
	}

	// Второй пул ссылкой на каталог не завести — пул заводит демон.
	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels",
		`{"name":"Свой пул","raw":"`+lists.DefaultPoolCatalogURL+`"}`)
	requireCode(t, resp, http.StatusConflict)

	// Встроенный пул не удаляется, но включается и обновляется.
	requireCode(t, ts.auth(t, cookie, http.MethodDelete, "/api/tunnels/"+pool.ID, ""),
		http.StatusConflict)
	requireCode(t, ts.auth(t, cookie, http.MethodPatch, "/api/tunnels/"+pool.ID, `{"enabled":true}`),
		http.StatusOK)
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+pool.ID+"/refresh", ""),
		http.StatusOK)

	if list := listTunnels(t, ts, cookie); len(list) != 1 {
		t.Fatalf("после действий над пулом их стало %d, ожидался один", len(list))
	}
}

// Пул, не помеченный встроенным, удаляется как обычный туннель: запрет касается
// только встроенного.
//
// Через API такой пул больше не завести, но в БД он бывает — это второй пул на
// установке, где их оказалось несколько, и встроенным стал не он. Удалить его должно
// быть можно: иначе лишний пул останется навсегда.
func TestDeleteUserPoolIsAllowed(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	created := poolTunnel(t, ts.st, "Второй пул", 4, false)
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
		`{"raw":"`+lists.DefaultPoolCatalogURL+`"}`)
	requireCode(t, resp, http.StatusOK)

	var got parsePreview
	decodeJSONBody(t, resp, &got)
	// Протокол в превью — от драйвера каталога, а не от схемы ссылки (ADR 0015).
	if got.Source != store.SourcePool || got.Type != store.TunnelShadowsocks {
		t.Fatalf("превью: type=%q source=%q", got.Type, got.Source)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("превью пула ругается: %v", got.Warnings)
	}
}

// Каталог, для которого драйвера нет, — внятный отказ ещё в превью, а не пул,
// который заведётся и молча останется пустым (issue #153).
func TestParsePreviewRejectsUnknownCatalog(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/parse",
		`{"raw":"https://vpnkeys.me/protocol/vless"}`)
	requireCode(t, resp, http.StatusBadRequest)
	if !strings.Contains(resp.body, "vpnkeys.me") {
		t.Errorf("отказ не называет каталог: %s", resp.body)
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

// Отбракованный сервер не участник пула: его нет ни в `servers`, ни в счётчике
// `servers_total`. Но и не исчезает молча — уходит в `excluded` с причиной, иначе
// похудевший пул выглядел бы поломкой каталога (ADR 0020, issue #202).
func TestPoolServersExcludesBlocked(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	ctx := context.Background()

	created, err := ts.st.CreateTunnel(ctx, store.Tunnel{
		Name: "Бесплатные ключи", Type: store.TunnelVLESS, Source: store.SourcePool,
		Raw: "https://raw.githack.com/igareck/vpn-configs-for-russia/main/", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	// Состав лежит в БД с прошлой версии: обхода каталога в этом тесте нет вовсе.
	servers := []store.PoolServer{
		{URL: "vless://a@10.0.0.1:443?security=reality&pbk=abc", Title: "🇳🇱 Нидерланды, Амстердам"},
		{URL: "vless://b@89.19.223.136:443?security=none", Title: "🇷🇺 Россия, Санкт-Петербург"},
		{URL: "vless://c@10.0.0.3:443?security=reality&pbk=abc", Title: "🇷🇺 🇺🇸 anycast"},
		{URL: "vless://d@10.0.0.4:443?security=none", Title: "🇩🇪 Германия, Франкфурт"},
	}
	if err := ts.st.UpdateTunnelPool(ctx, created.ID, servers, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateTunnelPool: %v", err)
	}

	got, body := poolServers(t, ts, cookie, created.ID)
	if len(got.Servers) != 1 || got.Servers[0].Title != "🇳🇱 Нидерланды, Амстердам" {
		t.Fatalf("в составе пула %+v, ожидалась одна нидерландская нода", got.Servers)
	}
	if strings.Contains(body, "89.19.223.136") {
		t.Error("адрес отбракованной ноды уехал в ответ")
	}
	if len(got.Excluded) != 3 {
		t.Fatalf("исключённых %d, ожидалось 3: %+v", len(got.Excluded), got.Excluded)
	}
	for _, e := range got.Excluded {
		if e.Reason == "" {
			t.Errorf("у исключённого %q нет причины", e.Title)
		}
	}

	// В списке туннелей пул считает только годных, а число выброшенных называет.
	list := listTunnels(t, ts, cookie)
	if list[0].Pool.ServersTotal != 1 {
		t.Errorf("servers_total = %d, ожидался 1", list[0].Pool.ServersTotal)
	}
	if list[0].Pool.ServersExcluded != 3 {
		t.Errorf("servers_excluded = %d, ожидалось 3", list[0].Pool.ServersExcluded)
	}
}

// Чёрный список правится через настройки: снятие RU возвращает ноды в пул без
// перезапуска демона и без обхода каталога.
func TestPoolServersFollowSettingsBlocklist(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	ctx := context.Background()

	created, err := ts.st.CreateTunnel(ctx, store.Tunnel{
		Name: "Бесплатные ключи", Type: store.TunnelVLESS, Source: store.SourcePool,
		Raw: "https://raw.githack.com/igareck/vpn-configs-for-russia/main/", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	servers := []store.PoolServer{
		{URL: "vless://a@10.0.0.1:443?security=reality&pbk=abc", Title: "🇳🇱 NL"},
		{URL: "vless://b@10.0.0.2:443?security=reality&pbk=abc", Title: "🇷🇺 Россия"},
	}
	if err := ts.st.UpdateTunnelPool(ctx, created.ID, servers, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateTunnelPool: %v", err)
	}

	requireCode(t, ts.auth(t, cookie, http.MethodPatch, "/api/settings",
		`{"pool_country_blocklist":[]}`), http.StatusOK)

	got, _ := poolServers(t, ts, cookie, created.ID)
	if len(got.Servers) != 2 || len(got.Excluded) != 0 {
		t.Fatalf("после снятия чёрного списка состав %+v, исключено %+v", got.Servers, got.Excluded)
	}
}
