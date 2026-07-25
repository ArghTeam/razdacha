package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

const (
	// sessionCookie — имя cookie сессии.
	sessionCookie = "razdacha_session"

	// sessionTokenLen — 32 байта из crypto/rand: 256 бит энтропии, перебирать
	// нечего, поэтому токен хешируется быстрым SHA-256, а не argon2.
	sessionTokenLen = 32

	// sessionTTL — срок жизни сессии. Сутки: панель открывают с телефона и
	// нечасто, а бессрочная cookie у публичной панели — подарок тому, кто её увёл.
	sessionTTL = 24 * time.Hour

	// sessionRenewAfter — с какого момента сессия продлевается. Продление на
	// каждом запросе означало бы запись в БД на каждый опрос статистики.
	sessionRenewAfter = sessionTTL / 2
)

// newSessionToken выдаёт токен сессии.
func newSessionToken() (string, error) {
	buf := make([]byte, sessionTokenLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("генерация токена сессии: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashToken считает то, что попадает в БД. Сам токен не хранится нигде:
// доступ к файлу БД не должен давать готовую cookie.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// issueSession создаёт сессию и ставит cookie.
func (s *Server) issueSession(ctx context.Context, w http.ResponseWriter) error {
	token, err := newSessionToken()
	if err != nil {
		return err
	}
	now := s.now()
	expires := now.Add(sessionTTL)
	if err := s.store.CreateSession(ctx, store.Session{
		TokenHash: hashToken(token),
		CreatedAt: now,
		ExpiresAt: expires,
	}); err != nil {
		return fmt.Errorf("создание сессии: %w", err)
	}
	http.SetCookie(w, s.sessionCookie(token, expires))
	return nil
}

// sessionCookie собирает cookie сессии.
//
// Secure стоит всегда: панель ходит по HTTPS через nginx, а доступ по SSH-туннелю
// идёт на http://localhost, который браузеры считают надёжным происхождением и
// такую cookie принимают. SameSite=Lax отсекает CSRF на POST со сторонних сайтов.
func (s *Server) sessionCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(sessionTTL / time.Second),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

// clearSessionCookie гасит cookie у клиента.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// session достаёт действующую сессию запроса.
//
// Второе значение — есть ли сессия; ошибка чтения БД от отсутствия сессии
// отличается по третьему значению, потому что «БД недоступна» это 500, а не 401.
func (s *Server) session(r *http.Request) (store.Session, bool, error) {
	// Ошибка здесь одна — cookie нет; это отсутствие сессии, а не сбой.
	cookie, _ := r.Cookie(sessionCookie)
	if cookie == nil || cookie.Value == "" {
		return store.Session{}, false, nil
	}
	hash := hashToken(cookie.Value)

	sess, err := s.store.Session(r.Context(), hash, s.now())
	if errors.Is(err, store.ErrNotFound) {
		// Нет сессии или она истекла — это 401, а не сбой чтения.
		return store.Session{}, false, nil //nolint:nilerr
	}
	if err != nil {
		return store.Session{}, false, err
	}
	// Выборка шла по первичному ключу, но сравнение всё равно постоянное по
	// времени: полагаться на то, что SQLite не даёт временнóго канала, незачем.
	if subtle.ConstantTimeCompare([]byte(sess.TokenHash), []byte(hash)) != 1 {
		return store.Session{}, false, nil
	}
	return sess, true, nil
}

// renew продлевает сессию, если истрачена больше половины срока.
func (s *Server) renew(ctx context.Context, w http.ResponseWriter, sess store.Session, token string) {
	now := s.now()
	if sess.ExpiresAt.Sub(now) > sessionRenewAfter {
		return
	}
	expires := now.Add(sessionTTL)
	if err := s.store.ExtendSession(ctx, sess.TokenHash, expires); err != nil {
		// Непродлённая сессия просто истечёт, ронять запрос из-за этого незачем.
		s.log.Error("продление сессии", "ошибка", err)
		return
	}
	http.SetCookie(w, s.sessionCookie(token, expires))
}
