package clash

import (
	"context"
	"encoding/json"
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
	if err != nil || counts[flowUserA] != 2 || counts[flowUserB] != 1 || len(counts) != 2 {
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
