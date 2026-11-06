package clash

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	flowUserA = "client-00000000-0000-4000-8000-000000000001"
	flowUserB = "client-00000000-0000-4000-8000-000000000002"
)

func TestRealityRuleIdentityRequiresSingletonPrefix(t *testing.T) {
	valid := "inbound=reality-in auth_user=" + flowUserA + " rule_set=selected => route(tunnel-exit)"
	for _, tt := range []struct {
		name, rule string
		ok         bool
	}{
		{"selected", valid, true},
		{"direct", "inbound=reality-in auth_user=" + flowUserA + " => route(direct-out)", true},
		{"domain spoof", "domain=auth_user=" + flowUserA + " => route(direct-out)", false},
		{"other inbound", strings.Replace(valid, "reality-in", "other", 1), false},
		{"logical", "!(" + valid + ")", false},
		{"list", "inbound=reality-in auth_user=[" + flowUserA + " " + flowUserB + "] => route(direct-out)", false},
		{"invalid id", "inbound=reality-in auth_user=client-example => route(direct-out)", false},
		{"repeated identity", valid + " auth_user=" + flowUserB, false},
		{"reject", strings.Replace(valid, "route(tunnel-exit)", "reject", 1), false},
		{"missing action", "inbound=reality-in auth_user=" + flowUserA, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			user, ok := realityRuleUser(tt.rule)
			if ok != tt.ok || ok && user != flowUserA {
				t.Fatalf("identity=%q valid=%v", user, ok)
			}
		})
	}
}

func TestRealityConnectionsSeparateAuthenticatedUsers(t *testing.T) {
	type conn struct {
		ID       string            `json:"id"`
		Metadata map[string]string `json:"metadata"`
		Rule     string            `json:"rule"`
	}
	rows := []conn{}
	for i, user := range []string{flowUserA, flowUserB, flowUserA} {
		rows = append(rows, conn{ID: string(rune('a' + i)), Metadata: map[string]string{"type": "vless/reality-in", "sourceIP": "192.0.2.1", "host": "not-identity.example"}, Rule: "inbound=reality-in auth_user=" + user + " => route(direct-out)"})
	}
	rows = append(rows, conn{ID: "wg", Metadata: map[string]string{"type": "tproxy/tproxy-in", "sourceIP": "192.0.2.1"}, Rule: "final"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connections" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"connections": rows})
	}))
	defer srv.Close()
	counts, err := New(Options{Addr: srv.URL}).RealityConnections(context.Background())
	if err != nil || counts[flowUserA].OpenFlows != 2 || counts[flowUserB].OpenFlows != 1 || len(counts) != 2 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
}

func TestRealityConnectionsMalformedIsUnavailable(t *testing.T) {
	one := `{"id":"one","metadata":{"type":"vless/reality-in"},"rule":"final"}`
	for _, body := range []string{`{}`, `{"connections":null}`, `{"connections":[` + one + `]}`, `{"connections":[]} trailing`, strings.Repeat(" ", maxBody+1), `{"connections":` + strings.Repeat("[", 20)} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		counts, err := New(Options{Addr: srv.URL}).RealityConnections(context.Background())
		srv.Close()
		if err == nil || counts != nil {
			t.Fatal("malformed snapshot became a measured zero")
		}
	}
	rows := make([]map[string]any, maxConnectionFlows+1)
	for i := range rows {
		rows[i] = map[string]any{"metadata": map[string]string{"type": "direct/test"}}
	}
	body, _ := json.Marshal(map[string]any{"connections": rows})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	if _, err := New(Options{Addr: srv.URL}).RealityConnections(context.Background()); err == nil {
		t.Fatal("excessive flow count accepted")
	}
}

func TestRealityConnectionsDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := New(Options{Addr: srv.URL}).RealityConnections(ctx); err == nil {
		t.Fatal("canceled read accepted")
	}
}

func realityFlow(id, user, counters string) string {
	return `{"id":"` + id + `","metadata":{"type":"vless/reality-in"},"rule":"inbound=reality-in auth_user=` + user + ` => route(direct-out)"` + counters + `}`
}

