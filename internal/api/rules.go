package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/ArghTeam/razdacha/internal/store"
)

// handleListRules — `GET /api/rules`, по возрастанию priority.
func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Rules(r.Context())
	if err != nil {
		s.storeError(w, err, "Правило не найдено")
		return
	}
	if list == nil {
		list = []store.Rule{}
	}
	writeJSON(w, s.log, http.StatusOK, list)
}

// handleCreateRule — `POST /api/rules`. Приоритет назначает store, добавляя
// правило в конец; присланный приоритет игнорируется.
func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	rule := store.Rule{Enabled: true, PeerScope: store.ScopeAll}
	if !s.decodeInto(w, r, &rule) {
		return
	}
	rule.ID = ""
	rule.Priority = 0
	rule.Name = strings.TrimSpace(rule.Name)
	if !s.checkChain(w, r, rule) {
		return
	}

	created, err := s.store.CreateRule(r.Context(), rule)
	if err != nil {
		s.storeError(w, err, "Туннель правила не найден")
		return
	}
	writeJSON(w, s.log, http.StatusCreated, created)
}

// handleUpdateRule — `PATCH /api/rules/{id}`, частичное обновление.
//
// Тело накладывается на прочитанное правило: присланные поля перезаписываются,
// остальные остаются. Идентификатор и приоритет восстанавливаются после разбора —
// порядок меняется только через `PUT /api/rules/order`.
func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	rule, err := s.store.Rule(r.Context(), id)
	if err != nil {
		s.storeError(w, err, "Правило не найдено")
		return
	}

	priority := rule.Priority
	if !s.decodeInto(w, r, &rule) {
		return
	}
	rule.ID = id
	rule.Priority = priority
	rule.Name = strings.TrimSpace(rule.Name)
	if !s.checkChain(w, r, rule) {
		return
	}

	if err := s.store.UpdateRule(r.Context(), rule); err != nil {
		s.storeError(w, err, "Правило не найдено")
		return
	}
	writeJSON(w, s.log, http.StatusOK, rule)
}

// handleDeleteRule — `DELETE /api/rules/{id}`. Приоритеты оставшихся сжимает store.
func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteRule(r.Context(), id); err != nil {
		s.storeError(w, err, "Правило не найдено")
		return
	}
	writeJSON(w, s.log, http.StatusOK, map[string]bool{"ok": true})
}

// checkChain проверяет второе звено цепи: им бывает только туннель WARP
// (ADR 0012). Второе значение false означает, что ответ уже отправлен.
//
// Проверка здесь, а не в store и не в генераторе: слою хранения одного правила
// не хватает — нужен туннель, — а `sing-box check` увидел бы только «конфиг не
// собрался». Пользователю нужна причина, и на русском.
func (s *Server) checkChain(w http.ResponseWriter, r *http.Request, rule store.Rule) bool {
	if rule.ViaTunnelID == "" {
		return true
	}
	via, err := s.store.Tunnel(r.Context(), rule.ViaTunnelID)
	if err != nil {
		s.storeError(w, err, "Второе звено цепи — туннель не найден")
		return false
	}
	if via.Source != store.SourceWARP {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest,
			"Вторым звеном цепи бывает только WARP, а туннель «"+via.Name+"» заведён иначе. "+
				"Добавьте WARP кнопкой на экране туннелей или вставьте его .conf.")
		return false
	}
	// Повтор возможен именно потому, что первым звеном годится любой туннель,
	// включая сам WARP: «WARP → тот же WARP» — это detour на самого себя, то
	// есть цепь из одного звена, записанная дважды.
	if rule.ViaTunnelID == rule.TunnelID {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest,
			"Туннель «"+via.Name+"» уже стоит первым звеном — вторым звеном нужен другой, "+
				"иначе трафик выходил бы через него же.")
		return false
	}
	return true
}

// reorderRequest — тело `PUT /api/rules/order`: полный список идентификаторов.
type reorderRequest struct {
	IDs []string `json:"ids"`
}

// handleReorderRules — `PUT /api/rules/order`.
//
// Порядок меняется целиком и одной транзакцией: промежуточных состояний с
// дублирующимися приоритетами быть не должно, а из PATCH каждого правила они
// получались бы неизбежно.
func (s *Server) handleReorderRules(w http.ResponseWriter, r *http.Request) {
	var req reorderRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	if err := s.store.ReorderRules(r.Context(), req.IDs); err != nil {
		s.storeError(w, err, "Правило не найдено")
		return
	}
	s.handleListRules(w, r)
}

// decodeInto накладывает тело запроса на уже заполненную структуру: поля, которых
// в теле нет, сохраняют прежнее значение. Так PATCH получается частичным без
// отдельной структуры с указателем на каждое поле.
func (s *Server) decodeInto(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDataBody))
	if err != nil {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "Некорректный запрос")
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "Некорректный запрос")
		return false
	}
	return true
}
