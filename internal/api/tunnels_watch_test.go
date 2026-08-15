package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/singbox"
)

// proxiesBody собирает ответ `/proxies` в форме Clash API.
func proxiesBody(t *testing.T, proxies map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"proxies": proxies})
	if err != nil {
		t.Fatalf("сборка ответа /proxies: %v", err)
	}
	return string(b)
}

// clashRoutes отвечает на `/proxies` заданным телом, а на пробу — задержкой.
// Счётчик проб нужен там, где важно, что активной пробы не было вовсе.
func clashRoutes(t *testing.T, ts *testServer, listBody string, delayMS int, probes *int) {
	t.Helper()
	withClash(t, ts, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/delay"):
			if probes != nil {
				*probes++
			}
			if delayMS < 0 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"message":"проба не удалась"}`))
				return
			}
			_, _ = w.Write([]byte(`{"delay":` + itoa(delayMS) + `}`))
		default:
			_, _ = w.Write([]byte(listBody))
		}
	})
}

func itoa(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Обычный outbound не проверяет никто, кроме нас: расписание обязано пробить
// его активно, иначе он навсегда останется «не проверялся».
func TestRefreshChecksProbesPlainTunnel(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Нидерланды")
	tag := singbox.TunnelTag(tun.ID)

	probes := 0
	clashRoutes(t, ts, proxiesBody(t, map[string]any{
		tag: map[string]any{"name": tag, "type": "socks"},
	}), 42, &probes)

	if err := ts.refreshChecks(context.Background()); err != nil {
		t.Fatalf("refreshChecks: %v", err)
	}
	res, ok := ts.checks.get(tun.ID)
	if !ok {
		t.Fatal("результат проверки не попал в кэш")
	}
	if res.Status != tunnelUp {
		t.Errorf("статус = %q, ожидался %q", res.Status, tunnelUp)
	}
	if probes != 1 {
		t.Errorf("активных проб = %d, ожидалась одна", probes)
	}
}

// Группу `urltest` sing-box проверяет сам. Активная проба означала бы прогон по
// всем её участникам на каждом круге — за тегом пула их сотня.
func TestRefreshChecksGroupUsesHistoryWithoutProbe(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Пул")
	tag := singbox.TunnelTag(tun.ID)

	probes := 0
	clashRoutes(t, ts, proxiesBody(t, map[string]any{
		tag: map[string]any{
			"name": tag, "type": "urltest",
			"all": []string{tag + "-0", tag + "-1"},
			"now": tag + "-0",
			"history": []map[string]any{
				{"time": "2026-07-27T06:00:00Z", "delay": 55},
			},
		},
	}), 0, &probes)

	if err := ts.refreshChecks(context.Background()); err != nil {
		t.Fatalf("refreshChecks: %v", err)
	}
	res, _ := ts.checks.get(tun.ID)
	if res.Status != tunnelUp {
		t.Errorf("статус = %q, ожидался %q", res.Status, tunnelUp)
	}
	if res.LatencyMS == nil || *res.LatencyMS != 55 {
		t.Errorf("задержка = %v, ожидалось 55 из журнала", res.LatencyMS)
	}
	if probes != 0 {
		t.Errorf("активных проб = %d, группу пробивать нельзя", probes)
	}
}

// Пустой журнал группы — это «sing-box ещё не прогнал», а не «не работает».
// Записывать down на этом основании значило бы соврать.
func TestRefreshChecksGroupWithoutHistoryStaysSilent(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Пул")
	tag := singbox.TunnelTag(tun.ID)

	clashRoutes(t, ts, proxiesBody(t, map[string]any{
		tag: map[string]any{"name": tag, "type": "urltest", "all": []string{tag + "-0"}},
	}), 0, nil)

	if err := ts.refreshChecks(context.Background()); err != nil {
		t.Fatalf("refreshChecks: %v", err)
	}
	if _, ok := ts.checks.get(tun.ID); ok {
		t.Error("для группы без журнала записан результат, ожидалось молчание")
	}
}

// Туннеля нет в работающем конфиге — это состояние панели, а не сети.
func TestRefreshChecksMissingTagIsNotApplied(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Нидерланды")

	clashRoutes(t, ts, proxiesBody(t, map[string]any{}), 0, nil)

	if err := ts.refreshChecks(context.Background()); err != nil {
		t.Fatalf("refreshChecks: %v", err)
	}
	res, _ := ts.checks.get(tun.ID)
	if res.Status != tunnelNotApplied {
		t.Errorf("статус = %q, ожидался %q", res.Status, tunnelNotApplied)
	}
}

// Результат прогона обязан пережить перезапуск демона — ради этого задача и
// заводилась. Задержка не переживает намеренно (ADR 0011).
func TestRefreshChecksPersistStatusWithoutLatency(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Нидерланды")
	tag := singbox.TunnelTag(tun.ID)

	clashRoutes(t, ts, proxiesBody(t, map[string]any{
		tag: map[string]any{"name": tag, "type": "socks"},
	}), 42, nil)

	ctx := context.Background()
	if err := ts.refreshChecks(ctx); err != nil {
		t.Fatalf("refreshChecks: %v", err)
	}

	saved, err := ts.st.TunnelChecks(ctx)
	if err != nil {
		t.Fatalf("TunnelChecks: %v", err)
	}
	if got := saved[tun.ID].Status; got != tunnelUp {
		t.Errorf("сохранённый статус = %q, ожидался %q", got, tunnelUp)
	}

	// Свежий кэш, как после перезапуска демона.
	ts.checks = newCheckCache()
	ts.loadChecks(ctx)
	res, ok := ts.checks.get(tun.ID)
	if !ok {
		t.Fatal("сохранённая проверка не поднялась в кэш")
	}
	if res.Status != tunnelUp {
		t.Errorf("поднятый статус = %q, ожидался %q", res.Status, tunnelUp)
	}
	if res.LatencyMS != nil {
		t.Errorf("задержка = %v, она храниться не должна", *res.LatencyMS)
	}
	if res.At.IsZero() {
		t.Error("время проверки не поднялось")
	}
}

// Отметка последнего удачного ответа переживает и неудачные круги, и перезапуск
// демона: без неё панель не может сказать, с какого момента туннель молчит, —
// «упал минуту назад» и «мёртв со вчера» это разные новости (issue #152).
func TestRefreshChecksKeepsLastOK(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Нидерланды")
	tag := singbox.TunnelTag(tun.ID)
	ctx := context.Background()

	list := proxiesBody(t, map[string]any{tag: map[string]any{"name": tag, "type": "socks"}})
	clashRoutes(t, ts, list, 42, nil)
	if err := ts.refreshChecks(ctx); err != nil {
		t.Fatalf("refreshChecks: %v", err)
	}
	okAt := ts.clock()

	// Три круга подряд туннель не отвечает: отметка удачи остаётся прежней.
	clashRoutes(t, ts, list, -1, nil)
	for range 3 {
		ts.advance(2 * time.Minute)
		if err := ts.refreshChecks(ctx); err != nil {
			t.Fatalf("refreshChecks: %v", err)
		}
	}

	// Свежий кэш, как после перезапуска демона.
	ts.checks = newCheckCache()
	ts.loadChecks(ctx)
	res, ok := ts.checks.get(tun.ID)
	if !ok {
		t.Fatal("сохранённая проверка не поднялась в кэш")
	}
	if res.Status != tunnelDown {
		t.Errorf("статус = %q, ожидался %q", res.Status, tunnelDown)
	}
	if !res.OKAt.Equal(okAt) {
		t.Errorf("OKAt = %v, ожидалось %v", res.OKAt, okAt)
	}

	var out []tunnelResponse
	decodeJSONBody(t, ts.auth(t, cookie, http.MethodGet, "/api/tunnels", ""), &out)
	if len(out) != 1 {
		t.Fatalf("туннелей в ответе = %d, ожидался один", len(out))
	}
	if out[0].LastOK == nil {
		t.Fatal("last_ok = null, ожидалось время последнего удачного ответа")
	}
	if !out[0].LastOK.Equal(okAt) {
		t.Errorf("last_ok = %v, ожидалось %v", *out[0].LastOK, okAt)
	}
	if out[0].LastCheck == nil || !out[0].LastCheck.After(*out[0].LastOK) {
		t.Errorf("last_check = %v, ожидалось время позже last_ok", out[0].LastCheck)
	}
}

// Туннель, который не отвечал ни разу, отдаёт `last_ok` пустым: выдуманной даты
// вместо «удачных проверок не записано» быть не должно.
func TestRefreshChecksNeverOKLeavesLastOKNull(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Резервный")
	tag := singbox.TunnelTag(tun.ID)

	clashRoutes(t, ts, proxiesBody(t, map[string]any{
		tag: map[string]any{"name": tag, "type": "socks"},
	}), -1, nil)
	if err := ts.refreshChecks(context.Background()); err != nil {
		t.Fatalf("refreshChecks: %v", err)
	}

	var out []tunnelResponse
	decodeJSONBody(t, ts.auth(t, cookie, http.MethodGet, "/api/tunnels", ""), &out)
	if len(out) != 1 || out[0].Status == nil || *out[0].Status != tunnelDown {
		t.Fatalf("ответ = %+v, ожидался один туннель со статусом down", out)
	}
	if out[0].LastOK != nil {
		t.Errorf("last_ok = %v, ожидался null", *out[0].LastOK)
	}
}

// sing-box не отвечает — о туннелях не известно ничего. Прежний результат с
// его отметкой времени честнее, чем «down» от несостоявшейся проверки.
func TestRefreshChecksKeepsPreviousWhenClashDown(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Нидерланды")

	was := tunnelCheck{Status: tunnelUp, At: ts.now}
	ts.checks.put(tun.ID, was)

	withClash(t, ts, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if err := ts.refreshChecks(context.Background()); err == nil {
		t.Fatal("ожидалась ошибка прогона при недоступном sing-box")
	}
	res, _ := ts.checks.get(tun.ID)
	if res.Status != tunnelUp || !res.At.Equal(was.At) {
		t.Errorf("прежний результат затёрт: %+v", res)
	}
}

// Удалённый туннель не должен держать за собой запись до перезапуска демона.
func TestRefreshChecksDropsDeletedTunnel(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Нидерланды")

	ts.checks.put("уже-удалён", tunnelCheck{Status: tunnelDown, At: ts.now})
	clashRoutes(t, ts, proxiesBody(t, map[string]any{}), 0, nil)

	if err := ts.refreshChecks(context.Background()); err != nil {
		t.Fatalf("refreshChecks: %v", err)
	}
	if _, ok := ts.checks.get("уже-удалён"); ok {
		t.Error("запись по несуществующему туннелю осталась в кэше")
	}
	if _, ok := ts.checks.get(tun.ID); !ok {
		t.Error("живой туннель выброшен из кэша")
	}
}

// Интервал берётся из настроек на каждом круге, а не запоминается на старте.
func TestCheckIntervalFollowsSettings(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	v, err := ts.st.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	v.TunnelCheckInterval = 5 * time.Minute
	if err := ts.st.SaveSettings(ctx, v); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if got := ts.checkInterval(ctx); got != 5*time.Minute {
		t.Errorf("интервал = %v, ожидалось 5m", got)
	}
}
