package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// fakeWARP подставляет регистрацию: наружу тесты не ходят вовсе.
type fakeWARP struct {
	conf string
	err  error
	// unregErr — чем отвечает Cloudflare на снятие устройства.
	unregErr error

	calls int
	// unregistered — пары, с которыми звали снятие, по одной на вызов.
	unregistered [][2]string
}

// warpFakeDeviceID и warpFakeToken — то, что «выдал Cloudflare» подставному
// клиенту. Токен — секрет, и тесты проверяют, что он не уезжает в ответы API.
const (
	warpFakeDeviceID = "t.00000000"
	warpFakeToken    = "токен-устройства"
)

func (f *fakeWARP) Register(context.Context) (singbox.WARPDevice, error) {
	f.calls++
	if f.err != nil {
		return singbox.WARPDevice{}, f.err
	}
	return singbox.WARPDevice{
		Conf: f.conf, DeviceID: warpFakeDeviceID, AccessToken: warpFakeToken,
	}, nil
}

func (f *fakeWARP) Unregister(_ context.Context, deviceID, accessToken string) error {
	f.unregistered = append(f.unregistered, [2]string{deviceID, accessToken})
	return f.unregErr
}

// warpDeviceConf — то, что регистратор собирает из ответа Cloudflare.
const warpDeviceConf = `[Interface]
PrivateKey = uHl8sTKvHZmqSN0dhBDl0kNmRvGaxNCEIYyNSGDPmVo=
Address = 172.16.0.2/32, 2606:4700:110:8a1b::1/128
MTU = 1280

[Peer]
PublicKey = bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = engage.cloudflareclient.com:2408
Reserved = Zo9x
`

func withWARP(ts *testServer, f *fakeWARP) *fakeWARP {
	ts.warp = f
	return f
}

func addWARP(t *testing.T, ts *testServer, cookie *http.Cookie, body string) response {
	t.Helper()
	return ts.do(t, request{
		method: http.MethodPost, path: "/api/tunnels/warp", body: body,
		cookies: []*http.Cookie{cookie},
	})
}

// Кнопка заводит обычный wireguard-туннель: протокол прежний, форма конфига — warp.
func TestAddWARPCreatesTunnel(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	reg := withWARP(ts, &fakeWARP{conf: warpDeviceConf})

	resp := addWARP(t, ts, cookie, "")
	if resp.code != http.StatusCreated {
		t.Fatalf("POST /api/tunnels/warp = %d, ожидался 201: %s", resp.code, resp.body)
	}
	var got tunnelResponse
	if err := json.Unmarshal([]byte(resp.body), &got); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if got.Source != store.SourceWARP || got.Type != store.TunnelWireGuard {
		t.Errorf("форма %q, тип %q — ожидались warp и wireguard", got.Source, got.Type)
	}
	if got.Name != defaultWARPName {
		t.Errorf("имя %q, ожидалось %q", got.Name, defaultWARPName)
	}
	if reg.calls != 1 {
		t.Errorf("регистраций %d, ожидалась одна", reg.calls)
	}

	// Ключ пережил запись: в БД лежит тот же конфиг, из которого собран parsed.
	saved, err := ts.st.Tunnel(context.Background(), got.ID)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	if saved.Raw != warpDeviceConf {
		t.Errorf("в БД лежит другой конфиг:\n%s", saved.Raw)
	}
	if !strings.Contains(string(saved.Parsed), `"reserved"`) {
		t.Errorf("client ID не доехал до parsed: %s", saved.Parsed)
	}
}

// Повторное нажатие не плодит туннели и не заводит лишнее устройство у Cloudflare:
// отказ выдаётся до запроса наружу.
func TestAddWARPTwiceRejected(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	reg := withWARP(ts, &fakeWARP{conf: warpDeviceConf})

	if resp := addWARP(t, ts, cookie, ""); resp.code != http.StatusCreated {
		t.Fatalf("первый POST = %d: %s", resp.code, resp.body)
	}
	resp := addWARP(t, ts, cookie, "")
	if resp.code != http.StatusConflict {
		t.Fatalf("второй POST = %d, ожидался 409: %s", resp.code, resp.body)
	}
	if reg.calls != 1 {
		t.Errorf("регистраций %d — второе нажатие сходило в Cloudflare", reg.calls)
	}

	list, err := ts.st.Tunnels(context.Background())
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("туннелей %d, ожидался один", len(list))
	}
}

