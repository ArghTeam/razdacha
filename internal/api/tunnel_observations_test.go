package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestDNSObservationCancellationAndHTTPTimeoutRetainRuntimeCapacity(t *testing.T) {
	ts := newTestServer(t)
	var calls atomic.Int32
	entered := make(chan struct{}, 4)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer controller.Close()
	ts.clash = clash.New(clash.Options{Addr: controller.URL, HTTPClient: &http.Client{Timeout: 20 * time.Millisecond}})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan signal, 1)
	go func() { result <- ts.observeRuntimeDNS(ctx) }()
	<-entered
	cancel()
	if got := <-result; got.State != "unknown" {
		t.Fatal(got)
	}
	// The detached HTTP request times out, but sing-box's independent job would
	// still be running. Repeated waiters must reuse that retained identity.
	time.Sleep(40 * time.Millisecond)
	for i := 0; i < 5; i++ {
		got := ts.observeRuntimeDNS(context.Background())
		if got.State != "unknown" {
			t.Fatal(got)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("Canceled/timed-out DNS work lost its runtime lease: %d jobs", calls.Load())
	}
}

func TestFractionalGroupHistoryCannotConfirmRecoveryRepeatedly(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	tunnel := createTunnel(t, ts, cookie, "Synthetic pool")
	at := ts.now.Truncate(time.Second).Add(123 * time.Millisecond)
	ts.now = at.Add(time.Second)
	if err := ts.store.SaveTunnelCheck(context.Background(), tunnel.ID, tunnelDown, at.Add(-time.Minute), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := ts.store.SetNotifiedStatus(context.Background(), tunnel.ID, eventDown); err != nil {
		t.Fatal(err)
	}
	tag := "tun-" + tunnel.ID
	clashRoutes(t, ts, proxiesBody(t, map[string]any{tag: map[string]any{
		"name": tag, "type": "urltest", "all": []string{"member"},
		"history": []map[string]any{{"time": at.Format(time.RFC3339Nano), "delay": 12}},
	}}), 0, nil)
	for i := 0; i < 5; i++ {
		if err := ts.refreshChecks(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := ts.store.TunnelChecks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if saved[tunnel.ID].Notified != eventDown {
		t.Fatalf("One fractional history sample confirmed recovery: %+v", saved[tunnel.ID])
	}
}
