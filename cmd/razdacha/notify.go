package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ArghTeam/razdacha/internal/store"
)

// Переменные окружения, которыми оповещения настраиваются при установке.
//
// Читаются прямо из окружения, а не через флаг команды, в отличие от
// RAZDACHA_PUBLIC: токен бота — секрет, а аргументы процесса видны в `ps`
// любому пользователю системы всё время работы установщика.
const (
	envTelegramToken = "RAZDACHA_TELEGRAM_TOKEN"
	envTelegramChat  = "RAZDACHA_TELEGRAM_CHAT"
)

// ensureNotify применяет настройки оповещений из окружения.
//
// Три состояния, как и у режима панели (issue #81): переменных нет — в БД
// остаётся то, что было, и обновление не сбрасывает настроенные оповещения;
// переменные есть и непусты — записываются и включают отправку; токен задан
// пустым — оповещения выключаются и токен стирается.
//
// Источник правды при этом остаётся один — БД. Переменные её засевают, а не
// подменяют: иначе панель показывала бы поля, которые ничего не меняют.
func ensureNotify(ctx context.Context, st *store.Store, log *slog.Logger) error {
	token, tokenSet := os.LookupEnv(envTelegramToken)
	chat, chatSet := os.LookupEnv(envTelegramChat)
	if !tokenSet && !chatSet {
		return nil
	}

	current, err := st.NotifyConfig(ctx)
	if err != nil {
		return err
	}
	next := current
	if tokenSet {
		next.Token = strings.TrimSpace(token)
	}
	if chatSet {
		next.ChatID = strings.TrimSpace(chat)
	}

	// Пустой токен — это «выключить», а не «включить без токена»: иначе
	// установка падала бы на попытке сохранить заведомо нерабочую настройку.
	next.Enabled = next.Token != "" && next.ChatID != ""

	if err := st.SaveNotifyConfig(ctx, next); err != nil {
		return fmt.Errorf("настройка оповещений: %w", err)
	}
	// Токен в лог не попадает: логи установки читают и пересылают в багрепортах.
	log.Info("оповещения в телеграм настроены из окружения",
		"включены", next.Enabled, "чат", next.ChatID)
	return nil
}