// Вставленный руками конфиг WARP занимает то же место: source узнаётся разбором,
// и кнопка после этого отвечает отказом, а не заводит второй.
func TestAddWARPAfterPastedConf(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	reg := withWARP(ts, &fakeWARP{conf: warpDeviceConf})

	body, err := json.Marshal(map[string]string{"name": "Свой WARP", "raw": warpDeviceConf})
	if err != nil {
		t.Fatalf("сборка тела: %v", err)
	}
	resp := ts.do(t, request{
		method: http.MethodPost, path: "/api/tunnels", body: string(body),
		cookies: []*http.Cookie{cookie},
	})
	if resp.code != http.StatusCreated {
		t.Fatalf("POST /api/tunnels = %d: %s", resp.code, resp.body)
	}
	var pasted tunnelResponse
	if err := json.Unmarshal([]byte(resp.body), &pasted); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if pasted.Source != store.SourceWARP {
		t.Fatalf("форма вставленного конфига %q, ожидалась warp", pasted.Source)
	}

	if got := addWARP(t, ts, cookie, ""); got.code != http.StatusConflict {
		t.Errorf("POST /api/tunnels/warp = %d, ожидался 409: %s", got.code, got.body)
	}
	if reg.calls != 0 {
		t.Errorf("регистраций %d — кнопка сходила наружу при готовом WARP", reg.calls)
	}
}

// Ошибка сети и отказ Cloudflare показываются разными текстами: пользователь
// чинит разное. Туннель при обеих не создаётся.
func TestAddWARPErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "сеть",
			err:  fmt.Errorf("%w: проверьте сеть на сервере", singbox.ErrWARPUnreachable),
			want: "связаться с Cloudflare",
		},
		{
			name: "отказ",
			err:  fmt.Errorf("%w: код 429", singbox.ErrWARPRejected),
			want: "Cloudflare отказал",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			cookie := ts.login(t)
			withWARP(ts, &fakeWARP{err: tt.err})

			resp := addWARP(t, ts, cookie, "")
			if resp.code != http.StatusBadGateway {
				t.Fatalf("код %d, ожидался 502: %s", resp.code, resp.body)
			}
			var got errorResponse
			if err := json.Unmarshal([]byte(resp.body), &got); err != nil {
				t.Fatalf("разбор ответа: %v", err)
			}
			if !strings.Contains(got.Error, tt.want) {
				t.Errorf("текст ошибки %q, ожидалось упоминание %q", got.Error, tt.want)
			}

			list, err := ts.st.Tunnels(context.Background())
			if err != nil {
				t.Fatalf("Tunnels: %v", err)
			}
			if len(list) != 0 {
				t.Errorf("туннелей %d — при отказе регистрации туннель создан", len(list))
			}
		})
	}
}

// Имя можно задать своё: у туннеля оно уникально, и «WARP» может быть занято.
func TestAddWARPCustomName(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	withWARP(ts, &fakeWARP{conf: warpDeviceConf})

	resp := addWARP(t, ts, cookie, `{"name":"Cloudflare"}`)
	if resp.code != http.StatusCreated {
		t.Fatalf("код %d, ожидался 201: %s", resp.code, resp.body)
	}
	var got tunnelResponse
	if err := json.Unmarshal([]byte(resp.body), &got); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if got.Name != "Cloudflare" {
		t.Errorf("имя %q, ожидалось «Cloudflare»", got.Name)
	}
}

// Без сессии наружу не ходят: регистрация — действие владельца панели.
func TestAddWARPRequiresSession(t *testing.T) {
	ts := newTestServer(t)
	reg := withWARP(ts, &fakeWARP{conf: warpDeviceConf})

	resp := ts.do(t, request{method: http.MethodPost, path: "/api/tunnels/warp"})
	if resp.code != http.StatusUnauthorized {
		t.Fatalf("код %d, ожидался 401", resp.code)
	}
	if reg.calls != 0 {
		t.Errorf("регистраций %d — запрос без сессии сходил в Cloudflare", reg.calls)
	}
}

// deleteTunnel — `DELETE /api/tunnels/{id}` от имени владельца панели.
func deleteTunnel(t *testing.T, ts *testServer, cookie *http.Cookie, id string) response {
	t.Helper()
	return ts.do(t, request{
		method: http.MethodDelete, path: "/api/tunnels/" + id,
		cookies: []*http.Cookie{cookie},
	})
}

// Удаление туннеля снимает устройство у Cloudflare: иначе оно остаётся у них
// навсегда, и перечитать регистрацию тоже нечем (issue #107).
func TestDeleteWARPUnregistersDevice(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	reg := withWARP(ts, &fakeWARP{conf: warpDeviceConf})

	resp := addWARP(t, ts, cookie, "")
	if resp.code != http.StatusCreated {
		t.Fatalf("POST /api/tunnels/warp = %d: %s", resp.code, resp.body)
	}
	var created tunnelResponse
	if err := json.Unmarshal([]byte(resp.body), &created); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}

	if got := deleteTunnel(t, ts, cookie, created.ID); got.code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", got.code, got.body)
	}
	if len(reg.unregistered) != 1 {
		t.Fatalf("снятий устройства %d, ожидалось одно", len(reg.unregistered))
	}
	if reg.unregistered[0] != [2]string{warpFakeDeviceID, warpFakeToken} {
		t.Errorf("снятие звали с парой %v", reg.unregistered[0])
	}
}

