package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPasswordHashAbsent(t *testing.T) {
	s := open(t)

	if _, err := s.PasswordHash(context.Background()); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("ожидалась ErrNoPassword для пустой БД, получено: %v", err)
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	const encoded = "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"
	if err := s.SetPasswordHash(ctx, encoded); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}
	got, err := s.PasswordHash(ctx)
	if err != nil {
		t.Fatalf("PasswordHash: %v", err)
	}
	if got != encoded {
		t.Errorf("хеш = %q, ожидался %q", got, encoded)
	}
}

// Пароль лежит в той же таблице, что и настройки, но их сохранение его не трогает
// и не отдаёт наружу: ключ не входит в Settings.
func TestSaveSettingsKeepsPasswordHash(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	const encoded = "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"
	if err := s.SetPasswordHash(ctx, encoded); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}
	if err := s.SaveSettings(ctx, DefaultSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := s.PasswordHash(ctx)
	if err != nil {
		t.Fatalf("PasswordHash после SaveSettings: %v", err)
	}
	if got != encoded {
		t.Errorf("хеш пароля затёрт сохранением настроек: %q", got)
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	now := time.Now().Truncate(time.Second)
	sess := Session{TokenHash: "hash-1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.Session(ctx, sess.TokenHash, now)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !got.ExpiresAt.Equal(sess.ExpiresAt.UTC()) {
		t.Errorf("срок сессии = %v, ожидался %v", got.ExpiresAt, sess.ExpiresAt.UTC())
	}

	// Истёкшая сессия не отдаётся, даже пока строка ещё в таблице.
	if _, err := s.Session(ctx, sess.TokenHash, now.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Errorf("ожидалась ErrNotFound для истёкшей сессии, получено: %v", err)
	}

	if err := s.ExtendSession(ctx, sess.TokenHash, now.Add(3*time.Hour)); err != nil {
		t.Fatalf("ExtendSession: %v", err)
	}
	if _, err := s.Session(ctx, sess.TokenHash, now.Add(2*time.Hour)); err != nil {
		t.Errorf("продлённая сессия не читается: %v", err)
	}

	if err := s.DeleteSession(ctx, sess.TokenHash); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.Session(ctx, sess.TokenHash, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("удалённая сессия читается: %v", err)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	now := time.Now().Truncate(time.Second)
	live := Session{TokenHash: "live", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	dead := Session{TokenHash: "dead", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
	for _, sess := range []Session{live, dead} {
		if err := s.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession %s: %v", sess.TokenHash, err)
		}
	}

	n, err := s.DeleteExpiredSessions(ctx, now)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("удалено сессий = %d, ожидалась 1", n)
	}
	if _, err := s.Session(ctx, live.TokenHash, now); err != nil {
		t.Errorf("живая сессия удалена: %v", err)
	}
}

// Смена пароля выкидывает всех: иначе увёденная cookie переживает смену.
func TestSetPasswordHashDropsSessions(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	now := time.Now().Truncate(time.Second)
	if err := s.CreateSession(ctx, Session{
		TokenHash: "hash-1", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.SetPasswordHash(ctx, "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}
	if _, err := s.Session(ctx, "hash-1", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("сессия пережила смену пароля: %v", err)
	}
}

func TestCreateSessionValidates(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Now()

	if err := s.CreateSession(ctx, Session{CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); !errors.Is(err, ErrInvalid) {
		t.Errorf("пустой хеш токена принят: %v", err)
	}
	if err := s.CreateSession(ctx, Session{TokenHash: "h", CreatedAt: now, ExpiresAt: now}); !errors.Is(err, ErrInvalid) {
		t.Errorf("сессия с нулевым сроком принята: %v", err)
	}
}
