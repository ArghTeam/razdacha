package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPasswordResetRejectsInFlightLogin(t *testing.T) {
	ts := newTestServer(t)
	nextHash, err := HashPassword("synthetic-new-password")
	if err != nil {
		t.Fatal(err)
	}
	oldCookie := ts.login(t)
	resetCompleted := false
	// The injected clock resets the password after verification, immediately
	// before the session write. No timing or scheduler assumptions are needed.
	ts.Server.now = func() time.Time {
		if !resetCompleted {
			if err := ts.st.SetPasswordHash(context.Background(), nextHash); err != nil {
				t.Fatal(err)
			}
			resetCompleted = true
		}
		return ts.clock()
	}
	res := ts.do(t, request{method: http.MethodPost, path: "/api/login", body: loginBody(testPassword)})
	if !resetCompleted {
		t.Fatal("reset interleaving did not execute")
	}
	if res.code != http.StatusUnauthorized || res.sessionCookie() != nil {
		t.Fatalf("stale login returned %d or published a cookie", res.code)
	}
	if got := ts.do(t, request{method: http.MethodGet, path: "/api/session", cookies: []*http.Cookie{oldCookie}}); got.code != http.StatusUnauthorized {
		t.Fatalf("old session survived reset: %d", got.code)
	}
	if got := ts.do(t, request{method: http.MethodPost, path: "/api/login", body: loginBody("synthetic-new-password")}); got.code != http.StatusOK || got.sessionCookie() == nil {
		t.Fatalf("new password login failed: %d", got.code)
	}
}

func TestPasswordVerificationQueuedAcrossReset(t *testing.T) {
	ts := newTestServer(t)
	// Occupy every verification slot, as concurrent Argon2 jobs would.
	for i := 0; i < cap(ts.verify); i++ {
		ts.verify <- struct{}{}
	}
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		_, ok, err := ts.checkPassword(httptest.NewRequest(http.MethodPost, "/api/login", nil), testPassword)
		if err == nil && ok {
			err = errors.New("queued verification accepted the previous password")
		}
		done <- err
	}()
	<-started
	next, err := HashPassword("synthetic-new-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.st.SetPasswordHash(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(ts.verify); i++ {
		<-ts.verify
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued verification did not finish")
	}
}
