package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

type evidencePublisher struct {
	authority func() (ClientEvidenceAuthority, error)
	plan      func(singbox.ExplainRequest) (ClientExitPlan, error)
	reality   func() (RealityActivityAuthority, error)
}

func (p evidencePublisher) Apply(context.Context, store.Snapshot) (singbox.ApplyResult, error) {
	return singbox.ApplyResult{}, nil
}

func (p evidencePublisher) ClientEvidenceAuthority(context.Context, *store.Store, string, string) (ClientEvidenceAuthority, error) {
	return p.authority()
}

func (p evidencePublisher) ClientExitPlan(_ context.Context, _ *store.Store, r singbox.ExplainRequest) (ClientExitPlan, error) {
	return p.plan(r)
}

func (p evidencePublisher) RealityActivityAuthority(context.Context, *store.Store) (RealityActivityAuthority, error) {
	return p.reality()
}

func invokeEvidence(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/"+path, strings.NewReader(body))
	if path == "observe" {
		s.handleClientObserve(w, r)
	} else {
		s.handleClientCheckExit(w, r)
	}
	return w
}

func TestClientEvidenceStrictAuthNoStore(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	var calls atomic.Int32
	ts.applier = evidencePublisher{authority: func() (ClientEvidenceAuthority, error) {
		calls.Add(1)
		return ClientEvidenceAuthority{State: "disabled", Reason: "transport_disabled"}, nil
	}, plan: func(r singbox.ExplainRequest) (ClientExitPlan, error) {
		calls.Add(1)
		return ClientExitPlan{Authority: ClientEvidenceAuthority{State: "unknown", Reason: "authority_unavailable"}, Policy: singbox.UnknownExplanation(r, "authority_unavailable")}, nil
	}}
	for _, path := range []string{"observe", "check-exit"} {
		base := `{"peer_id":"p","transport":"wg"}`
		if path == "check-exit" {
			base = `{"peer_id":"p","transport":"wg","domain":"example.com"}`
		}
		for _, bad := range []string{base + ` {}`, `null`, `{}`, `{"peer_id":"p","transport":"udp"}`, strings.TrimSuffix(base, "}") + `,"tag":"direct-out"}`, strings.TrimSuffix(base, "}") + `,"url":"https://example.com"}`, strings.Repeat(" ", 1100) + base} {
			res := ts.do(t, request{method: "POST", path: "/api/clients/" + path, body: bad, cookies: []*http.Cookie{cookie}})
			if res.code != 400 || res.header.Get("Cache-Control") != "no-store" {
				t.Fatalf("%s %s: %+v", path, bad, res)
			}
		}
		res := ts.do(t, request{method: "POST", path: "/api/clients/" + path, body: base})
		if res.code != 401 || res.header.Get("Cache-Control") != "no-store" {
			t.Fatal(res)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid or unauthorized requests reached publisher")
	}
	for _, domain := range []string{"https://example.com", "example.com:443", "192.0.2.1", "example.com/path", "*.example.com", "::1", "192.0.2.0/24"} {
		body, _ := json.Marshal(singbox.ExplainRequest{PeerID: "p", Transport: "wg", Domain: domain})
		if w := invokeEvidence(t, ts.Server, "exit", string(body)); w.Code != 400 {
			t.Fatal(w.Body.String())
		}
	}
	ts.applier = evidencePublisher{authority: func() (ClientEvidenceAuthority, error) { return ClientEvidenceAuthority{}, store.ErrNotFound }, plan: func(singbox.ExplainRequest) (ClientExitPlan, error) { return ClientExitPlan{}, store.ErrNotFound }}
	for _, path := range []string{"observe", "check-exit"} {
		body := `{"peer_id":"p","transport":"wg"}`
		if path == "check-exit" {
			body = `{"peer_id":"p","transport":"wg","domain":"example.com"}`
		}
		if w := invokeEvidence(t, ts.Server, path, body); w.Code != 404 {
			t.Fatal(w.Body.String())
		}
	}
}

func TestClientWGObservationNullZeroGrowthResetAndUnsafe(t *testing.T) {
	ts := newTestServer(t)
	now := ts.clock()
	authority := ClientEvidenceAuthority{Epoch: "a", SourceEpoch: "source-a", Peer: store.Peer{ID: "p", PublicKey: "selected"}}
	ts.applier = evidencePublisher{authority: func() (ClientEvidenceAuthority, error) { return authority, nil }}
	var reads int
	var stats map[string]netstack.WGPeerStat
	ts.peerStat = func(context.Context) (map[string]netstack.WGPeerStat, error) { reads++; return stats, nil }
	sample := func() clientObservation {
		w := invokeEvidence(t, ts.Server, "observe", `{"peer_id":"p","transport":"wg"}`)
		var out clientObservation
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	stats = map[string]netstack.WGPeerStat{"other": {RxBytes: 999, Endpoint: "secret-endpoint"}}
	if out := sample(); out.WG != nil || out.Reason != "peer_not_in_source" {
		t.Fatal(out)
	}
	for i, n := range []int64{0, 12, 45, 1, -1, maxEvidenceCounter + 1} {
		ts.advance(6 * time.Second)
		stats = map[string]netstack.WGPeerStat{"selected": {RxBytes: n, TxBytes: n, LastHandshake: now}, "other": {RxBytes: 999}}
		out := sample()
		if out.State != "observed" || out.WG == nil || out.Epoch == nil || !out.Fresh {
			t.Fatalf("sample %d: %+v", i, out)
		}
		if n < 0 || n > maxEvidenceCounter {
			if out.WG.RxBytes != nil || out.Reason != "unsafe_counter" {
				t.Fatal(out)
			}
		} else if out.WG.RxBytes == nil || *out.WG.RxBytes != n {
			t.Fatal(out)
		}
		at := *out.ObservedAt
		count := reads
		duplicate := sample()
		if reads != count || !duplicate.ObservedAt.Equal(at) {
			t.Fatal("duplicate poll resampled or relabeled source time")
		}
	}
	ts.advance(6 * time.Second)
	stats = map[string]netstack.WGPeerStat{"selected": {LastHandshake: ts.clock().Add(time.Hour)}}
	if out := sample(); out.WG.LastHandshake != nil || out.Reason != "future_handshake" {
		t.Fatal(out)
	}
	// Same-byte publication must not reuse the old cached device sample.
	count := reads
	authority.Epoch = "b"
	authority.SourceEpoch = "source-b"
	stats = map[string]netstack.WGPeerStat{"selected": {RxBytes: 77}}
	if out := sample(); reads != count+1 || *out.WG.RxBytes != 77 || *out.Epoch != "b" {
		t.Fatal(out)
	}
	ts.advance(6 * time.Second)
	ts.peerStat = func(context.Context) (map[string]netstack.WGPeerStat, error) {
		return nil, errors.New("private kernel error")
	}
	w := invokeEvidence(t, ts.Server, "observe", `{"peer_id":"p","transport":"wg"}`)
	if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "endpoint") {
		t.Fatal(w.Body.String())
	}
}

func TestWGEvidenceCacheCancellationAndEpoch(t *testing.T) {
	var cache wgEvidenceCache
	now := time.Now
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(context.Context) (map[string]netstack.WGPeerStat, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return map[string]netstack.WGPeerStat{"p": {RxBytes: int64(calls.Load())}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan wgEvidenceSample)
	go func() { first <- cache.observe(ctx, "a", now, load) }()
	<-started
	cancel()
	for i := 0; i < 20; i++ {
		c, stop := context.WithCancel(context.Background())
		stop()
		if out := cache.observe(c, "b", now, load); out.stats != nil {
			t.Fatal(out)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("cancellation amplified source reads")
	}
	next := make(chan wgEvidenceSample)
	go func() { next <- cache.observe(context.Background(), "b", now, load) }()
	close(release)
	<-first
	out := <-next
	if calls.Load() != 2 || out.epoch != "b" || out.stats["p"].RxBytes != 2 {
		t.Fatal(out, calls.Load())
	}
	// Distinct clients sharing the same source lifetime coalesce one device read.
	cache.observe(context.Background(), "b", now, load)
	if calls.Load() != 2 {
		t.Fatal("cache missed")
	}
}

func TestClientObservationRejectsPublicationChangeAfterIO(t *testing.T) {
	ts := newTestServer(t)
	var epoch atomic.Int32
	epoch.Store(1)
	ts.applier = evidencePublisher{authority: func() (ClientEvidenceAuthority, error) {
		e := "a"
		if epoch.Load() != 1 {
			e = "b"
		}
		return ClientEvidenceAuthority{Epoch: e, SourceEpoch: e, Peer: store.Peer{PublicKey: "p"}}, nil
	}}
	ts.peerStat = func(context.Context) (map[string]netstack.WGPeerStat, error) {
		epoch.Store(2)
		return map[string]netstack.WGPeerStat{"p": {RxBytes: 123}}, nil
	}
	w := invokeEvidence(t, ts.Server, "observe", `{"peer_id":"p","transport":"wg"}`)
	var out clientObservation
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Epoch != nil || out.WG != nil || out.State != "unknown" || out.Reason != "authority_changed" {
		t.Fatal(w.Body.String())
	}
}

func TestClientRealityObservationScopeClosureAndMissingBytes(t *testing.T) {
	ts := newTestServer(t)
	const id = "00000000-0000-4000-8000-000000000001"
	user := store.RealityUserName(id)
	var body string
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connections" {
			t.Error(r.URL.Path)
		}
		reads.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	ts.clash = clash.New(clash.Options{Addr: srv.URL})
	ts.applier = evidencePublisher{authority: func() (ClientEvidenceAuthority, error) {
		return ClientEvidenceAuthority{Epoch: "a", SourceEpoch: "s", Peer: store.Peer{ID: id, Enabled: true, RealityEnabled: true, RealityRevision: 1}}, nil
	}, reality: func() (RealityActivityAuthority, error) {
		return RealityActivityAuthority{Epoch: "r", Users: map[string]int{user: 1}}, nil
	}}
	row := func(name, bytes string) string {
		return `{"id":"` + name + `","metadata":{"type":"vless/reality-in"},` + bytes + `"rule":"inbound=reality-in auth_user=` + user + ` => route(direct-out)"}`
	}
	body = `{"connections":[` + row("one", `"upload":100,"download":200,`) + `,` + row("two", `"upload":300,"download":400,`) + `]}`
	observe := func() clientObservation {
		w := invokeEvidence(t, ts.Server, "observe", `{"peer_id":"`+id+`","transport":"reality"}`)
		var out clientObservation
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	out := observe()
	if out.Reality == nil || *out.Reality.OpenFlows != 2 || *out.Reality.OpenUploadBytes != 400 {
		t.Fatal(out)
	}
	at := *out.ObservedAt
	observe()
	if reads.Load() != 1 {
		t.Fatal("existing controller cache was bypassed")
	}
	ts.advance(6 * time.Second)
	body = `{"connections":[` + row("one", `"upload":101,`) + `]}`
	out = observe()
	if out.Reality == nil || *out.Reality.OpenFlows != 1 || out.Reality.OpenDownloadBytes != nil || !out.ObservedAt.After(at) || out.State != "observed" {
		t.Fatal(out)
	}
	ts.advance(6 * time.Second)
	body = `{"connections":[]}`
	out = observe()
	if out.Reality == nil || *out.Reality.OpenFlows != 0 || *out.Reality.OpenUploadBytes != 0 || out.State != "observed" {
		t.Fatal(out)
	}
	ts.advance(6 * time.Second)
	body = `{"connections":[` + row("one", `"upload":9007199254740992,"download":7,`) + `]}`
	out = observe()
	if out.Reality == nil || out.Reality.OpenUploadBytes != nil || *out.Reality.OpenDownloadBytes != 7 {
		t.Fatal(out)
	}
}

func TestClientExitNoUnexpectedProbesAndNoGlobalWrites(t *testing.T) {
	ts := newTestServer(t)
	ts.Server.now = time.Now
	var requests atomic.Int32
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/proxies/") || !strings.HasSuffix(r.URL.Path, "/delay") {
			t.Error(r.URL.Path)
		}
		if strings.Contains(r.URL.RawQuery, "example.com") {
			t.Error("supplied domain became probe target")
		}
		_, _ = w.Write([]byte(`{"delay":17}`))
	}))
	defer controller.Close()
	ts.clash = clash.New(clash.Options{Addr: controller.URL})
	a := ClientEvidenceAuthority{Epoch: "a", SourceEpoch: "s"}
	action, tag := "unknown", ""
	ts.applier = evidencePublisher{authority: func() (ClientEvidenceAuthority, error) { return a, nil }, plan: func(r singbox.ExplainRequest) (ClientExitPlan, error) {
		p := singbox.UnknownExplanation(r, "fixture")
		p.Routing.Action = action
		return ClientExitPlan{Authority: a, Policy: p, Tag: tag}, nil
	}}
	check := func() clientExitResponse {
		w := invokeEvidence(t, ts.Server, "exit", `{"peer_id":"p","transport":"wg","domain":"example.com"}`)
		var out clientExitResponse
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	for _, v := range []string{"unknown", "reject"} {
		action = v
		out := check()
		if out.HTTPS.State != "not_run" || requests.Load() != 0 {
			t.Fatal(out)
		}
	}
	action = "direct"
	tag = singbox.TagDirect
	out := check()
	if requests.Load() != 1 || out.HTTPS.State != "observed" || out.HTTPS.DelayMS == nil || *out.HTTPS.DelayMS != 17 || !out.HTTPS.Fresh {
		t.Fatal(out)
	}
	if len(ts.checks.last) != 0 {
		t.Fatal("new endpoint wrote legacy global check cache")
	}
	a.Epoch = ""
	tag = "must-not-probe"
	check()
	if requests.Load() != 1 {
		t.Fatal("missing admission probed")
	}
}

