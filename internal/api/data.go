package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/store"
)

// maxDataBody — предел тела запроса к эндпоинтам данных. Самое крупное, что сюда
// приходит, — вставленный конфиг туннеля; 256 KiB с запасом.
const maxDataBody = 256 << 10

// decodeBody разбирает тело запроса и сам отвечает на ошибку разбора.
// Второе значение false означает, что ответ уже отправлен.
func (s *Server) decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDataBody))
	if err := dec.Decode(dst); err != nil {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "Некорректный запрос")
		return false
	}
	return true
}

// storeError переводит сентинелы слоя хранения в коды HTTP.
//
// Тексты ошибок store уже написаны для пользователя (например, перечисляют
// правила, мешающие удалить туннель), поэтому доходят до UI как есть; сюда их
// нельзя оборачивать своим префиксом. Всё, что не сентинел, — 500: это сбой БД,
// а не решение пользователя.
func (s *Server) storeError(w http.ResponseWriter, err error, notFound string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, s.log, http.StatusNotFound, codeNotFound, notFound)
	case errors.Is(err, store.ErrInUse):
		writeError(w, s.log, http.StatusConflict, codeConflict, userMessage(err, store.ErrInUse))
	case errors.Is(err, store.ErrInvalid):
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, userMessage(err, store.ErrInvalid))
	default:
		s.log.Error("запрос к хранилищу", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
	}
}

// userMessage снимает с ошибки префикс сентинела: «запись используется: на туннель
// ссылаются правила …» читается пользователю хуже, чем сама причина.
func userMessage(err error, sentinel error) string {
	msg := err.Error()
	if trimmed := strings.TrimPrefix(msg, sentinel.Error()+": "); trimmed != msg {
		msg = trimmed
	}
	return msg
}

// idFrom достаёт {id} из пути и сам отвечает на пустой.
func (s *Server) idFrom(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "Не указан идентификатор")
		return "", false
	}
	return id, true
}

// serverPublicKey отдаёт публичный ключ wg0.
//
// Ключи сервера заводит слой netstack при первом поднятии интерфейса. Пустая
// строка означает «источника нет», и это состояние проходит через API как null,
// а не как выдуманное значение.
func (s *Server) serverPublicKey(r *http.Request) (string, error) {
	if s.serverKey == nil {
		return "", nil
	}
	key, err := s.serverKey(r.Context())
	if err != nil {
		return "", fmt.Errorf("чтение публичного ключа сервера: %w", err)
	}
	return key, nil
}

// peerStats читает замеры с wg0. Ошибка сюда не поднимается: список пиров
// показывается и без статистики, а без списка панель бесполезна. Отсутствие
// данных видно в ответе как null у производных полей.
func (s *Server) peerStats(r *http.Request) map[string]netstack.WGPeerStat {
	if s.peerStat == nil {
		return nil
	}
	stats, err := s.peerStat(r.Context())
	if err != nil {
		s.log.Debug("статистика пиров недоступна", "ошибка", err)
		return nil
	}
	return stats
}