// Отказ Cloudflare не мешает удалить туннель локально: пользователь не обязан
// застревать из-за чужого сервиса. Событие при этом обязано попасть в лог.
func TestDeleteWARPSurvivesCloudflareFailure(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	reg := withWARP(ts, &fakeWARP{
		conf:     warpDeviceConf,
		unregErr: fmt.Errorf("%w: код 401", singbox.ErrWARPUnregisterRejected),
	})

	resp := addWARP(t, ts, cookie, "")
	if resp.code != http.StatusCreated {
		t.Fatalf("POST /api/tunnels/warp = %d: %s", resp.code, resp.body)
	}
	var created tunnelResponse
	if err := json.Unmarshal([]byte(resp.body), &created); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}

	if got := deleteTunnel(t, ts, cookie, created.ID); got.code != http.StatusOK {
		t.Fatalf("DELETE = %d, ожидался 200: %s", got.code, got.body)
	}
	if len(reg.unregistered) != 1 {
		t.Errorf("снятий устройства %d, ожидалось одно", len(reg.unregistered))
	}
	list, err := ts.st.Tunnels(context.Background())
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("туннелей %d — отказ Cloudflare помешал удалить локально", len(list))
	}
	logs := ts.logs.String()
	if !strings.Contains(logs, "устройство WARP осталось у Cloudflare") {
		t.Errorf("отказ снятия не попал в лог:\n%s", logs)
	}
	// В логе остаётся единственный след зависшего устройства: идентификатор и
	// причина. Причина обязана называть снятие — регистрацию в этот момент
	// никто не заводил.
	if !strings.Contains(logs, warpFakeDeviceID) {
		t.Errorf("в логе нет идентификатора устройства:\n%s", logs)
	}
	if !strings.Contains(logs, "снятие устройства отклонено Cloudflare: код 401") {
		t.Errorf("причина в логе не называет снятие:\n%s", logs)
	}
	if strings.Contains(logs, warpFakeToken) {
		t.Errorf("токен устройства уехал в лог:\n%s", logs)
	}
}

// Туннель, заведённый вставкой .conf, устройства у Cloudflare не имеет — его
// удаление наружу не ходит вовсе.
func TestDeletePastedWARPDoesNotCallCloudflare(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	reg := withWARP(ts, &fakeWARP{conf: warpDeviceConf})

	body, err := json.Marshal(map[string]string{"name": "Свой WARP", "raw": warpDeviceConf})
	if err != nil {
		t.Fatalf("сборка тела: %v", err)
	}
	resp := ts.do(t, request{
		method: http.MethodPost, path: "/api/tunnels", body: string(body),
		cookies: []*http.Cookie{cookie},
	})
	if resp.code != http.StatusCreated {
		t.Fatalf("POST /api/tunnels = %d: %s", resp.code, resp.body)
	}
	var pasted tunnelResponse
	if err := json.Unmarshal([]byte(resp.body), &pasted); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}

	if got := deleteTunnel(t, ts, cookie, pasted.ID); got.code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", got.code, got.body)
	}
	if len(reg.unregistered) != 0 {
		t.Errorf("снятий устройства %d — сходили в Cloudflare за чужим устройством", len(reg.unregistered))
	}
}

// Токен устройства — секрет: ни в ответе о создании, ни в списке туннелей его
// быть не должно. Панель показывает `raw` как есть, и туда он тоже не попадает.
func TestWARPTokenNotExposed(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	withWARP(ts, &fakeWARP{conf: warpDeviceConf})

	created := addWARP(t, ts, cookie, "")
	if created.code != http.StatusCreated {
		t.Fatalf("POST /api/tunnels/warp = %d: %s", created.code, created.body)
	}
	list := ts.do(t, request{
		method: http.MethodGet, path: "/api/tunnels", cookies: []*http.Cookie{cookie},
	})
	if list.code != http.StatusOK {
		t.Fatalf("GET /api/tunnels = %d: %s", list.code, list.body)
	}
	for _, body := range []string{created.body, list.body} {
		if strings.Contains(body, warpFakeToken) {
			t.Errorf("токен устройства уехал в ответ API: %s", body)
		}
	}
}