func TestClientExitPoolHistoryNoFanoutAndPostProbeGuard(t *testing.T) {
	ts := newTestServer(t)
	ts.Server.now = time.Now
	now := time.Now().UTC()
	a := ClientEvidenceAuthority{Epoch: "a", SourceEpoch: "s"}
	pool := true
	ts.poolProxies = &fakeProxies{proxies: map[string]clash.Proxy{"pool": {History: []clash.History{{Time: now.Add(-time.Minute), Delay: 53}}}}}
	ts.applier = evidencePublisher{authority: func() (ClientEvidenceAuthority, error) { return a, nil }, plan: func(r singbox.ExplainRequest) (ClientExitPlan, error) {
		return ClientExitPlan{Authority: a, Policy: singbox.UnknownExplanation(r, "fixture"), Tag: "pool", Pool: pool}, nil
	}}
	check := func() clientExitResponse {
		w := invokeEvidence(t, ts.Server, "exit", `{"peer_id":"p","transport":"wg","domain":"example.com"}`)
		var out clientExitResponse
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	out := check()
	if out.HTTPS.State != "observed" || out.HTTPS.Source == nil || *out.HTTPS.Source != "pool_history" || !out.HTTPS.ObservedAt.Equal(now.Add(-time.Minute)) {
		t.Fatal(out)
	}
	a.Epoch = "publication-b"
	repeated := check()
	if repeated.HTTPS.Reason != "pool_history_only" || !repeated.HTTPS.ObservedAt.Equal(*out.HTTPS.ObservedAt) || *repeated.Epoch != "publication-b" {
		t.Fatal("history relabeled as a new generation observation", repeated)
	}
	ts.poolProxies = &fakeProxies{proxies: map[string]clash.Proxy{"pool": {History: []clash.History{{Time: now.Add(time.Hour), Delay: 53}}}}}
	out = check()
	if out.HTTPS.Fresh || out.HTTPS.DelayMS != nil || out.HTTPS.State != "unknown" {
		t.Fatal(out)
	}
	pool = false
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.Epoch = "b"; _, _ = w.Write([]byte(`{"delay":17}`)) }))
	defer controller.Close()
	ts.clash = clash.New(clash.Options{Addr: controller.URL})
	out = check()
	if out.Epoch != nil || out.HTTPS.DelayMS != nil || out.HTTPS.Reason != "authority_changed" || out.Policy.State != "unknown" {
		t.Fatal(out)
	}
}

