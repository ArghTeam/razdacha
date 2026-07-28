package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// routes собирает маршруты.
//
// Открыт ровно один путь — вход. Всё остальное под `/api/`, включая ещё не
// написанные эндпоинты, закрыто одним общим обработчиком: новый маршрут,
// который забыли обернуть проверкой сессии, отдаст 401, а не данные.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/login", s.handleLogin)
	s.protect(mux, "POST /api/logout", s.handleLogout)
	s.protect(mux, "GET /api/session", s.handleSession)

	s.protect(mux, "GET /api/peers", s.handleListPeers)
	s.protect(mux, "POST /api/peers", s.handleCreatePeer)
	s.protect(mux, "PATCH /api/peers/{id}", s.handleUpdatePeer)
	s.protect(mux, "DELETE /api/peers/{id}", s.handleDeletePeer)
	s.protect(mux, "GET /api/peers/{id}/config", s.handlePeerConfig)

	s.protect(mux, "GET /api/tunnels", s.handleListTunnels)
	s.protect(mux, "POST /api/tunnels", s.handleCreateTunnel)
	s.protect(mux, "POST /api/tunnels/parse", s.handleParseTunnel)
	s.protect(mux, "POST /api/tunnels/warp", s.handleAddWARP)
	s.protect(mux, "PATCH /api/tunnels/{id}", s.handleUpdateTunnel)
	s.protect(mux, "DELETE /api/tunnels/{id}", s.handleDeleteTunnel)
	s.protect(mux, "POST /api/tunnels/{id}/check", s.handleCheckTunnel)
	s.protect(mux, "POST /api/tunnels/{id}/refresh", s.handleRefreshPool)
	s.protect(mux, "GET /api/tunnels/{id}/pool", s.handlePoolServers)
	s.protect(mux, "GET /api/tunnels/{id}/raw", s.handleTunnelRaw)

	s.protect(mux, "GET /api/rules", s.handleListRules)
	s.protect(mux, "POST /api/rules", s.handleCreateRule)
	s.protect(mux, "PUT /api/rules/order", s.handleReorderRules)
	s.protect(mux, "PATCH /api/rules/{id}", s.handleUpdateRule)
	s.protect(mux, "DELETE /api/rules/{id}", s.handleDeleteRule)

	s.protect(mux, "GET /api/lists/community", s.handleCommunityLists)

	s.protect(mux, "GET /api/settings", s.handleSettings)
	s.protect(mux, "PATCH /api/settings", s.handleUpdateSettings)

	s.protect(mux, "GET /api/notify", s.handleNotify)
	s.protect(mux, "PUT /api/notify", s.handleUpdateNotify)
	s.protect(mux, "POST /api/notify/test", s.handleNotifyTest)

	s.protect(mux, "POST /api/apply", s.handleApply)

	s.protect(mux, "GET /api/version", s.handleVersion)

	s.protect(mux, "GET /api/diag", s.handleDiag)
	s.protect(mux, "POST /api/diag/run", s.handleDiag)

	mux.Handle("/api/", s.requireSession(http.HandlerFunc(s.handleUnknown)))

	// Всё, что не `/api/`, — интерфейс. Более длинный шаблон у ServeMux
	// выигрывает, поэтому REST-путь в SPA не проваливается: несуществующий
	// `/api/…` отдаёт JSON-ошибку, а не страницу входа со статусом 200.
	mux.Handle("/", s.static())

	return mux
}

// protect регистрирует маршрут под проверкой сессии. Отдельная функция ровно для
// того, чтобы регистрация без неё бросалась в глаза при чтении списка маршрутов.
func (s *Server) protect(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	mux.Handle(pattern, s.requireSession(h))
}

// requireSession закрывает обработчик проверкой сессии и заодно продлевает её.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok, err := s.session(r)
		if err != nil {
			s.log.Error("проверка сессии", "ошибка", err)
			writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
			return
		}
		if !ok {
			// Cookie есть, но сессии нет — гасим её, чтобы браузер не слал
			// протухший токен в каждом запросе.
			if c, cerr := r.Cookie(sessionCookie); cerr == nil && c.Value != "" {
				clearSessionCookie(w)
			}
			writeError(w, s.log, http.StatusUnauthorized, codeUnauthorized, "Требуется вход")
			return
		}
		if c, cerr := r.Cookie(sessionCookie); cerr == nil {
			s.renew(r.Context(), w, sess, c.Value)
		}
		next.ServeHTTP(w, r)
	})
}

