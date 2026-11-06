package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRestoreAuthenticationReset(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.SetPasswordHash(ctx, "restored-password-hash"); err != nil {
		t.Fatal(err)
	}
	old, err := s.PasswordState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	for _, token := range []string{"live", "revoked", "expired"} {
		expiry := now.Add(time.Hour)
		if token == "expired" {
			expiry = now.Add(-time.Hour)
		}
		if err := s.CreateSession(ctx, Session{TokenHash: token, CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: expiry}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ResetSessionsAfterRestore(ctx); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"live", "revoked", "expired"} {
		if _, err := s.Session(ctx, token, now); !errors.Is(err, ErrNotFound) {
			t.Fatalf("saved token %s: %v", token, err)
		}
	}
	fresh, err := s.PasswordState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Hash != old.Hash || fresh.Generation == old.Generation {
		t.Fatal("restore must preserve password and rotate generation")
	}
	sess := Session{TokenHash: "fresh-login", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.CreateVerifiedSession(ctx, sess, old); !errors.Is(err, ErrPasswordChanged) {
		t.Fatalf("old verification accepted: %v", err)
	}
	if err := s.CreateVerifiedSession(ctx, sess, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, sess.TokenHash, now); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreAuthenticationResetRollback(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.SetPasswordHash(ctx, "preserved-hash"); err != nil {
		t.Fatal(err)
	}
	before, err := s.PasswordState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	if err := s.CreateSession(ctx, Session{TokenHash: "saved", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_generation BEFORE UPDATE ON settings WHEN NEW.key='auth_password_generation' BEGIN SELECT RAISE(ABORT,'injected generation failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetSessionsAfterRestore(ctx); err == nil {
		t.Fatal("injected failure accepted")
	}
	if _, err := s.Session(ctx, "saved", now); err != nil {
		t.Fatalf("session deletion did not roll back: %v", err)
	}
	after, err := s.PasswordState(ctx)
	if err != nil || after != before {
		t.Fatalf("authentication state changed on failure: %v", err)
	}
}
