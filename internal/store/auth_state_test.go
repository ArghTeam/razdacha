package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifiedSessionAcrossConnections(t *testing.T) {
	for _, sequence := range []struct {
		name   string
		hashes []string
	}{
		{"different hash", []string{"next"}},
		{"same hash", []string{"initial"}},
		{"ABA hash", []string{"next", "initial"}},
	} {
		t.Run(sequence.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "auth.db")
			reader, writer := openAt(t, path), openAt(t, path)
			if err := writer.SetPasswordHash(ctx, "initial"); err != nil {
				t.Fatal(err)
			}
			stale, err := reader.PasswordState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().Truncate(time.Second)
			before := Session{TokenHash: "before", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
			if err := reader.CreateVerifiedSession(ctx, before, stale); err != nil {
				t.Fatal(err)
			}
			for _, hash := range sequence.hashes {
				if err := writer.SetPasswordHash(ctx, hash); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := reader.Session(ctx, before.TokenHash, now); !errors.Is(err, ErrNotFound) {
				t.Fatalf("reset retained session: %v", err)
			}
			staleSession := before
			staleSession.TokenHash = "stale"
			if err := reader.CreateVerifiedSession(ctx, staleSession, stale); !errors.Is(err, ErrPasswordChanged) {
				t.Fatalf("stale verification accepted: %v", err)
			}
			if _, err := writer.Session(ctx, staleSession.TokenHash, now); !errors.Is(err, ErrNotFound) {
				t.Fatalf("stale session exists: %v", err)
			}
			fresh, err := reader.PasswordState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Generation == stale.Generation {
				t.Fatal("reset did not rotate generation")
			}
			if err := reader.CreateVerifiedSession(ctx, staleSession, fresh); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Session(ctx, staleSession.TokenHash, now); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLegacyPasswordState(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, err := s.PasswordState(ctx); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("absent password: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, keyPasswordHash, "legacy"); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.PasswordState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Generation != "" || legacy.Hash != "legacy" {
		t.Fatal("unexpected legacy state")
	}
	now := time.Now()
	sess := Session{TokenHash: "legacy", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.CreateVerifiedSession(ctx, sess, legacy); err != nil {
		t.Fatal(err)
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	retained, err := s.PasswordState(ctx)
	if err != nil || retained != legacy {
		t.Fatalf("settings changed auth state: %v", err)
	}
	if err := s.SetPasswordHash(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateVerifiedSession(ctx, sess, legacy); !errors.Is(err, ErrPasswordChanged) {
		t.Fatalf("legacy state survived reset: %v", err)
	}
}
