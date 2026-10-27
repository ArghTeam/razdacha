package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

type setupHTTP func(*http.Request) (*http.Response, error)

func (f setupHTTP) Do(r *http.Request) (*http.Response, error) { return f(r) }

func setupGateFixture(t *testing.T) (*publicationFixture, *store.Store) {
	t.Helper()
	f := newPublicationFixture(t, nil)
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(filepath.Dir(f.g.network.path), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	peer, err := api.CreatePeer(ctx, st, "Initial client", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureBuiltinPool(ctx, lists.BuiltinPoolCatalogURL, store.TunnelVLESS); err != nil {
		t.Fatal(err)
	}
	if err := st.InitOnboarding(ctx, peer.ID, "", "AUTO"); err != nil {
		t.Fatal(err)
	}
	snap, err := st.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.Apply(ctx, snap); err != nil {
		t.Fatal(err)
	}
	return f, st
}

func TestOnboardingGateRejectsUnrelatedPendingAndBlockedCountry(t *testing.T) {
	f, st := setupGateFixture(t)
	ctx := context.Background()
	calls := 0
	f.g.onboardingCatalog = func(context.Context, string) ([]store.PoolServer, error) { calls++; return nil, nil }
	if _, err := f.g.ConfigureOnboarding(ctx, st, api.OnboardingRequest{Mode: "free", Country: "RU"}); !errors.Is(err, store.ErrInvalid) || calls != 0 {
		t.Fatal("blocked country fetched")
	}
	settings, _ := st.Settings(ctx)
	settings.ClientMTU = 1320
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	before := f.reloadCount()
	if _, err := f.g.ConfigureOnboarding(ctx, st, api.OnboardingRequest{Mode: "free", Country: "DE"}); !errors.Is(err, store.ErrOnboardingConflict) || calls != 0 || before != f.reloadCount() {
		t.Fatal("unrelated pending edits published or catalog fetched")
	}
}

func TestOnboardingGateFailedApplyCannotFinish(t *testing.T) {
	f, st := setupGateFixture(t)
	ctx := context.Background()
	f.g.onboardingCatalog = func(context.Context, string) ([]store.PoolServer, error) {
		return nil, errors.New("synthetic catalog unavailable")
	}
	state, err := f.g.ConfigureOnboarding(ctx, st, api.OnboardingRequest{Mode: "free", Country: "DE"})
	if err != nil {
		t.Fatal(err)
	}
	before := f.reloadCount()
	if _, err := f.g.ApplyOnboarding(ctx, st); err == nil || before != f.reloadCount() {
		t.Fatal("missing sources bypassed guarded preparation")
	}
	if err := f.g.FinishOnboarding(ctx, st); !errors.Is(err, store.ErrOnboardingConflict) {
		t.Fatal("failed setup completed")
	}
	fresh, _ := st.Onboarding(ctx)
	if !fresh.Pending || fresh.Applied || fresh.Expected != state.Expected {
		t.Fatal("failed Apply lost retryable intent")
	}
}

func TestOnboardingGateSuccessApplyFinishAndConcurrentRevision(t *testing.T) {
	f, st := setupGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ConfigureOnboarding(ctx, st, api.OnboardingRequest{Mode: "skip", Country: "AUTO"}); err != nil {
		t.Fatal(err)
	}
	settings, _ := st.Settings(ctx)
	settings.ClientMTU = 1320
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.ApplyOnboarding(ctx, st); !errors.Is(err, store.ErrOnboardingConflict) {
		t.Fatal("concurrent revision accepted")
	}
	settings.ClientMTU = 1280
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.ApplyOnboarding(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := f.g.FinishOnboarding(ctx, st); err != nil {
		t.Fatal(err)
	}
	state, _ := st.Onboarding(ctx)
	if state.Pending {
		t.Fatal("successful completion not persisted")
	}
}

func TestOnboardingGateFreeApplyAcquiresCanonicalSources(t *testing.T) {
	f, st := setupGateFixture(t)
	ctx := context.Background()
	var body bytes.Buffer
	slow := true
	if err := srs.Write(&body, option.PlainRuleSet{Rules: []option.HeadlessRule{{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultHeadlessRule{DomainSuffix: badoption.Listable[string]{"video.example"}}}}}, C.RuleSetVersion2); err != nil {
		t.Fatal(err)
	}
	client := setupHTTP(func(req *http.Request) (*http.Response, error) {
		if slow {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		raw := body.Bytes()
		if strings.HasSuffix(req.URL.Path, ".lst") {
			raw = []byte("203.0.113.0/24\n")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(raw)), Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})
	fetcher, err := lists.NewFetcher(lists.Options{Dir: filepath.Join(filepath.Dir(f.g.network.path), "lists"), Client: client, Logger: quietSlog()})
	if err != nil {
		t.Fatal(err)
	}
	f.g.network.manager = lists.NewManager(lists.ManagerOptions{Fetcher: fetcher, Logger: quietSlog()})
	f.g.onboardingCatalog = func(context.Context, string) ([]store.PoolServer, error) {
		return []store.PoolServer{{URL: "vless://00000000-0000-4000-8000-000000000001@edge.example:443?security=tls", Country: "DE"}}, nil
	}
	if _, err := f.g.ConfigureOnboarding(ctx, st, api.OnboardingRequest{Mode: "free", Country: "DE"}); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
	before := f.reloadCount()
	_, err = f.g.ApplyOnboarding(short, st)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || before != f.reloadCount() {
		t.Fatal("slow acquisition published after cancellation")
	}
	slow = false
	if _, err := f.g.ApplyOnboarding(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := f.g.FinishOnboarding(ctx, st); err != nil {
		t.Fatal(err)
	}
	if len(f.contents) == 0 || len(f.published.Rules) != 1 || f.published.Rules[0].PeerScope != store.ScopeSelected || f.isGuarded() {
		t.Fatal("coherent selected-client publication missing")
	}
}