func readRealityFixture(t *testing.T, rows ...string) (map[string]RealityConnectionObservation, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Global counters and unrelated inbound byte types are not REALITY authority.
		_, _ = fmt.Fprintf(w, `{"uploadTotal":999999999,"downloadTotal":999999999,"connections":[%s]}`, strings.Join(rows, ","))
	}))
	defer srv.Close()
	return New(Options{Addr: srv.URL}).RealityConnections(context.Background())
}

func TestRealityConnectionBytesRetainDirectionsAndUserIsolation(t *testing.T) {
	rows := []string{
		realityFlow("tcp-a", flowUserA, `,"upload":10,"download":100`),
		realityFlow("udp-a", flowUserA, `,"upload":7,"download":70`),
		realityFlow("tcp-b", flowUserB, `,"upload":3,"download":30`),
		`{"id":"wg","metadata":{"type":"tproxy/tproxy-in"},"upload":"not a REALITY counter","download":-1}`,
	}
	got, err := readRealityFixture(t, rows...)
	if err != nil {
		t.Fatal(err)
	}
	for user, want := range map[string][3]int64{flowUserA: {2, 17, 170}, flowUserB: {1, 3, 30}} {
		value := got[user]
		if int64(value.OpenFlows) != want[0] || value.OpenUploadBytes == nil || *value.OpenUploadBytes != want[1] || value.OpenDownloadBytes == nil || *value.OpenDownloadBytes != want[2] {
			t.Fatalf("wrong user/direction totals for %s: %+v", user, value)
		}
	}
	if len(got) != 2 {
		t.Fatal("unrelated inbound attributed to REALITY")
	}
	// A second snapshot replaces the previous observation; closed flows disappear.
	closed, err := readRealityFixture(t, rows[0])
	if err != nil || closed[flowUserA].OpenFlows != 1 || *closed[flowUserA].OpenUploadBytes != 10 || *closed[flowUserA].OpenDownloadBytes != 100 {
		t.Fatalf("closed flow remained in snapshot: %+v, %v", closed, err)
	}
}

func TestRealityConnectionMissingBytesPreserveCountsPerDirection(t *testing.T) {
	for _, missing := range []string{``, `,"upload":null`, `,"download":5`} {
		got, err := readRealityFixture(t,
			realityFlow("old", flowUserA, missing),
			realityFlow("new", flowUserA, `,"upload":4,"download":40`),
			realityFlow("other", flowUserB, `,"upload":2,"download":20`))
		if err != nil || got[flowUserA].OpenFlows != 2 || got[flowUserA].OpenUploadBytes != nil || *got[flowUserB].OpenUploadBytes != 2 {
			t.Fatalf("missing bytes corrupted counts or another user: %+v, %v", got, err)
		}
		if missing == `,"download":5` {
			if got[flowUserA].OpenDownloadBytes == nil || *got[flowUserA].OpenDownloadBytes != 45 {
				t.Fatal("known direction discarded")
			}
		} else if got[flowUserA].OpenDownloadBytes != nil {
			t.Fatal("missing direction became a partial sum")
		}
	}
}

func TestRealityConnectionInvalidBytesAndDuplicatesInvalidateSnapshot(t *testing.T) {
	for _, invalid := range []string{`-1`, `1.5`, `"2"`, `true`, `{}`, `9223372036854775808`} {
		for _, direction := range []string{"upload", "download"} {
			_, err := readRealityFixture(t, realityFlow("bad", flowUserA, `,"`+direction+`":`+invalid))
			if err == nil {
				t.Fatalf("invalid %s=%s accepted", direction, invalid)
			}
		}
	}
	for _, rows := range [][]string{
		{realityFlow("same", flowUserA, `,"upload":1`), realityFlow("same", flowUserB, `,"upload":2`)},
		{realityFlow("a", flowUserA, fmt.Sprintf(`,"upload":%d`, int64(math.MaxInt64))), realityFlow("b", flowUserA, `,"upload":1`)},
		{realityFlow("a", flowUserA, fmt.Sprintf(`,"download":%d`, int64(math.MaxInt64))), realityFlow("b", flowUserA, `,"download":1`)},
		// A missing earlier counter cannot hide a malformed later counter.
		{realityFlow("a", flowUserA, ``), realityFlow("b", flowUserA, `,"upload":-1`)},
	} {
		if _, err := readRealityFixture(t, rows...); err == nil {
			t.Fatal("duplicate or overflow snapshot accepted")
		}
	}
}
