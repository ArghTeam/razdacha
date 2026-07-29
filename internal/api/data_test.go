package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

// testServerKey — публичный ключ «сервера» для тестов выдачи конфига.
const testServerKey = "kL9pQ2xVn3tYb7mR4sE1wZ8cJ6hA0dF5gU2iO7yT3nQ="

// вспомогательный запрос под сессией.
func (ts *testServer) auth(t *testing.T, cookie *http.Cookie, method, path, body string) response {
	t.Helper()
	return ts.do(t, request{method: method, path: path, body: body, cookies: []*http.Cookie{cookie}})
}

// decodeInto разбирает тело успешного ответа.
func decodeJSONBody(t *testing.T, resp response, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(resp.body), dst); err != nil {
		t.Fatalf("разбор ответа (%s): %v", resp.body, err)
	}
}

// requireCode проваливает тест, если код ответа не тот.
func requireCode(t *testing.T, resp response, want int) {
	t.Helper()
	if resp.code != want {
		t.Fatalf("код %d, ожидался %d (%s)", resp.code, want, resp.body)
	}
}

// createPeer заводит пира и возвращает его.
func createPeer(t *testing.T, ts *testServer, cookie *http.Cookie, name string) peerResponse {
	t.Helper()
	resp := ts.auth(t, cookie, http.MethodPost, "/api/peers", `{"name":"`+name+`"}`)
	requireCode(t, resp, http.StatusCreated)
	var p peerResponse
	decodeJSONBody(t, resp, &p)
	return p
}

// createTunnel заводит туннель из готового JSON-фрагмента.
func createTunnel(t *testing.T, ts *testServer, cookie *http.Cookie, name string) tunnelResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"name": name,
		"raw":  `{"type":"socks","server":"203.0.113.9","server_port":1080}`,
	})
	if err != nil {
		t.Fatalf("сборка тела: %v", err)
	}
	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels", string(body))
	requireCode(t, resp, http.StatusCreated)
	var out tunnelResponse
	decodeJSONBody(t, resp, &out)
	return out
}

// TestDataEndpointsRequireSession — весь слой данных закрыт сессией. Забытый
// маршрут должен отдавать 401, а не содержимое БД.
func TestDataEndpointsRequireSession(t *testing.T) {
	ts := newTestServer(t)

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/peers"},
		{http.MethodPost, "/api/peers"},
		{http.MethodPatch, "/api/peers/p1"},
		{http.MethodDelete, "/api/peers/p1"},
		{http.MethodGet, "/api/peers/p1/config"},
		{http.MethodGet, "/api/tunnels"},
		{http.MethodPost, "/api/tunnels"},
		{http.MethodPost, "/api/tunnels/parse"},
		{http.MethodPatch, "/api/tunnels/t1"},
		{http.MethodDelete, "/api/tunnels/t1"},
		{http.MethodGet, "/api/tunnels/t1/raw"},
		{http.MethodGet, "/api/rules"},
		{http.MethodPost, "/api/rules"},
		{http.MethodPut, "/api/rules/order"},
		{http.MethodPatch, "/api/rules/r1"},
		{http.MethodDelete, "/api/rules/r1"},
		{http.MethodGet, "/api/settings"},
		{http.MethodPatch, "/api/settings"},
		{http.MethodGet, "/api/diag"},
		{http.MethodPost, "/api/diag/run"},
	}

	for _, rt := range routes {
		resp := ts.do(t, request{method: rt.method, path: rt.path, body: "{}"})
		if resp.code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, ожидался 401 (%s)", rt.method, rt.path, resp.code, resp.body)
			continue
		}
		if code := decodeError(t, resp).Code; code != codeUnauthorized {
			t.Errorf("%s %s: код ошибки %q, ожидался %q", rt.method, rt.path, code, codeUnauthorized)
		}
	}
}

