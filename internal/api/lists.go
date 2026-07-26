package api

import (
	"net/http"

	"github.com/ArghTeam/razdacha/internal/lists"
)

// handleCommunityLists — `GET /api/lists/community`: каталог готовых списков
// сервисов для формы правила (docs/05-api.md).
//
// Ответ не зависит ни от БД, ни от загруженных списков: каталог описывает
// состав allow-domains, а не состояние демона. Поэтому эндпоинт отвечает и
// тогда, когда планировщик ещё ничего не скачал или скачать не смог — иначе
// при недоступном GitHub в панели было бы нечего выбрать.
func (s *Server) handleCommunityLists(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.log, http.StatusOK, lists.Catalog())
}
