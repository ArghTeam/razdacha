// Package notify отправляет оповещения наружу. Транспорт пока один — телеграм.
//
// Пакет намеренно ничего не знает ни о туннелях, ни о расписании: он получает
// готовый текст. События подключаются отдельной задачей, и проверять транспорт
// нужно уметь без них.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrNotConfigured — отправлять нечем: нет токена или чата.
var ErrNotConfigured = errors.New("оповещения не настроены")

// defaultTimeout — потолок на один запрос к телеграму.
//
// Отправку дёргает HTTP-обработчик кнопки «отправить тестовое», и висеть на
// чужом сервере дольше пользовательского терпения он не должен. Для фоновых
// оповещений этого тоже достаточно: недоставленное сообщение о падении туннеля
// не стоит того, чтобы держать горутину минутами.
const defaultTimeout = 10 * time.Second

// apiBase — адрес Bot API. Поле, а не константа, ради тестов: настоящий
// api.telegram.org в них не участвует.
const apiBase = "https://api.telegram.org"

// Telegram — отправитель сообщений через Bot API.
//
// Бота заводит пользователь в @BotFather: своей регистрации у демона быть не
// может, бот принадлежит владельцу сервера.
type Telegram struct {
	base   string
	hc     *http.Client
	token  string
	chatID string
}

// Options — параметры отправителя. Base и HTTPClient подменяются в тестах.
type Options struct {
	Token      string
	ChatID     string
	Base       string
	HTTPClient *http.Client
}

// NewTelegram собирает отправителя. Пустой токен или чат — законное состояние:
// [Telegram.Send] ответит [ErrNotConfigured], а не отправит запрос в никуда.
func NewTelegram(o Options) *Telegram {
	base := o.Base
	if base == "" {
		base = apiBase
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Telegram{base: base, hc: hc, token: o.Token, chatID: o.ChatID}
}

// telegramResponse — общая форма ответа Bot API. При ошибке `description`
// объясняет причину, и она полезнее кода: «chat not found» и «Unauthorized»
// требуют разных действий от пользователя.
type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	ErrorCode   int    `json:"error_code"`
}

// Send отправляет текст в настроенный чат.
//
// Ошибки возвращаются на русском: они доходят до панели и показываются как
// есть. Молчаливого провала быть не должно — уведомления, которые не доехали и
// не пожаловались, хуже отсутствия уведомлений.
func (t *Telegram) Send(ctx context.Context, text string) error {
	if t.token == "" || t.chatID == "" {
		return ErrNotConfigured
	}

	body, err := json.Marshal(map[string]any{
		"chat_id": t.chatID,
		"text":    text,
	})
	if err != nil {
		return fmt.Errorf("сборка сообщения: %w", err)
	}

	url := t.base + "/bot" + t.token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("запрос к телеграму: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.hc.Do(req)
	if err != nil {
		// Текст ошибки транспорта содержит URL, а в URL лежит токен.
		return fmt.Errorf("телеграм недоступен: проверьте сеть на сервере")
	}
	defer func() { _ = resp.Body.Close() }()

	var out telegramResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("телеграм ответил непонятным (код %d)", resp.StatusCode)
	}
	if out.OK {
		return nil
	}
	return fmt.Errorf("телеграм отказал: %s", explain(out))
}

// explain переводит отказ Bot API в подсказку, что чинить. Голое английское
// описание пользователю панели ничего не говорит.
func explain(r telegramResponse) string {
	switch r.ErrorCode {
	case http.StatusUnauthorized:
		return "токен бота неверен или отозван"
	case http.StatusForbidden:
		return "бот заблокирован в этом чате или не добавлен в него"
	case http.StatusBadRequest:
		if r.Description == "" {
			return "чат не найден — проверьте идентификатор"
		}
		return "чат не найден или запрос отвергнут (" + r.Description + ")"
	}
	if r.Description != "" {
		return r.Description
	}
	return fmt.Sprintf("код %d", r.ErrorCode)
}