// TestPeerLifecycle — создание, чтение, изменение и удаление пира.
func TestPeerLifecycle(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	p := createPeer(t, ts, cookie, "iPhone Ромы")
	if p.ID == "" || p.PublicKey == "" {
		t.Fatalf("пир создан без идентификатора или ключа: %+v", p)
	}
	if p.Address != "10.8.0.2" {
		t.Errorf("адрес %q, ожидался первый свободный 10.8.0.2", p.Address)
	}
	if !p.Enabled {
		t.Error("новый пир выключен")
	}

	second := createPeer(t, ts, cookie, "MacBook")
	if second.Address == p.Address {
		t.Errorf("двум пирам выдан один адрес %s", p.Address)
	}

	var list []peerResponse
	resp := ts.auth(t, cookie, http.MethodGet, "/api/peers", "")
	requireCode(t, resp, http.StatusOK)
	decodeJSONBody(t, resp, &list)
	if len(list) != 2 {
		t.Fatalf("в списке %d пиров, ожидалось 2", len(list))
	}
	if strings.Contains(resp.body, "private_key") || strings.Contains(resp.body, "preshared_key") {
		t.Error("список пиров отдаёт приватные ключи")
	}

	resp = ts.auth(t, cookie, http.MethodPatch, "/api/peers/"+p.ID, `{"name":"Телефон","enabled":false}`)
	requireCode(t, resp, http.StatusOK)
	var patched peerResponse
	decodeJSONBody(t, resp, &patched)
	if patched.Name != "Телефон" || patched.Enabled {
		t.Errorf("изменение не применилось: %+v", patched)
	}

	requireCode(t, ts.auth(t, cookie, http.MethodDelete, "/api/peers/"+p.ID, ""), http.StatusOK)
	requireCode(t, ts.auth(t, cookie, http.MethodDelete, "/api/peers/"+p.ID, ""), http.StatusNotFound)
	requireCode(t, ts.auth(t, cookie, http.MethodPatch, "/api/peers/нет", `{}`), http.StatusNotFound)
}

// TestPeerDerivedFieldsAreNull — производных данных пока неоткуда взять, и это
// видно в ответе: null, а не ноль. Иначе UI показал бы «0 байт» вместо «нет данных».
func TestPeerDerivedFieldsAreNull(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	createPeer(t, ts, cookie, "iPhone")

	resp := ts.auth(t, cookie, http.MethodGet, "/api/peers", "")
	requireCode(t, resp, http.StatusOK)

	var raw []map[string]json.RawMessage
	decodeJSONBody(t, resp, &raw)
	if len(raw) != 1 {
		t.Fatalf("в списке %d пиров, ожидался 1", len(raw))
	}
	for _, field := range []string{"online", "last_handshake", "rx_bytes", "tx_bytes", "endpoint"} {
		v, ok := raw[0][field]
		if !ok {
			t.Errorf("поля %q нет в ответе — UI не отличит «нет данных» от «не поддерживается»", field)
			continue
		}
		if string(v) != "null" {
			t.Errorf("%s = %s, ожидался null: источника данных ещё нет", field, v)
		}
	}
}

// TestPeerConfigFixedFields — клиентский конфиг собран по docs/03-networking.md.
func TestPeerConfigFixedFields(t *testing.T) {
	ts := newTestServer(t)
	ts.serverKey = func(context.Context) (string, error) { return testServerKey, nil }
	cookie := ts.login(t)

	requireCode(t, ts.auth(t, cookie, http.MethodPatch, "/api/settings",
		`{"endpoint_host":"vpn.example.com"}`), http.StatusOK)

	p := createPeer(t, ts, cookie, "iPhone Ромы")
	resp := ts.auth(t, cookie, http.MethodGet, "/api/peers/"+p.ID+"/config", "")
	requireCode(t, resp, http.StatusOK)

	if ct := resp.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, ожидался text/plain", ct)
	}
	for _, want := range []string{
		"[Interface]",
		"DNS        = 10.8.0.1",
		"MTU        = 1280",
		"[Peer]",
		"PublicKey           = " + testServerKey,
		"PresharedKey        = ",
		"Endpoint            = vpn.example.com:51820",
		"AllowedIPs          = 0.0.0.0/0, ::/0",
		"PersistentKeepalive = 25",
		"Address    = " + p.Address + "/32",
	} {
		if !strings.Contains(resp.body, want) {
			t.Errorf("в конфиге нет %q:\n%s", want, resp.body)
		}
	}

	psk := ts.peerSecret(t, p.ID)
	if !strings.Contains(resp.body, psk) {
		t.Error("в конфиге нет pre-shared ключа пира")
	}
}

// peerSecret достаёт pre-shared ключ пира прямо из БД: наружу он уходит только
// в конфиге, и проверять его надо по хранимому значению.
func (ts *testServer) peerSecret(t *testing.T, id string) string {
	t.Helper()
	p, err := ts.st.Peer(context.Background(), id)
	if err != nil {
		t.Fatalf("чтение пира: %v", err)
	}
	return p.PresharedKey
}

