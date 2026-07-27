package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendDeliversText(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tg := NewTelegram(Options{Token: "123:ABC", ChatID: "-1001", Base: srv.URL})
	if err := tg.Send(context.Background(), "привет"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/bot123:ABC/sendMessage" {
		t.Errorf("путь = %q", gotPath)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("тело не разобрано: %v", err)
	}
	if body["chat_id"] != "-1001" || body["text"] != "привет" {
		t.Errorf("тело = %v", body)
	}
}

// Без токена или чата запрос не уходит вовсе: слать его в никуда незачем, а
// молчаливый успех скрыл бы незаконченную настройку.
func TestSendWithoutConfigDoesNotCallAPI(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer srv.Close()

	for _, o := range []Options{
		{ChatID: "-1001", Base: srv.URL},
		{Token: "123:ABC", Base: srv.URL},
		{Base: srv.URL},
	} {
		if err := NewTelegram(o).Send(context.Background(), "текст"); !errors.Is(err, ErrNotConfigured) {
			t.Errorf("ошибка = %v, ожидалась ErrNotConfigured", err)
		}
	}
	if called {
		t.Error("запрос к API ушёл при незаполненной настройке")
	}
}

// Отказ телеграма обязан доехать до пользователя объяснением, а не кодом:
// «Unauthorized» и «chat not found» чинятся по-разному.
func TestSendExplainsFailures(t *testing.T) {
	cases := []struct {
		name   string
		code   int
		body   string
		expect string
	}{
		{"неверный токен", 401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, "токен бота неверен"},
		{"бот не в чате", 403, `{"ok":false,"error_code":403,"description":"bot was blocked"}`, "бот заблокирован"},
		{"чат не найден", 400, `{"ok":false,"error_code":400,"description":"chat not found"}`, "чат не найден"},
		// Испорченный токен подставляется в путь URL, поэтому телеграм отвечает
		// 404, а не 401. Найдено прогоном на живом боте: пользователь получал
		// английское «Not Found» и не понимал, что чинить.
		{"битый токен", 404, `{"ok":false,"error_code":404,"description":"Not Found"}`, "токен бота неверен"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.code)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()

			tg := NewTelegram(Options{Token: "123:ABC", ChatID: "-1001", Base: srv.URL})
			err := tg.Send(context.Background(), "текст")
			if err == nil {
				t.Fatal("ожидалась ошибка")
			}
			if !strings.Contains(err.Error(), c.expect) {
				t.Errorf("ошибка = %q, ожидалось упоминание %q", err, c.expect)
			}
		})
	}
}

// Незнакомый код не теряет описание телеграма, но и не выдаёт его за
// объяснение: без пометки английская строка выглядит как наш текст.
func TestSendKeepsUnknownFailureRecognisable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests"}`))
	}))
	defer srv.Close()

	tg := NewTelegram(Options{Token: "123:ABC", ChatID: "-1001", Base: srv.URL})
	err := tg.Send(context.Background(), "текст")
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	for _, want := range []string{"неожиданный отказ", "429", "Too Many Requests"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ошибка = %q, ожидалось упоминание %q", err, want)
		}
	}
}

// Токен лежит в URL, поэтому ошибка транспорта не должна его пересказывать:
// текст ошибки уходит в панель и в логи.
func TestSendErrorDoesNotLeakToken(t *testing.T) {
	tg := NewTelegram(Options{
		Token:  "123:СЕКРЕТ",
		ChatID: "-1001",
		Base:   "http://127.0.0.1:1", // никто не слушает
	})
	err := tg.Send(context.Background(), "текст")
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if strings.Contains(err.Error(), "СЕКРЕТ") {
		t.Errorf("токен утёк в текст ошибки: %q", err)
	}
}
