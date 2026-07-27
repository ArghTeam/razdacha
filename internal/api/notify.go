package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ArghTeam/razdacha/internal/notify"
	"github.com/ArghTeam/razdacha/internal/store"
)

// testMessage — текст пробного сообщения. Он должен быть узнаваем в чате, где
// уже могут лежать оповещения: «тест» без источника ни о чём не говорит.
const testMessage = "razdacha: проверка связи. Если вы это видите, оповещения настроены."

// notifyResponse — настройки оповещений для UI.
//
// Токена здесь нет и не будет: он секрет, и вернуть его наружу означало бы
// раздать его всякому, кто дотянулся до сессии. Вместо значения — признак
// `token_set`, по которому интерфейс показывает «сохранён» вместо пустого поля.
type notifyResponse struct {
	Enabled  bool   `json:"enabled"`
	ChatID   string `json:"chat_id"`
	TokenSet bool   `json:"token_set"`
}

func newNotifyResponse(c store.NotifyConfig) notifyResponse {
	return notifyResponse{Enabled: c.Enabled, ChatID: c.ChatID, TokenSet: c.Token != ""}
}

// notifyRequest — тело `PUT /api/notify`.
//
// Токен — указатель: «не прислали» и «прислали пустой» это разные намерения.
// Не прислали — оставить прежний, иначе сохранение галочки стирало бы секрет,
// которого в форме и не было.
type notifyRequest struct {
	Enabled *bool   `json:"enabled"`
	ChatID  *string `json:"chat_id"`
	Token   *string `json:"token"`
}

func (req notifyRequest) apply(c store.NotifyConfig) store.NotifyConfig {
	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}
	if req.ChatID != nil {
		c.ChatID = strings.TrimSpace(*req.ChatID)
	}
	if req.Token != nil {
		c.Token = strings.TrimSpace(*req.Token)
	}
	return c
}

// handleNotify — `GET /api/notify`.
func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.NotifyConfig(r.Context())
	if err != nil {
		s.storeError(w, err, "Настройки оповещений не прочитаны")
		return
	}
	writeJSON(w, s.log, http.StatusOK, newNotifyResponse(c))
}

// handleUpdateNotify — `PUT /api/notify`.
func (s *Server) handleUpdateNotify(w http.ResponseWriter, r *http.Request) {
	var req notifyRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	current, err := s.store.NotifyConfig(r.Context())
	if err != nil {
		s.storeError(w, err, "Настройки оповещений не прочитаны")
		return
	}
	next := req.apply(current)
	if err := s.store.SaveNotifyConfig(r.Context(), next); err != nil {
		s.storeError(w, err, "Настройки оповещений не сохранены")
		return
	}
	writeJSON(w, s.log, http.StatusOK, newNotifyResponse(next))
}

// handleNotifyTest — `POST /api/notify/test`.
//
// Отправка идёт и при выключенных оповещениях: проверять канал приходится до
// того, как его включишь, иначе галочку ставят вслепую. Это единственный
// исходящий запрос к телеграму, который делает демон в этой задаче, и делает он
// его по явному нажатию, а не сам.
func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.NotifyConfig(r.Context())
	if err != nil {
		s.storeError(w, err, "Настройки оповещений не прочитаны")
		return
	}
	if c.Token == "" || c.ChatID == "" {
		writeError(w, s.log, http.StatusConflict, codeNotReady,
			"Сначала сохраните токен бота и идентификатор чата.")
		return
	}

	sender := s.notifier(c)
	if err := sender.Send(r.Context(), testMessage); err != nil {
		if errors.Is(err, notify.ErrNotConfigured) {
			writeError(w, s.log, http.StatusConflict, codeNotReady,
				"Сначала сохраните токен бота и идентификатор чата.")
			return
		}
		s.log.Warn("пробное оповещение", "ошибка", err)
		writeError(w, s.log, http.StatusBadGateway, codeInternal,
			"Отправить не удалось. "+err.Error())
		return
	}
	writeJSON(w, s.log, http.StatusOK, map[string]string{"detail": "Сообщение отправлено."})
}

// notifier собирает отправителя по сохранённой конфигурации. Подменяется в
// тестах: настоящий api.telegram.org в них не участвует.
func (s *Server) notifier(c store.NotifyConfig) notifySender {
	if s.notify != nil {
		return s.notify(c)
	}
	return notify.NewTelegram(notify.Options{Token: c.Token, ChatID: c.ChatID})
}