// TestPeerConfigWithoutServerKey — ключа сервера ещё нет (netstack не написан):
// вместо конфига, который выглядит рабочим и не подключается, — внятный отказ.
func TestPeerConfigWithoutServerKey(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	p := createPeer(t, ts, cookie, "iPhone")

	resp := ts.auth(t, cookie, http.MethodGet, "/api/peers/"+p.ID+"/config", "")
	requireCode(t, resp, http.StatusServiceUnavailable)
	if e := decodeError(t, resp); e.Code != codeNotReady || !strings.Contains(e.Error, "ключ сервера") {
		t.Errorf("ошибка %+v не объясняет, чего не хватает", e)
	}
}

// TestSettingsExposesServerPublicKey — ключ сервера живёт в настройках, отдельным
// полем только на чтение; без netstack он null.
func TestSettingsExposesServerPublicKey(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodGet, "/api/settings", "")
	requireCode(t, resp, http.StatusOK)
	var raw map[string]json.RawMessage
	decodeJSONBody(t, resp, &raw)
	if v, ok := raw["server_public_key"]; !ok || string(v) != "null" {
		t.Errorf("server_public_key = %s (есть: %v), ожидался null", v, ok)
	}

	ts.serverKey = func(context.Context) (string, error) { return testServerKey, nil }
	resp = ts.auth(t, cookie, http.MethodGet, "/api/settings", "")
	requireCode(t, resp, http.StatusOK)
	var out settingsResponse
	decodeJSONBody(t, resp, &out)
	if out.ServerPublicKey == nil || *out.ServerPublicKey != testServerKey {
		t.Errorf("server_public_key = %v, ожидался ключ сервера", out.ServerPublicKey)
	}
}

// TestSettingsPatch — частичное изменение и признак переподключения клиентов.
func TestSettingsPatch(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodPatch, "/api/settings", `{"dns_upstream":"9.9.9.9"}`)
	requireCode(t, resp, http.StatusOK)
	var out settingsResponse
	decodeJSONBody(t, resp, &out)
	if out.DNSUpstream != "9.9.9.9" {
		t.Errorf("dns_upstream = %q, ожидался 9.9.9.9", out.DNSUpstream)
	}
	if out.ClientMTU != 1280 {
		t.Errorf("MTU = %d, ожидался неизменённый 1280", out.ClientMTU)
	}
	if out.ListUpdateInterval != 86400 {
		t.Errorf("list_update_interval = %d, ожидались секунды (86400)", out.ListUpdateInterval)
	}
	if out.RequiresClientReconfig {
		t.Error("смена апстрима DNS не требует перевыдачи клиентских конфигов")
	}

	resp = ts.auth(t, cookie, http.MethodPatch, "/api/settings", `{"wg_listen_port":51821}`)
	requireCode(t, resp, http.StatusOK)
	decodeJSONBody(t, resp, &out)
	if !out.RequiresClientReconfig {
		t.Error("смена порта wg не помечена как требующая перевыдачи конфигов")
	}

	resp = ts.auth(t, cookie, http.MethodPatch, "/api/settings", `{"client_mtu":9000}`)
	requireCode(t, resp, http.StatusBadRequest)
	if e := decodeError(t, resp); !strings.Contains(e.Error, "MTU") {
		t.Errorf("ошибка %+v не называет причину", e)
	}
}

