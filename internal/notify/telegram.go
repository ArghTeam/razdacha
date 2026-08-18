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
	"mime/multipart"
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

// MaxDocumentSize — потолок загрузки файла ботом в телеграм (50 МиБ). Больше
// он не принимает, и узнать об этом лучше до того, как файл уедет наполовину.
const MaxDocumentSize = 50 << 20

// documentTimeout — потолок на загрузку файла. Отдельный от [defaultTimeout]:
// десяти секунд на мегабайты по узкому каналу VPS не хватает, а копия
// состояния — не сообщение, ради которого нельзя подождать.
const documentTimeout = 2 * time.Minute

// SendDocument отправляет файл в тот же чат.
//
// Тем же ботом и тем же транспортом, что и сообщения: второго канала наружу у
// демона нет и не заводится (ADR 0016). Содержимое приезжает целиком в памяти —
// его всё равно нужно шифровать целиком, а поток телеграму пришлось бы
// пересчитывать в multipart руками.
func (t *Telegram) SendDocument(ctx context.Context, filename string, data []byte, caption string) error {
	if t.token == "" || t.chatID == "" {
		return ErrNotConfigured
	}
	if len(data) > MaxDocumentSize {
		return fmt.Errorf("файл больше %d МиБ — телеграм такой не примет", MaxDocumentSize>>20)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("chat_id", t.chatID); err != nil {
		return fmt.Errorf("сборка сообщения: %w", err)
	}
	if caption != "" {
		if err := mw.WriteField("caption", caption); err != nil {
			return fmt.Errorf("сборка сообщения: %w", err)
		}
	}
	part, err := mw.CreateFormFile("document", filename)
	if err != nil {
		return fmt.Errorf("сборка сообщения: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("сборка сообщения: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("сборка сообщения: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, documentTimeout)
	defer cancel()

	url := t.base + "/bot" + t.token + "/sendDocument"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return fmt.Errorf("запрос к телеграму: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := t.client(documentTimeout).Do(req)
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

// client отдаёт клиента с потолком не меньше требуемого. Потолок клиента
// действует на весь запрос и перебивает контекст: с десятью секундами от
// [defaultTimeout] загрузка файла обрывалась бы независимо от того, сколько
// времени ей дали снаружи.
func (t *Telegram) client(timeout time.Duration) *http.Client {
	if t.hc.Timeout == 0 || t.hc.Timeout >= timeout {
		return t.hc
	}
	clone := *t.hc
	clone.Timeout = timeout
	return &clone
}

// explain переводит отказ Bot API в подсказку, что чинить. Голое английское
// описание пользователю панели ничего не говорит.
func explain(r telegramResponse) string {
	switch r.ErrorCode {
	case http.StatusUnauthorized:
		return "токен бота неверен или отозван"
	case http.StatusNotFound:
		// Токен подставляется прямо в путь URL, поэтому испорченный даёт не 401,
		// а 404: телеграм не находит метод, а не отказывает в доступе. Без этой
		// ветки пользователь получал английское «Not Found» и не понимал, что
		// чинить — проверено на живом боте.
		return "токен бота неверен: телеграм не знает такого бота"
	case http.StatusForbidden:
		return "бот заблокирован в этом чате или не добавлен в него"
	case http.StatusBadRequest:
		if r.Description == "" {
			return "чат не найден — проверьте идентификатор"
		}
		return "чат не найден или запрос отвергнут (" + r.Description + ")"
	}
	// Английское описание от телеграма пользователю панели ничего не говорит,
	// но и терять его нельзя: без него непонятная ошибка станет безымянной.
	if r.Description != "" {
		return fmt.Sprintf("неожиданный отказ (код %d, %s)", r.ErrorCode, r.Description)
	}
	return fmt.Sprintf("неожиданный отказ (код %d)", r.ErrorCode)
}