// loginRequest — тело `POST /api/login`.
type loginRequest struct {
	Password string `json:"password"`
}

// handleLogin проверяет пароль и выдаёт сессию.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	key := clientKey(r)
	if left := s.throttle.retryAfter(key); left > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(left.Seconds()+0.5)))
		s.log.Warn("вход отклонён блокировкой", "адрес", key, "осталось", left)
		writeError(w, s.log, http.StatusTooManyRequests, codeTooMany,
			fmt.Sprintf("Слишком много попыток входа. Повторите через %s.", humanDuration(left)))
		return
	}

	var req loginRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody))
	if err := dec.Decode(&req); err != nil {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "Некорректный запрос")
		return
	}

	ok, err := s.checkPassword(r, req.Password)
	if err != nil {
		s.log.Error("проверка пароля", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
		return
	}
	if !ok {
		delay := s.throttle.fail(key)
		s.log.Warn("неверный пароль", "адрес", key)
		s.sleep(r.Context(), delay)
		writeError(w, s.log, http.StatusUnauthorized, codeUnauthorized, "Неверный пароль")
		return
	}

	s.throttle.success(key)
	if err := s.issueSession(r.Context(), w); err != nil {
		s.log.Error("выдача сессии", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
		return
	}
	s.log.Info("вход в панель", "адрес", key)
	writeJSON(w, s.log, http.StatusOK, map[string]bool{"ok": true})
}

// checkPassword считает argon2id под семафором: параллельные проверки иначе
// выедают память быстрее, чем блокировка успевает сработать.
func (s *Server) checkPassword(r *http.Request, password string) (bool, error) {
	encoded, err := s.store.PasswordHash(r.Context())
	if err != nil {
		return false, fmt.Errorf("чтение пароля панели: %w", err)
	}

	select {
	case s.verify <- struct{}{}:
		defer func() { <-s.verify }()
	case <-r.Context().Done():
		return false, r.Context().Err()
	}

	// Испорченный хеш (ErrBadHash) — тоже ошибка, а не «неверный пароль»:
	// пускать нельзя, но и списывать это на пользователя нельзя.
	return VerifyPassword(encoded, password)
}

// handleLogout удаляет сессию и гасит cookie.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := s.store.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			s.log.Error("выход из панели", "ошибка", err)
			writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
			return
		}
	}
	clearSessionCookie(w)
	writeJSON(w, s.log, http.StatusOK, map[string]bool{"ok": true})
}

// sessionResponse — тело `GET /api/session`.
type sessionResponse struct {
	Authenticated bool      `json:"authenticated"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// handleSession сообщает UI, что сессия жива, и до каких пор.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sess, ok, err := s.session(r)
	if err != nil || !ok {
		// Сюда не попасть мимо requireSession, но отвечать наугад нечем.
		writeError(w, s.log, http.StatusUnauthorized, codeUnauthorized, "Требуется вход")
		return
	}
	writeJSON(w, s.log, http.StatusOK, sessionResponse{
		Authenticated: true,
		ExpiresAt:     sess.ExpiresAt.UTC(),
	})
}

// handleUnknown отвечает на всё под `/api/`, чего ещё нет. Интерфейс различает
// «эндпоинта нет» и «данных нет» именно по этому 404 и говорит пользователю
// «данные появятся позже» вместо пустого экрана.
func (s *Server) handleUnknown(w http.ResponseWriter, _ *http.Request) {
	writeError(w, s.log, http.StatusNotFound, codeNotFound, "Такого пути нет")
}

// humanDuration переводит остаток блокировки в текст для пользователя.
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d с", int(d.Seconds()+0.5))
	}
	return fmt.Sprintf("%d мин", int(d.Minutes()+0.5))
}
