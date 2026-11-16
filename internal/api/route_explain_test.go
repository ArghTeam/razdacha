package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

type explainFunc func(context.Context, *store.Store, singbox.ExplainRequest) (singbox.PolicyExplanation, error)

func (f explainFunc) ExplainRoute(c context.Context, s *store.Store, r singbox.ExplainRequest) (singbox.PolicyExplanation, error) {
	return f(c, s, r)
}

func TestRouteExplainAuthValidationAndNoStore(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	calls := 0
	ts.routeExplainer = explainFunc(func(_ context.Context, _ *store.Store, r singbox.ExplainRequest) (singbox.PolicyExplanation, error) {
		calls++
		return singbox.UnknownExplanation(r, "synthetic"), nil
	})
	requestFor := func(body string, authenticated bool) response {
		req := request{method: "POST", path: "/api/route/explain", body: body}
		if authenticated {
			req.cookies = []*http.Cookie{cookie}
		}
		res := ts.do(t, req)
		if res.header.Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
		return res
	}
	if res := requestFor(`{"peer_id":"p","transport":"wg","domain":"example.com"}`, false); res.code != 401 {
		t.Fatal(res)
	}
	for _, domain := range []string{"https://example.com", "example.com/path", "example.com:443", "192.0.2.1", "::1", "192.0.2.0/24", "*.example.com", ""} {
		body, _ := json.Marshal(singbox.ExplainRequest{PeerID: "p", Transport: "wg", Domain: domain})
		if res := requestFor(string(body), true); res.code != 400 {
			t.Fatalf("%q: %+v", domain, res)
		}
	}
	for _, body := range []string{`{"peer_id":"","transport":"wg","domain":"example.com"}`, `{"peer_id":"p","transport":"udp","domain":"example.com"}`, `{"peer_id":"p","transport":"wg","domain":"example.com","port":443}`, `{"peer_id":"p","transport":"wg","domain":"example.com"} {}`, `{"peer_id":"p","transport":"wg","domain":"example.com","network":"udp"}`} {
		if res := requestFor(body, true); res.code != 400 {
			t.Fatal(res)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid request compiled %d times", calls)
	}
	res := requestFor(`{"peer_id":"p","transport":"reality","domain":"WWW.Example.com."}`, true)
	if res.code != 200 || calls != 1 {
		t.Fatal(res)
	}
	var out singbox.PolicyExplanation
	decodeJSONBody(t, res, &out)
	if out.Domain != "www.example.com" || out.Scope != "Web policy / TCP 443" {
		t.Fatal(out)
	}
	ts.routeExplainer = explainFunc(func(context.Context, *store.Store, singbox.ExplainRequest) (singbox.PolicyExplanation, error) {
		return singbox.PolicyExplanation{}, store.ErrNotFound
	})
	if res := requestFor(`{"peer_id":"p","transport":"wg","domain":"example.com"}`, true); res.code != 404 {
		t.Fatal(res)
	}
}

func TestRouteExplainCancellationRetainsActiveSlot(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ts.routeExplainer = explainFunc(func(context.Context, *store.Store, singbox.ExplainRequest) (singbox.PolicyExplanation, error) {
		once.Do(func() { close(started) })
		<-release
		return singbox.PolicyExplanation{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := httptest.NewRequest("POST", "/api/route/explain", strings.NewReader(`{"peer_id":"p","transport":"wg","domain":"example.com"}`)).WithContext(ctx)
		r.AddCookie(cookie)
		ts.Handler().ServeHTTP(httptest.NewRecorder(), r)
	}()
	<-started
	cancel()
	res := ts.do(t, request{method: "POST", path: "/api/route/explain", body: `{"peer_id":"p","transport":"wg","domain":"another.example"}`, cookies: []*http.Cookie{cookie}})
	if res.code != 429 {
		t.Fatal(res)
	}
	close(release)
	<-done
}
