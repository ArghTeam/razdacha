package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// Applier применяет состояние БД к работающему sing-box. Интерфейс, а не
// *singbox.Applier, ровно ради тестов: в них ни рантайма, ни systemd нет.
type Applier interface {
	Apply(ctx context.Context, snap store.Snapshot) (singbox.ApplyResult, error)
}

// applyResponse — тело `POST /api/apply`.
//
// `changed` отвечает на вопрос «а было ли что применять»: изменения через REST
// ложатся в БД сразу, но применяются пакетно, и UI показывает плашку с кнопкой
// (docs/05-api.md). Нажатие без изменений — обычное дело, и это не ошибка.
type applyResponse struct {
	Changed  bool   `json:"changed"`
	Reloaded bool   `json:"reloaded"`
	Path     string `json:"path"`
	Detail   string `json:"detail"`
}

// handleApply пересобирает конфиг sing-box из состояния и применяет его.
//
// Отказ `sing-box check` — 422 с текстом ошибки рантайма: прежний конфиг остался
// в силе, и пользователю нужна причина, а не код (docs/05-api.md).
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	snap, err := s.store.Snapshot(r.Context())
	if err != nil {
		s.storeError(w, err, "Состояние не прочитано")
		return
	}

	res, err := s.applier.Apply(r.Context(), snap)
	switch {
	case errors.Is(err, singbox.ErrCheckFailed):
		s.log.Error("конфиг sing-box не применён", "ошибка", err)
		writeError(w, s.log, http.StatusUnprocessableEntity, codeInvalidConfig, err.Error())
		return
	case errors.Is(err, singbox.ErrReloadFailed):
		// Конфиг записан и валиден, перечитать его не удалось. Это не 422:
		// возвращать нечего откатывать, но и «применено» сказать нельзя.
		s.log.Error("sing-box не перезагружен", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, err.Error())
		return
	case err != nil:
		s.log.Error("применение конфигурации", "ошибка", err)
		writeError(w, s.log, http.StatusUnprocessableEntity, codeInvalidConfig, err.Error())
		return
	}

	writeJSON(w, s.log, http.StatusOK, applyResponse{
		Changed:  res.Changed,
		Reloaded: res.Reloaded,
		Path:     res.Path,
		Detail:   applyDetail(res),
	})
}

// applyDetail — строка для плашки в UI. Текст здесь, а не в UI, потому что
// различить «нечего применять» и «применено» может только эта сторона.
func applyDetail(res singbox.ApplyResult) string {
	if !res.Changed {
		return "Конфигурация не изменилась, sing-box не перезапускался"
	}
	if !res.Reloaded {
		return "Конфигурация записана, но sing-box не перезагружен"
	}
	return "Конфигурация применена, sing-box перезагружен"
}