func TestClientExitAdmissionSlotRetainedDuringCancellation(t *testing.T) {
	ts := newTestServer(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ts.applier = evidencePublisher{plan: func(r singbox.ExplainRequest) (ClientExitPlan, error) {
		once.Do(func() { close(started) })
		<-release
		return ClientExitPlan{Policy: singbox.UnknownExplanation(r, "fixture")}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"peer_id":"p","transport":"wg","domain":"example.com"}`)).WithContext(ctx)
		ts.handleClientCheckExit(httptest.NewRecorder(), r)
	}()
	<-started
	cancel()
	w := invokeEvidence(t, ts.Server, "exit", `{"peer_id":"p","transport":"wg","domain":"example.com"}`)
	if w.Code != 429 {
		t.Fatal(w.Code)
	}
	close(release)
	<-done
}

func TestClientExitDoesNotRelabelOlderSharedProbe(t *testing.T) {
	ts := newTestServer(t)
	ts.Server.now = time.Now
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-release
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer controller.Close()
	ts.clash = clash.New(clash.Options{Addr: controller.URL})
	// The old job could come from a manual/scheduled check under publication A.
	oldDone := make(chan struct{})
	startOld := func() {
		go func() { defer close(oldDone); _, _ = ts.clash.Delay(context.Background(), singbox.TagDirect) }()
		<-entered
	}
	a := ClientEvidenceAuthority{Epoch: "new-publication-b", SourceEpoch: "b"}
	planned := make(chan struct{})
	ts.applier = evidencePublisher{authority: func() (ClientEvidenceAuthority, error) { return a, nil }, plan: func(r singbox.ExplainRequest) (ClientExitPlan, error) {
		// Admit A while plan compilation is in progress, then return B authority.
		// A fence captured before the plan would incorrectly accept this source.
		startOld()
		close(planned)
		return ClientExitPlan{Authority: a, Policy: singbox.UnknownExplanation(r, "fixture"), Tag: singbox.TagDirect}, nil
	}}
	done := make(chan clientExitResponse)
	go func() {
		w := invokeEvidence(t, ts.Server, "exit", `{"peer_id":"p","transport":"wg","domain":"example.com"}`)
		var out clientExitResponse
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		done <- out
	}()
	<-planned
	// Keep the old broker identity alive after completion so either scheduling
	// order still joins it rather than racing a newly created source identity.
	// A failure retains the normal broker lease; success is exercised elsewhere.
	close(release)
	out := <-done
	<-oldDone
	if calls.Load() != 1 || out.HTTPS.State != "unknown" || out.HTTPS.Reason != "source_lifetime_unknown" || out.HTTPS.DelayMS != nil || out.HTTPS.ObservedAt != nil {
		t.Fatal(out, calls.Load())
	}
}
