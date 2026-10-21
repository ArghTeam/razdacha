package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestDNSRejectionRemainsSeparateFromHTTPSAndTransportEvidence(t *testing.T) {
	ts := newTestServer(t)

	// Replace the default synthetic DNS response with a resolver rejecting policy.
	// The actual runtime regression is maintained separately in tests/dnsruntime.
	server := newRejectedDNSController(t)
	ts.clash = clash.New(clash.Options{Addr: server, HTTPClient: &http.Client{Timeout: time.Second}})
	out := ts.observeSignals(context.Background(), tunnelCheck{Status: tunnelSlow, At: ts.now})
	if out.DNS.State != "rejected" || out.Transport.State != "unknown" || out.HTTPS.State != tunnelSlow {
		t.Fatalf("signals: %+v", out)
	}
}

func TestGroupHistoryRetainsObservationTime(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tunnel := createTunnel(t, ts, cookie, "Synthetic")
	old := ts.now.Add(-time.Hour)
	stored, err := ts.store.Tunnel(context.Background(), tunnel.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, ok := ts.checkOne(context.Background(), stored, map[string]clash.Proxy{"tun-" + tunnel.ID: {All: []string{"member"}, History: []clash.History{{Time: old, Delay: 12}}}})
	if !ok || !res.At.Equal(old) {
		t.Fatalf("history timestamp: %+v", res)
	}
}

func newRejectedDNSController(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dns/query" {
			_, _ = w.Write([]byte(`{"Status":5}`))
			return
		}
		_, _ = w.Write([]byte(`{"delay":12000}`))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestPartialAndStaleTunnelSummariesCannotClaimAllHealthy(t *testing.T) {
	ts := newTestServer(t)
	now := ts.now
	cache := newCheckCache()
	cache.put("known", tunnelCheck{Status: tunnelUp, At: now})
	tunnels := []store.Tunnel{{ID: "known", Enabled: true}, {ID: "missing", Enabled: true}}
	if got := tunnelsCheck(tunnels, cache, now); got.Status == statusOK {
		t.Fatalf("partial summary: %+v", got)
	}
	cache.put("missing", tunnelCheck{Status: tunnelUp, At: now.Add(-time.Hour)})
	if got := tunnelsCheck(tunnels, cache, now); got.Status == statusOK {
		t.Fatalf("stale summary: %+v", got)
	}
}
