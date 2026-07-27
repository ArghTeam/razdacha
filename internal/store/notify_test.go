package store

import (
	"context"
	"errors"
	"testing"
)

func TestNotifyConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	got, err := s.NotifyConfig(ctx)
	if err != nil {
		t.Fatalf("NotifyConfig: %v", err)
	}
	if got.Enabled || got.Token != "" || got.ChatID != "" {
		t.Errorf("пустая БД дала %+v, ожидалось «не настроено»", got)
	}

	want := NotifyConfig{Enabled: true, Token: " 123:ABC ", ChatID: " -1001 "}
	if err := s.SaveNotifyConfig(ctx, want); err != nil {
		t.Fatalf("SaveNotifyConfig: %v", err)
	}
	got, err = s.NotifyConfig(ctx)
	if err != nil {
		t.Fatalf("NotifyConfig: %v", err)
	}
	// Пробелы по краям срезаются: скопированный из чата токен приезжает с ними,
	// а телеграм на такой отвечает отказом, который нечем объяснить.
	if got.Token != "123:ABC" || got.ChatID != "-1001" || !got.Enabled {
		t.Errorf("прочитано %+v", got)
	}
	if !got.Ready() {
		t.Error("Ready() = false при полной настройке")
	}
}

// Включённые оповещения без токена или чата — не рабочая конфигурация, а
// незаконченная настройка: она выглядит рабочей и молча ничего не шлёт.
func TestNotifyConfigRejectsEnabledWithoutCredentials(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	for _, c := range []NotifyConfig{
		{Enabled: true},
		{Enabled: true, Token: "123:ABC"},
		{Enabled: true, ChatID: "-1001"},
	} {
		if err := s.SaveNotifyConfig(ctx, c); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: ошибка = %v, ожидалась ErrInvalid", c, err)
		}
	}

	// Выключенные — можно сохранять как угодно неполными: настройку заполняют
	// по частям, и запрещать это значило бы требовать всё сразу.
	if err := s.SaveNotifyConfig(ctx, NotifyConfig{ChatID: "-1001"}); err != nil {
		t.Errorf("выключенная неполная настройка отвергнута: %v", err)
	}
}

// Токен лежит вне Settings: иначе он переписывался бы из PATCH /api/settings и
// уезжал бы обратно в GET /api/settings.
func TestNotifyTokenStaysOutOfSettings(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if err := s.SaveNotifyConfig(ctx,
		NotifyConfig{Enabled: true, Token: "123:СЕКРЕТ", ChatID: "-1001"}); err != nil {
		t.Fatalf("SaveNotifyConfig: %v", err)
	}
	// Полная перезапись настроек не должна задевать оповещения.
	if err := s.SaveSettings(ctx, DefaultSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	got, err := s.NotifyConfig(ctx)
	if err != nil {
		t.Fatalf("NotifyConfig: %v", err)
	}
	if got.Token != "123:СЕКРЕТ" || !got.Enabled {
		t.Errorf("настройки оповещений затёрты записью Settings: %+v", got)
	}
}
