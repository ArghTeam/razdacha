package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

// fakeSender подменяет транспорт: настоящий api.telegram.org в тестах не
// участвует, проверяется поведение панели вокруг него.
type fakeSender struct {
	sent []string
	err  error
}

func (f *fakeSender) Send(_ context.Context, text string) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, text)
	return nil
}

func withSender(ts *testServer, f *fakeSender) {
	ts.notify = func(store.NotifyConfig) notifySender { return f }
}

func saveNotify(t *testing.T, ts *testServer, cookie *http.Cookie, body string) response {
	t.Helper()
	return ts.auth(t, cookie, http.MethodPut, "/api/notify", body)
}

func TestNotifyRequiresSession(t *testing.T) {
	ts := newTestServer(t)
	for _, r := range []request{
		{method: http.MethodGet, path: "/api/notify"},
		{method: http.MethodPut, path: "/api/notify", body: `{}`},
		{method: http.MethodPost, path: "/api/notify/test"},
	} {
		requireCode(t, ts.do(t, r), http.StatusUnauthorized)
	}
}

// Токен — секрет. Он не возвращается ни из чтения, ни из сохранения: сессия
// даёт право менять настройки, а не забирать чужой ключ.
func TestNotifyNeverReturnsToken(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := saveNotify(t, ts, cookie,
		`{"enabled":true,"chat_id":"-1001","token":"123:СЕКРЕТ"}`)
	requireCode(t, resp, http.StatusOK)
	if strings.Contains(resp.body, "СЕКРЕТ") {
		t.Errorf("токен вернулся из PUT: %s", resp.body)
	}

	resp = ts.auth(t, cookie, http.MethodGet, "/api/notify", "")
	requireCode(t, resp, http.StatusOK)
	if strings.Contains(resp.body, "СЕКРЕТ") {
		t.Errorf("токен вернулся из GET: %s", resp.body)
	}
	var out notifyResponse
	decodeJSONBody(t, resp, &out)
	if !out.TokenSet {
		t.Error("token_set = false, хотя токен сохранён")
	}
	if out.ChatID != "-1001" || !out.Enabled {
		t.Errorf("настройки не сохранились: %+v", out)
	}
}

// Токен не должен попадать и в общие настройки: там его увидел бы каждый
// экран, который их читает.
func TestNotifySecretsStayOutOfSettings(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	requireCode(t, saveNotify(t, ts, cookie,
		`{"enabled":true,"chat_id":"-1001","token":"123:СЕКРЕТ"}`), http.StatusOK)

	resp := ts.auth(t, cookie, http.MethodGet, "/api/settings", "")
	requireCode(t, resp, http.StatusOK)
	if strings.Contains(resp.body, "СЕКРЕТ") || strings.Contains(resp.body, "notify") {
		t.Errorf("оповещения протекли в настройки: %s", resp.body)
	}
}

// Не прислали токен — оставить прежний. Иначе сохранение галочки стирало бы
// секрет, которого в форме и не было.
func TestNotifyKeepsTokenWhenOmitted(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	requireCode(t, saveNotify(t, ts, cookie,
		`{"enabled":true,"chat_id":"-1001","token":"123:ABC"}`), http.StatusOK)
	requireCode(t, saveNotify(t, ts, cookie, `{"enabled":false}`), http.StatusOK)

	c, err := ts.st.NotifyConfig(context.Background())
	if err != nil {
		t.Fatalf("NotifyConfig: %v", err)
	}
	if c.Token != "123:ABC" {
		t.Errorf("токен = %q, ожидался прежний", c.Token)
	}
	if c.Enabled {
		t.Error("выключение не сохранилось")
	}
}

// Включить оповещения без токена или чата нельзя: такая настройка выглядит
// рабочей и молча ничего не шлёт.
func TestNotifyRejectsEnableWithoutCredentials(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	resp := saveNotify(t, ts, cookie, `{"enabled":true,"chat_id":"","token":""}`)
	// 400, как и на любой отказ валидации store: ErrInvalid по всему слою
	// переводится одинаково.
	requireCode(t, resp, http.StatusBadRequest)
}

func TestNotifyTestSendsMessage(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	f := &fakeSender{}
	withSender(ts, f)

	requireCode(t, saveNotify(t, ts, cookie,
		`{"enabled":true,"chat_id":"-1001","token":"123:ABC"}`), http.StatusOK)
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/notify/test", ""), http.StatusOK)

	if len(f.sent) != 1 {
		t.Fatalf("отправлено сообщений: %d, ожидалось одно", len(f.sent))
	}
	if !strings.Contains(f.sent[0], "razdacha") {
		t.Errorf("текст = %q, в нём должен быть узнаваем источник", f.sent[0])
	}
}

// Проверять канал приходится до того, как его включишь: иначе галочку ставят
// вслепую. Выключенные оповещения тесту не помеха, отсутствие токена — помеха.
func TestNotifyTestWorksWhileDisabledButNeedsCredentials(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	f := &fakeSender{}
	withSender(ts, f)

	resp := ts.auth(t, cookie, http.MethodPost, "/api/notify/test", "")
	requireCode(t, resp, http.StatusConflict)
	if len(f.sent) != 0 {
		t.Error("отправка пошла без токена")
	}

	requireCode(t, saveNotify(t, ts, cookie,
		`{"enabled":false,"chat_id":"-1001","token":"123:ABC"}`), http.StatusOK)
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/notify/test", ""), http.StatusOK)
	if len(f.sent) != 1 {
		t.Errorf("отправлено сообщений: %d, ожидалось одно", len(f.sent))
	}
}

// Отказ телеграма доходит до пользователя текстом, а не молчанием.
func TestNotifyTestReportsFailure(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	withSender(ts, &fakeSender{err: errTestSend})

	requireCode(t, saveNotify(t, ts, cookie,
		`{"enabled":true,"chat_id":"-1001","token":"123:ABC"}`), http.StatusOK)
	resp := ts.auth(t, cookie, http.MethodPost, "/api/notify/test", "")
	requireCode(t, resp, http.StatusBadGateway)
	if !strings.Contains(decodeError(t, resp).Error, "токен бота неверен") {
		t.Errorf("ответ не объясняет причину: %s", resp.body)
	}
}

// errTestSend — отказ транспорта в форме, в какой его отдаёт настоящий клиент.
var errTestSend = errors.New("телеграм отказал: токен бота неверен или отозван")