// Опечатка в адресе отвергается ручкой: до этого она сохранялась, а падал
// следующий старт демона — шлюз не поднимался вовсе (аудит от 2026-07, пункт 11).
func TestSettingsPatchRejectsBadAddress(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	cases := map[string]string{
		"адрес сервера с буквой": `{"wg_server_address":"10.8.0.o"}`,
		"пул без маски":          `{"wg_pool":"10.8.0.0"}`,
		"адрес вне пула":         `{"wg_server_address":"10.9.0.1"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp := ts.auth(t, cookie, http.MethodPatch, "/api/settings", body)
			requireCode(t, resp, http.StatusBadRequest)
			if e := decodeError(t, resp); e.Error == "" {
				t.Errorf("ошибка %+v без объяснения", e)
			}
		})
	}

	// В БД не осталось битого значения: следующее чтение настроек рабочее.
	resp := ts.auth(t, cookie, http.MethodGet, "/api/settings", "")
	requireCode(t, resp, http.StatusOK)
	var out settingsResponse
	decodeJSONBody(t, resp, &out)
	if out.WGServerAddress != "10.8.0.1" || out.WGPool != "10.8.0.0/24" {
		t.Errorf("настройки изменились: пул %q, адрес %q", out.WGPool, out.WGServerAddress)
	}
}

// TestTunnelLifecycle — создание из URL, изменение, разбор и удаление.
func TestTunnelLifecycle(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	raw := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@203.0.113.9:443?security=tls&type=ws#Нидерланды"
	body, err := json.Marshal(map[string]string{"raw": raw})
	if err != nil {
		t.Fatalf("сборка тела: %v", err)
	}
	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels", string(body))
	requireCode(t, resp, http.StatusCreated)

	var created tunnelResponse
	decodeJSONBody(t, resp, &created)
	if created.Type != store.TunnelVLESS {
		t.Errorf("тип %q, ожидался vless", created.Type)
	}
	if created.Name != "Нидерланды" {
		t.Errorf("имя %q, ожидалось взятое из ссылки", created.Name)
	}
	if created.Status != nil || created.LatencyMS != nil || created.LastCheck != nil {
		t.Error("статус и latency заполнены, хотя проверять их пока нечем")
	}

	resp = ts.auth(t, cookie, http.MethodPatch, "/api/tunnels/"+created.ID, `{"name":"NL","enabled":false}`)
	requireCode(t, resp, http.StatusOK)
	var patched tunnelResponse
	decodeJSONBody(t, resp, &patched)
	if patched.Name != "NL" || patched.Enabled {
		t.Errorf("изменение не применилось: %+v", patched)
	}

	requireCode(t, ts.auth(t, cookie, http.MethodDelete, "/api/tunnels/"+created.ID, ""), http.StatusOK)
	requireCode(t, ts.auth(t, cookie, http.MethodDelete, "/api/tunnels/"+created.ID, ""), http.StatusNotFound)
}

// TestTunnelParse — превью формы: корректная строка разбирается, битая даёт 400
// с объяснением на русском.
func TestTunnelParse(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	raw := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@203.0.113.9:443?security=tls&type=ws#Нидерланды"
	body, err := json.Marshal(map[string]string{"raw": raw})
	if err != nil {
		t.Fatalf("сборка тела: %v", err)
	}
	resp := ts.auth(t, cookie, http.MethodPost, "/api/tunnels/parse", string(body))
	requireCode(t, resp, http.StatusOK)

	var out parsePreview
	decodeJSONBody(t, resp, &out)
	if out.Type != store.TunnelVLESS || out.Host != "203.0.113.9" || out.Port != 443 {
		t.Errorf("разбор дал %+v", out)
	}
	if out.Security != "tls" || out.Transport != "ws" {
		t.Errorf("security = %q, transport = %q, ожидались tls и ws", out.Security, out.Transport)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("предупреждения на корректной ссылке: %v", out.Warnings)
	}

	resp = ts.auth(t, cookie, http.MethodPost, "/api/tunnels/parse", `{"raw":"это точно не конфиг"}`)
	requireCode(t, resp, http.StatusBadRequest)
	e := decodeError(t, resp)
	if !strings.Contains(e.Error, "не похоже") {
		t.Errorf("ошибка %+v не объясняет, что не разобралось", e)
	}
	if e.Code != codeBadRequest {
		t.Errorf("код ошибки %q, ожидался %q", e.Code, codeBadRequest)
	}

	// Битая строка ничего не сохранила.
	resp = ts.auth(t, cookie, http.MethodGet, "/api/tunnels", "")
	requireCode(t, resp, http.StatusOK)
	if resp.body != "[]" {
		t.Errorf("разбор без сохранения оставил в БД %s", resp.body)
	}
}

// TestDeleteTunnelInUse — правило, ссылающееся на туннель, превращает удаление в
// 409 с русским текстом, а не в 500.
func TestDeleteTunnelInUse(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	tun := createTunnel(t, ts, cookie, "Нидерланды")
	rule, err := json.Marshal(map[string]any{
		"name":      "YouTube и Google",
		"action":    "tunnel",
		"tunnel_id": tun.ID,
		"domains":   []string{"youtube.com"},
	})
	if err != nil {
		t.Fatalf("сборка тела: %v", err)
	}
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/rules", string(rule)), http.StatusCreated)

	resp := ts.auth(t, cookie, http.MethodDelete, "/api/tunnels/"+tun.ID, "")
	requireCode(t, resp, http.StatusConflict)

	e := decodeError(t, resp)
	if e.Code != codeConflict {
		t.Errorf("код ошибки %q, ожидался %q", e.Code, codeConflict)
	}
	if !strings.Contains(e.Error, "YouTube и Google") {
		t.Errorf("ошибка %q не называет мешающее правило", e.Error)
	}
	if !hasCyrillicText(e.Error) {
		t.Errorf("ошибка %q не на русском, а она показывается пользователю как есть", e.Error)
	}
}

func hasCyrillicText(s string) bool {
	for _, r := range s {
		if r >= 'а' && r <= 'я' || r >= 'А' && r <= 'Я' {
			return true
		}
	}
	return false
}

// TestRuleLifecycleAndOrder — создание, изменение, переупорядочивание и удаление.
func TestRuleLifecycleAndOrder(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	var ids []string
	for _, name := range []string{"Первое", "Второе", "Третье"} {
		body, err := json.Marshal(map[string]any{
			"name":    name,
			"action":  "block",
			"domains": []string{"ads.example"},
		})
		if err != nil {
			t.Fatalf("сборка тела: %v", err)
		}
		resp := ts.auth(t, cookie, http.MethodPost, "/api/rules", string(body))
		requireCode(t, resp, http.StatusCreated)
		var r store.Rule
		decodeJSONBody(t, resp, &r)
		ids = append(ids, r.ID)
	}

	// Приоритет назначает store: правила идут в порядке добавления.
	var list []store.Rule
	resp := ts.auth(t, cookie, http.MethodGet, "/api/rules", "")
	requireCode(t, resp, http.StatusOK)
	decodeJSONBody(t, resp, &list)
	for i, r := range list {
		if r.Priority != i || r.ID != ids[i] {
			t.Fatalf("правило %d: id %s, приоритет %d", i, r.ID, r.Priority)
		}
	}

	order, err := json.Marshal(reorderRequest{IDs: []string{ids[2], ids[0], ids[1]}})
	if err != nil {
		t.Fatalf("сборка тела: %v", err)
	}
	resp = ts.auth(t, cookie, http.MethodPut, "/api/rules/order", string(order))
	requireCode(t, resp, http.StatusOK)
	decodeJSONBody(t, resp, &list)
	want := []string{ids[2], ids[0], ids[1]}
	for i, r := range list {
		if r.ID != want[i] || r.Priority != i {
			t.Errorf("после перестановки правило %d = %s (приоритет %d), ожидалось %s",
				i, r.ID, r.Priority, want[i])
		}
	}

	// Неполный порядок отклоняется целиком: промежуточных состояний быть не должно.
	partial, err := json.Marshal(reorderRequest{IDs: []string{ids[0]}})
	if err != nil {
		t.Fatalf("сборка тела: %v", err)
	}
	requireCode(t, ts.auth(t, cookie, http.MethodPut, "/api/rules/order", string(partial)),
		http.StatusBadRequest)

	// Изменение не трогает приоритет.
	resp = ts.auth(t, cookie, http.MethodPatch, "/api/rules/"+ids[0], `{"name":"Переименованное"}`)
	requireCode(t, resp, http.StatusOK)
	var patched store.Rule
	decodeJSONBody(t, resp, &patched)
	if patched.Name != "Переименованное" || patched.Priority != 1 {
		t.Errorf("после изменения: %+v", patched)
	}

	requireCode(t, ts.auth(t, cookie, http.MethodDelete, "/api/rules/"+ids[0], ""), http.StatusOK)
	requireCode(t, ts.auth(t, cookie, http.MethodDelete, "/api/rules/"+ids[0], ""), http.StatusNotFound)
}

// TestRuleValidationIsBadRequest — правило без туннеля при action=tunnel это ввод
// пользователя, а не сбой: 400 с текстом от store.
func TestRuleValidationIsBadRequest(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodPost, "/api/rules", `{"name":"Без туннеля","action":"tunnel"}`)
	requireCode(t, resp, http.StatusBadRequest)
	if e := decodeError(t, resp); !strings.Contains(e.Error, "туннел") {
		t.Errorf("ошибка %+v не называет причину", e)
	}
}

// TestAllocateAddressSkipsTaken — адрес сервера и занятые адреса пропускаются.
func TestAllocateAddressSkipsTaken(t *testing.T) {
	s := store.DefaultSettings()
	peers := []store.Peer{{Address: "10.8.0.2"}, {Address: "10.8.0.3"}}

	got, err := allocateAddress(s, peers)
	if err != nil {
		t.Fatalf("allocateAddress: %v", err)
	}
	if got != "10.8.0.4" {
		t.Errorf("адрес %q, ожидался 10.8.0.4", got)
	}

	s.WGPool = "10.8.0.0/30" // .0 сеть, .1 сервер, .2 занят, .3 широковещательный
	if _, err := allocateAddress(s, []store.Peer{{Address: "10.8.0.2"}}); err == nil {
		t.Error("в исчерпанном пуле выдался адрес")
	}
}
