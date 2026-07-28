package api

import (
	"net/http"
	"strings"
	"testing"
)

// vlessKeyLink — ссылка с UUID: ровно то, что не должно попадать в ответы списка.
const vlessKeyLink = "vless://11111111-2222-3333-4444-555555555555@203.0.113.7:443" +
	"?security=tls&type=tcp#Тест"

// Список туннелей не отдаёт ни вставленную ссылку, ни приватный ключ WARP: он
// перечитывается на каждый опрос экрана, и ключ оседал бы в кэше браузера и в
// логах между nginx и клиентом (issue #124).
func TestListTunnelsHidesSecrets(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	withWARP(ts, &fakeWARP{conf: warpDeviceConf})

	created := ts.auth(t, cookie, http.MethodPost, "/api/tunnels",
		`{"name":"Ключ","raw":"`+vlessKeyLink+`"}`)
	requireCode(t, created, http.StatusCreated)
	// Ответ на создание — тот же tunnelResponse: эхо собственного ввода никому не
	// нужно, а форма ответа одна на все ручки.
	requireNoSecrets(t, created.body, "ответ на создание")

	requireCode(t, addWARP(t, ts, cookie, ""), http.StatusCreated)

	list := ts.auth(t, cookie, http.MethodGet, "/api/tunnels", "")
	requireCode(t, list, http.StatusOK)
	requireNoSecrets(t, list.body, "список туннелей")
	if strings.Contains(list.body, `"raw"`) {
		t.Errorf("в списке туннелей осталось поле raw: %s", list.body)
	}
}

// Конфиг для формы правки отдаётся отдельной ручкой по явному запросу.
func TestTunnelRawForEditing(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	created := ts.auth(t, cookie, http.MethodPost, "/api/tunnels",
		`{"name":"Ключ","raw":"`+vlessKeyLink+`"}`)
	requireCode(t, created, http.StatusCreated)
	var tun tunnelResponse
	decodeJSONBody(t, created, &tun)

	resp := ts.auth(t, cookie, http.MethodGet, "/api/tunnels/"+tun.ID+"/raw", "")
	requireCode(t, resp, http.StatusOK)
	var got tunnelRawResponse
	decodeJSONBody(t, resp, &got)
	if !got.Editable {
		t.Error("вставленный руками конфиг помечен неправимым")
	}
	if got.Raw != vlessKeyLink {
		t.Errorf("конфиг %q, ожидался вставленный", got.Raw)
	}
}

// У WARP ключ сгенерировал сервер: править его нечего, и в ответе его нет —
// иначе та же утечка вернулась бы через другую дверь.
func TestTunnelRawWARPNotEditable(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	withWARP(ts, &fakeWARP{conf: warpDeviceConf})

	created := addWARP(t, ts, cookie, "")
	requireCode(t, created, http.StatusCreated)
	var tun tunnelResponse
	decodeJSONBody(t, created, &tun)

	resp := ts.auth(t, cookie, http.MethodGet, "/api/tunnels/"+tun.ID+"/raw", "")
	requireCode(t, resp, http.StatusOK)
	requireNoSecrets(t, resp.body, "конфиг WARP")
	var got tunnelRawResponse
	decodeJSONBody(t, resp, &got)
	if got.Editable || got.Raw != "" {
		t.Errorf("конфиг WARP отдан на правку: %+v", got)
	}
	if got.Note == "" {
		t.Error("объяснения, почему править нечего, в ответе нет")
	}
}

// Несуществующий туннель — 404, а не пустой конфиг.
func TestTunnelRawUnknown(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	requireCode(t, ts.auth(t, cookie, http.MethodGet, "/api/tunnels/нет/raw", ""),
		http.StatusNotFound)
}

// requireNoSecrets — в теле ответа нет ни ссылки с UUID, ни приватного ключа.
func requireNoSecrets(t *testing.T, body, what string) {
	t.Helper()
	for _, bad := range []string{"vless://", "PrivateKey", "[Interface]"} {
		if strings.Contains(body, bad) {
			t.Errorf("%s содержит %q: %s", what, bad, body)
		}
	}
}
