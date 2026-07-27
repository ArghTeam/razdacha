package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/singbox"
)

// feed прогоняет серию статусов через подтверждение, отдавая накопленные
// сообщения. notified обновляется так же, как это делает расписание.
func feed(e *tunnelEvents, id, name string, start time.Time, step time.Duration,
	notified string, statuses ...string,
) ([]string, string) {
	var msgs []string
	at := start
	for _, st := range statuses {
		v := e.observe(id, name, st, notified, at)
		if v.Message != "" {
			msgs = append(msgs, v.Message)
		}
		if v.Confirmed != "" {
			notified = v.Confirmed
		}
		at = at.Add(step)
	}
	return msgs, notified
}

var base = time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

// Флап короче трёх проверок не должен порождать ни одного сообщения: у пула
// `urltest` переключение сервера — штатная работа, а не событие.
func TestEventsIgnoreShortFlap(t *testing.T) {
	e := newTunnelEvents()
	msgs, _ := feed(e, "t1", "Нидерланды", base, time.Minute, eventUp,
		tunnelDown, tunnelUp, tunnelDown, tunnelUp, tunnelDown, tunnelUp)
	if len(msgs) != 0 {
		t.Errorf("сообщения на коротком флапе: %v", msgs)
	}
}

// Падение и восстановление — по одному сообщению на переход, а не на проверку.
func TestEventsOneMessagePerTransition(t *testing.T) {
	e := newTunnelEvents()
	msgs, notified := feed(e, "t1", "Нидерланды", base, time.Minute, eventUp,
		tunnelDown, tunnelDown, tunnelDown, tunnelDown, tunnelDown)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "не отвечает") {
		t.Fatalf("сообщения о падении: %v", msgs)
	}
	if notified != eventDown {
		t.Errorf("сообщённый статус = %q", notified)
	}

	msgs, _ = feed(e, "t1", "Нидерланды", base.Add(time.Hour), time.Minute, notified,
		tunnelUp, tunnelUp, tunnelUp, tunnelUp)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "снова работает") {
		t.Errorf("сообщения о восстановлении: %v", msgs)
	}
}

// `slow` — рабочее состояние: сообщение о замедлении приходило бы на каждый
// скачок задержки.
func TestEventsSlowCountsAsUp(t *testing.T) {
	e := newTunnelEvents()
	msgs, _ := feed(e, "t1", "Нидерланды", base, time.Minute, eventUp,
		tunnelSlow, tunnelSlow, tunnelSlow, tunnelSlow)
	if len(msgs) != 0 {
		t.Errorf("сообщения на медленном туннеле: %v", msgs)
	}
}

// `not_applied` — состояние панели, а не сети: событием не считается и
// сбрасывает набранное подтверждение.
func TestEventsNotAppliedResetsStreak(t *testing.T) {
	e := newTunnelEvents()
	// Две проверки «down», затем выпадение из конфига, затем ещё две «down»:
	// подтверждение не должно склеиться через разрыв.
	msgs, _ := feed(e, "t1", "Нидерланды", base, time.Minute, eventUp,
		tunnelDown, tunnelDown, tunnelNotApplied, tunnelDown, tunnelDown)
	if len(msgs) != 0 {
		t.Errorf("подтверждение склеилось через not_applied: %v", msgs)
	}
}

// Первое наблюдение сообщается, только если оно плохое: «работает» сразу после
// настройки — не новость.
func TestEventsFirstObservation(t *testing.T) {
	e := newTunnelEvents()
	msgs, notified := feed(e, "t1", "Нидерланды", base, time.Minute, "",
		tunnelUp, tunnelUp, tunnelUp)
	if len(msgs) != 0 {
		t.Errorf("сообщение о рабочем туннеле при первом наблюдении: %v", msgs)
	}
	if notified != eventUp {
		t.Errorf("состояние не запомнено: %q", notified)
	}

	e2 := newTunnelEvents()
	msgs, _ = feed(e2, "t2", "Тайвань", base, time.Minute, "",
		tunnelDown, tunnelDown, tunnelDown)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "не отвечает") {
		t.Errorf("падение при первом наблюдении не сообщено: %v", msgs)
	}
}

// Туннель, валящийся через раз, не наберёт трёх одинаковых проверок и без
// отдельного режима не пришлёт ничего — тишина выглядела бы как «всё хорошо».
func TestEventsUnstableSaysSomethingOnce(t *testing.T) {
	e := newTunnelEvents()
	notified := eventUp
	var msgs []string
	at := base
	// Каждые три проверки состояние подтверждается и меняется — пять раз.
	for i := 0; i < 5; i++ {
		want := tunnelDown
		if i%2 == 1 {
			want = tunnelUp
		}
		got, n := feed(e, "t1", "Нидерланды", at, time.Minute, notified, want, want, want)
		msgs = append(msgs, got...)
		notified = n
		at = at.Add(3 * time.Minute)
	}

	if len(msgs) == 0 {
		t.Fatal("флапающий туннель не сказал ничего — это и есть дыра, которую закрываем")
	}
	unstable := 0
	for _, m := range msgs {
		if strings.Contains(m, "нестабилен") {
			unstable++
		}
	}
	if unstable != 1 {
		t.Errorf("сообщений «нестабилен» = %d, ожидалось одно: %v", unstable, msgs)
	}
	if len(msgs) > unstableChanges+1 {
		t.Errorf("сообщений всего %d — это уже спам: %v", len(msgs), msgs)
	}
}

