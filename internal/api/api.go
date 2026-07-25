// Package api — HTTP-слой демона: сервер панели, авторизация и REST.
//
// Панель доступна из интернета (docs/decisions/0009-public-panel-access.md), но
// сам демон слушает только loopback: TLS, редирект с 80 и первичный отсекатель
// ботов остаются на nginx (docs/decisions/0008-nginx-before-panel.md). Отсюда
// два следствия, на которых держится весь пакет:
//
//   - пароль — единственная преграда к управлению маршрутизацией всего трафика,
//     поэтому хеш argon2id, постоянное по времени сравнение и блокировка после
//     серии неудач не «на будущее», а условие работоспособности;
//   - реальный адрес клиента приходит в заголовке, и доверять заголовку можно
//     только от loopback — иначе адрес подделает кто угодно, см. [clientKey].
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// Ошибки слоя. Тексты доходят до пользователя как есть.
var (
	// ErrNoPassword — пароль панели не задан. Демон с ним не стартует:
	// пустой и дефолтный пароль невозможны (ADR 0009).
	ErrNoPassword = errors.New("пароль панели не задан")

	// ErrWeakPassword — пароль не проходит проверку до хеширования.
	ErrWeakPassword = errors.New("пароль слишком короткий")

	// ErrBadHash — сохранённый хеш пароля не разбирается.
	ErrBadHash = errors.New("хеш пароля не разобран")

	// ErrPublicListen — попытка привязать демон к неloopback-адресу.
	// Наружу панель открывает nginx, демон снаружи не виден (ADR 0008).
	ErrPublicListen = errors.New("демон нельзя открыть на публичном адресе")

	// ErrBadConfig — параметры сервера не проходят проверку.
	ErrBadConfig = errors.New("некорректные параметры сервера панели")
)

// errorResponse — формат ошибки из docs/05-api.md.
type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// Коды ошибок. UI различает ситуации по ним, а не по тексту.
const (
	codeUnauthorized  = "unauthorized"
	codeBadRequest    = "bad_request"
	codeTooMany       = "too_many_attempts"
	codeNotFound      = "not_found"
	codeConflict      = "conflict"
	codeNotReady      = "not_ready"
	codeInvalidConfig = "invalid_config"
	codeInternal      = "internal"
)

// writeJSON отдаёт тело ответа. Ошибка записи означает оборванное соединение —
// отвечать на неё уже нечем, поэтому она только пишется в лог.
func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Error("сериализация ответа", "ошибка", err)
		http.Error(w, `{"error":"Внутренняя ошибка","code":"internal"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		log.Debug("ответ не дописан", "ошибка", err)
	}
}

// writeError отдаёт ошибку в формате docs/05-api.md.
func writeError(w http.ResponseWriter, log *slog.Logger, status int, code, message string) {
	writeJSON(w, log, status, errorResponse{Error: message, Code: code})
}
