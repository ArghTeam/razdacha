package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

func notifyStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "razdacha.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func runEnsureNotify(t *testing.T, st *store.Store) store.NotifyConfig {
	t.Helper()
	if err := ensureNotify(context.Background(), st, quietLog()); err != nil {
		t.Fatalf("ensureNotify: %v", err)
	}
	c, err := st.NotifyConfig(context.Background())
	if err != nil {
		t.Fatalf("NotifyConfig: %v", err)
	}
	return c
}

// Переменных нет — в БД остаётся то, что было. Обновление сервера не должно
// сбрасывать оповещения, настроенные через панель (та же логика, что у режима
// панели в issue #81).
func TestEnsureNotifyKeepsConfigWithoutEnv(t *testing.T) {
	st := notifyStore(t)
	want := store.NotifyConfig{Enabled: true, Token: "123:ABC", ChatID: "-1001"}
	if err := st.SaveNotifyConfig(context.Background(), want); err != nil {
		t.Fatalf("SaveNotifyConfig: %v", err)
	}

	got := runEnsureNotify(t, st)
	if got != want {
		t.Errorf("настройки изменились без переменных: %+v", got)
	}
}

// Обе переменные заданы — оповещения настраиваются и включаются.
func TestEnsureNotifyAppliesEnv(t *testing.T) {
	st := notifyStore(t)
	t.Setenv(envTelegramToken, " 123:ABC ")
	t.Setenv(envTelegramChat, " -1001 ")

	got := runEnsureNotify(t, st)
	if got.Token != "123:ABC" || got.ChatID != "-1001" || !got.Enabled {
		t.Errorf("настройки = %+v", got)
	}
}

// Пустой токен — «выключить», а не «включить без токена»: иначе установка
// падала бы на попытке сохранить заведомо нерабочую настройку.
func TestEnsureNotifyEmptyTokenDisables(t *testing.T) {
	st := notifyStore(t)
	if err := st.SaveNotifyConfig(context.Background(),
		store.NotifyConfig{Enabled: true, Token: "123:ABC", ChatID: "-1001"}); err != nil {
		t.Fatalf("SaveNotifyConfig: %v", err)
	}
	t.Setenv(envTelegramToken, "")

	got := runEnsureNotify(t, st)
	if got.Enabled || got.Token != "" {
		t.Errorf("оповещения не выключились: %+v", got)
	}
	// Чат остаётся: выключение не должно заставлять искать идентификатор заново.
	if got.ChatID != "-1001" {
		t.Errorf("чат потерян: %+v", got)
	}
}

// Задан только чат — сохраняется, но без токена ничего не включается.
func TestEnsureNotifyChatOnlyStaysDisabled(t *testing.T) {
	st := notifyStore(t)
	t.Setenv(envTelegramChat, "-1001")

	got := runEnsureNotify(t, st)
	if got.ChatID != "-1001" {
		t.Errorf("чат не сохранён: %+v", got)
	}
	if got.Enabled {
		t.Error("оповещения включились без токена")
	}
}
