package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

type onboardingGateStub struct {
	fakeApplier
	fail  error
	calls int
}

func (g *onboardingGateStub) ConfigureOnboarding(context.Context, *store.Store, OnboardingRequest) (store.OnboardingState, error) {
	g.calls++
	return store.OnboardingState{Pending: true, Prepared: true, Expected: "secret-internal-fingerprint"}, g.fail
}

func (g *onboardingGateStub) ApplyOnboarding(context.Context, *store.Store) (singbox.ApplyResult, error) {
	g.calls++
	return singbox.ApplyResult{}, g.fail
}

func (g *onboardingGateStub) FinishOnboarding(context.Context, *store.Store) error {
	g.calls++
	return g.fail
}

func (g *onboardingGateStub) ReopenOnboarding(ctx context.Context, st *store.Store, id string) (store.OnboardingState, error) {
	g.calls++
	if g.fail != nil {
		return store.OnboardingState{}, g.fail
	}
	return st.ReopenOnboarding(ctx, id)
}

func TestOnboardingRequiresAuthenticationAndDoesNotFetchOnRead(t *testing.T) {
	ts := newTestServer(t)
	gate := &onboardingGateStub{}
	ts.applier = gate
	for _, path := range []string{"/api/onboarding", "/api/onboarding/reopen", "/api/onboarding/configure", "/api/onboarding/apply", "/api/onboarding/finish"} {
		method := http.MethodPost
		if path == "/api/onboarding" {
			method = http.MethodGet
		}
		if resp := ts.do(t, request{method: method, path: path, body: "{}"}); resp.code != 401 {
			t.Fatalf("unprotected %s: %d", path, resp.code)
		}
	}
	cookie := ts.login(t)
	resp := ts.do(t, request{method: http.MethodGet, path: "/api/onboarding", cookies: []*http.Cookie{cookie}})
	if resp.code != 200 || !strings.Contains(resp.body, `"pending":false`) || gate.calls != 0 {
		t.Fatalf("legacy GET caused mutation: %+v", resp)
	}
}

func TestOnboardingMutationErrorsAreSecretSafe(t *testing.T) {
	ts := newTestServer(t)
	gate := &onboardingGateStub{}
	ts.applier = gate
	cookie := ts.login(t)
	resp := ts.do(t, request{method: http.MethodPost, path: "/api/onboarding/configure", body: `{"mode":"free","country":"DE"}`, cookies: []*http.Cookie{cookie}})
	if resp.code != 200 || strings.Contains(resp.body, "expected") || strings.Contains(resp.body, "secret") {
		t.Fatalf("internal authority exposed: %s", resp.body)
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{store.ErrOnboardingConflict, 409}, {store.ErrInvalid, 400}, {errors.New("secret-vless://credential@edge.example"), 503}} {
		gate.fail = tc.err
		resp = ts.do(t, request{method: http.MethodPost, path: "/api/onboarding/apply", body: "{}", cookies: []*http.Cookie{cookie}})
		if resp.code != tc.status || strings.Contains(resp.body+ts.logs.String(), "secret-vless") {
			t.Fatalf("unsafe error: %d %s", resp.code, resp.body)
		}
	}
}

func TestOnboardingExplicitReopenChangesOnlyMetadata(t *testing.T) {
	ts := newTestServer(t)
	gate := &onboardingGateStub{}
	ts.applier = gate
	ctx := context.Background()
	peer, err := CreatePeer(ctx, ts.st, "Existing client", ts.clock())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := ts.st.Snapshot(ctx)
	hash, _ := store.PolicyFingerprint(before)
	cookie := ts.login(t)
	resp := ts.do(t, request{method: http.MethodPost, path: "/api/onboarding/reopen", body: `{}`, cookies: []*http.Cookie{cookie}})
	if resp.code != 200 || !strings.Contains(resp.body, peer.ID) || !strings.Contains(resp.body, `"pending":true`) {
		t.Fatalf("reopen: %d %s", resp.code, resp.body)
	}
	after, _ := ts.st.Snapshot(ctx)
	afterHash, _ := store.PolicyFingerprint(after)
	if hash != afterHash || strings.Contains(resp.body, "private_key") || strings.Contains(resp.body, peer.PrivateKey) {
		t.Fatal("reopen changed routes or exposed profile")
	}
}