// После сообщения о нестабильности туннель замолкает на час, а не продолжает
// сыпать на каждую смену.
func TestEventsUnstableMutes(t *testing.T) {
	e := newTunnelEvents()
	notified := eventUp
	at := base
	for i := 0; i < 5; i++ {
		want := tunnelDown
		if i%2 == 1 {
			want = tunnelUp
		}
		_, notified = feed(e, "t1", "Нидерланды", at, time.Minute, notified, want, want, want)
		at = at.Add(3 * time.Minute)
	}

	// Ещё две смены внутри того же часа — молчание.
	var after []string
	for i := 0; i < 2; i++ {
		want := tunnelDown
		if i%2 == 1 {
			want = tunnelUp
		}
		got, n := feed(e, "t1", "Нидерланды", at, time.Minute, notified, want, want, want)
		after = append(after, got...)
		notified = n
		at = at.Add(3 * time.Minute)
	}
	if len(after) != 0 {
		t.Errorf("после «нестабилен» продолжились сообщения: %v", after)
	}
}

// Удалённый туннель не должен держать за собой счётчики.
func TestEventsForgetDeleted(t *testing.T) {
	e := newTunnelEvents()
	feed(e, "t1", "Нидерланды", base, time.Minute, eventUp, tunnelDown, tunnelDown)
	e.forget(map[string]bool{"t2": true})
	e.mu.Lock()
	_, ok := e.state["t1"]
	e.mu.Unlock()
	if ok {
		t.Error("состояние удалённого туннеля осталось")
	}
}

/* --- сквозь настоящий прогон расписания ---------------------------------- */

// Оповещение доезжает по итогам прогонов, а не по одному наблюдению, и то, о
// чём сообщили, переживает перезапуск демона.
func TestWatchAnnouncesConfirmedDown(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Нидерланды")
	f := &fakeSender{}
	withSender(ts, f)
	requireCode(t, ts.auth(t, cookie, http.MethodPut, "/api/notify",
		`{"enabled":true,"chat_id":"-1001","token":"123:ABC"}`), http.StatusOK)

	// Туннель есть в конфиге, но проба не удаётся — это «down».
	clashRoutes(t, ts, proxiesBody(t, map[string]any{
		singbox.TunnelTag(tun.ID): map[string]any{"name": singbox.TunnelTag(tun.ID), "type": "socks"},
	}), -1, nil)

	ctx := context.Background()
	for i := 0; i < confirmChecks-1; i++ {
		if err := ts.refreshChecks(ctx); err != nil {
			t.Fatalf("refreshChecks: %v", err)
		}
	}
	if len(f.sent) != 0 {
		t.Fatalf("сообщение ушло до подтверждения: %v", f.sent)
	}

	if err := ts.refreshChecks(ctx); err != nil {
		t.Fatalf("refreshChecks: %v", err)
	}
	if len(f.sent) != 1 || !strings.Contains(f.sent[0], "не отвечает") {
		t.Fatalf("сообщения: %v", f.sent)
	}

	// Ещё прогоны — молчание: событие это переход, а не состояние.
	for i := 0; i < 3; i++ {
		if err := ts.refreshChecks(ctx); err != nil {
			t.Fatalf("refreshChecks: %v", err)
		}
	}
	if len(f.sent) != 1 {
		t.Errorf("повторные сообщения об одном падении: %v", f.sent)
	}

	saved, err := ts.st.TunnelChecks(ctx)
	if err != nil {
		t.Fatalf("TunnelChecks: %v", err)
	}
	if saved[tun.ID].Notified != eventDown {
		t.Errorf("сообщённый статус в БД = %q", saved[tun.ID].Notified)
	}
}

// С выключенными оповещениями расписание работает, состояние пишется, но
// наружу ничего не уходит.
func TestWatchStaysSilentWhenNotifyDisabled(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tun := createTunnel(t, ts, cookie, "Нидерланды")
	f := &fakeSender{}
	withSender(ts, f)

	clashRoutes(t, ts, proxiesBody(t, map[string]any{
		singbox.TunnelTag(tun.ID): map[string]any{"name": singbox.TunnelTag(tun.ID), "type": "socks"},
	}), -1, nil)

	ctx := context.Background()
	for i := 0; i < confirmChecks+1; i++ {
		if err := ts.refreshChecks(ctx); err != nil {
			t.Fatalf("refreshChecks: %v", err)
		}
	}
	if len(f.sent) != 0 {
		t.Errorf("с выключенными оповещениями ушли сообщения: %v", f.sent)
	}
	res, _ := ts.checks.get(tun.ID)
	if res.Status != tunnelDown {
		t.Errorf("статус не записан: %q", res.Status)
	}
}

// Недоступный sing-box — это «не знаем ничего», а не падение всех туннелей.
func TestWatchDoesNotAnnounceWhenClashDown(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	createTunnel(t, ts, cookie, "Нидерланды")
	f := &fakeSender{}
	withSender(ts, f)
	requireCode(t, ts.auth(t, cookie, http.MethodPut, "/api/notify",
		`{"enabled":true,"chat_id":"-1001","token":"123:ABC"}`), http.StatusOK)

	withClash(t, ts, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	ctx := context.Background()
	for i := 0; i < confirmChecks+2; i++ {
		_ = ts.refreshChecks(ctx)
	}
	if len(f.sent) != 0 {
		t.Errorf("недоступный sing-box породил оповещения: %v", f.sent)
	}
}
