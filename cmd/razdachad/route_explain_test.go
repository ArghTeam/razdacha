package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestExplainCommittedPolicyPendingAndNoWrites(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	p, err := api.CreatePeer(ctx, st, "Synthetic client", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.CreateRule(ctx, store.Rule{Name: "Applied block", Action: store.ActionBlock, Enabled: true, PeerScope: store.ScopeAll, Domains: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	f := newPublicationFixture(t, nil)
	if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	req := singbox.ExplainRequest{PeerID: p.ID, Transport: "wg", Domain: "example.com"}
	before, _ := st.Snapshot(ctx)
	events := append([]string(nil), f.events...)
	config, _ := os.ReadFile(f.g.inner.ConfigPath)
	authority, _ := os.ReadFile(f.g.network.path)
	out, err := f.g.ExplainRoute(ctx, st, req)
	if err != nil || out.Routing.Action != "reject" || out.Pending || out.Marker == "" {
		t.Fatalf("applied %+v %v", out, err)
	}
	after, _ := st.Snapshot(ctx)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(events, f.events) {
		t.Fatal("diagnostics mutated state or runtime")
	}
	now, _ := os.ReadFile(f.g.inner.ConfigPath)
	gen, _ := os.ReadFile(f.g.network.path)
	if string(now) != string(config) || string(gen) != string(authority) {
		t.Fatal("diagnostics wrote authority/config")
	}
	r.Action = store.ActionDirect
	if err = st.UpdateRule(ctx, r); err != nil {
		t.Fatal(err)
	}
	out, err = f.g.ExplainRoute(ctx, st, req)
	if err != nil || out.Routing.Action != "reject" || !out.Pending {
		t.Fatalf("pending leaked %+v %v", out, err)
	}
	f.g.network.uncertain = true
	out, _ = f.g.ExplainRoute(ctx, st, req)
	if out.State != "unknown" || out.Routing.Action != "unknown" {
		t.Fatal(out)
	}
	f.g.network.uncertain = false
	f.g.network.initialized = false
	out, _ = f.g.ExplainRoute(ctx, st, req)
	if out.State != "unknown" {
		t.Fatal(out)
	}
	f.g.network.initialized = true
	p.Enabled = false
	if err = st.UpdatePeer(ctx, p); err != nil {
		t.Fatal(err)
	}
	out, _ = f.g.ExplainRoute(ctx, st, req)
	if out.State != "disabled" {
		t.Fatal(out)
	}
	if err = st.DeletePeer(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.g.ExplainRoute(ctx, st, req)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestExplainFrozenListsIgnoreEditableSources(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	p, err := api.CreatePeer(ctx, st, "Synthetic client", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	snap, err := st.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snap.Rules = []store.Rule{{ID: "frozen", Name: "Frozen rule", Action: store.ActionBlock, Enabled: true, RemoteLists: []string{"https://private.example/list"}, PeerScope: store.ScopeAll}}
	f := newPublicationFixture(t, nil)
	f.g.base = snap
	f.g.network.initialized = true
	f.g.network.uncertain = false
	f.g.network.contents = map[string]lists.List{"https://private.example/list": {Domains: []string{"example.com"}}}
	out, err := f.g.ExplainRoute(ctx, st, singbox.ExplainRequest{PeerID: p.ID, Transport: "wg", Domain: "example.com"})
	if err != nil || out.Routing.Action != "reject" || !out.Pending {
		t.Fatalf("frozen %+v %v", out, err)
	}
	data, _ := json.Marshal(out)
	if string(data) == "" {
		t.Fatal("empty response")
	}
}

func TestExplainRealityAdmissionRevocationAndGuards(t *testing.T) {
	ctx := context.Background()
	f := newRealityGateFixture(t)
	req := singbox.ExplainRequest{PeerID: f.p.ID, Transport: "reality", Domain: "example.com"}
	out, err := f.g.ExplainRoute(ctx, f.st, req)
	if err != nil || out.State != "unknown" {
		t.Fatalf("startup %+v %v", out, err)
	}
	if _, err = f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	before := f.reloads
	out, err = f.g.ExplainRoute(ctx, f.st, req)
	if err != nil || out.State != "conditional" || f.reloads != before {
		t.Fatalf("active %+v %v", out, err)
	}
	// Rotation is an explicitly separate mutation; its new credential stays pending.
	if _, err = f.g.RotateReality(ctx, f.st, f.p.ID, f.p.RealityRevision); err != nil {
		t.Fatal(err)
	}
	out, err = f.g.ExplainRoute(ctx, f.st, req)
	if err != nil || out.State != "disabled" || out.Reason != "transport_not_admitted" {
		t.Fatalf("rotation %+v %v", out, err)
	}
	f.g.network.uncertain = true
	out, _ = f.g.ExplainRoute(ctx, f.st, req)
	if out.State != "unknown" {
		t.Fatal(out)
	}
}

func TestExplainCompilesOutsideGateAndRejectsChangedAuthority(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	p, err := api.CreatePeer(ctx, st, "Synthetic client", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f := newPublicationFixture(t, nil)
	if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	f.g.explainCompile = func(s store.Snapshot, l singbox.PlainLists, r singbox.ExplainRequest) (singbox.PolicyExplanation, error) {
		close(started)
		<-release
		return singbox.ExplainPolicy(s, l, r)
	}
	result := make(chan singbox.PolicyExplanation, 1)
	go func() {
		out, _ := f.g.ExplainRoute(ctx, st, singbox.ExplainRequest{PeerID: p.ID, Transport: "wg", Domain: "example.com"})
		result <- out
	}()
	<-started
	if !f.g.mu.TryLock() {
		close(release)
		t.Fatal("compiler retained publication gate")
	}
	f.g.activityEpoch++
	f.g.mu.Unlock()
	close(release)
	out := <-result
	if out.State != "unknown" || out.Reason != "authority_changed" || out.Marker != "" {
		t.Fatal(out)
	}
}

func TestExplainBoundsCommittedPendingAndRepeatedSources(t *testing.T) {
	for _, kind := range []string{"committed", "pending", "repeated_sources"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			st, _ := openStore(t)
			p, err := api.CreatePeer(ctx, st, "Synthetic client", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			r, err := st.CreateRule(ctx, store.Rule{Name: "Bounded rule", Action: store.ActionBlock, Enabled: true, PeerScope: store.ScopeAll, Domains: []string{"example.com"}})
			if err != nil {
				t.Fatal(err)
			}
			f := newPublicationFixture(t, nil)
			if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
				t.Fatal(err)
			}
			compiled := false
			f.g.explainCompile = func(s store.Snapshot, l singbox.PlainLists, r singbox.ExplainRequest) (singbox.PolicyExplanation, error) {
				compiled = true
				return singbox.ExplainPolicy(s, l, r)
			}
			if kind == "committed" {
				f.g.base.Rules[0].Name = strings.Repeat("n", 1_000_001)
			}
			if kind == "pending" {
				r.Name = strings.Repeat("n", 1_000_001)
				if err = st.UpdateRule(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "repeated_sources" {
				domains := make([]string, 3000)
				for i := range domains {
					domains[i] = "a.test"
				}
				f.g.network.contents = map[string]lists.List{"https://source.invalid/list": {Domains: domains}}
				f.g.base.Rules = nil
				for i := 0; i < 40; i++ {
					copy := r
					copy.ID = fmt.Sprintf("rule-%d", i)
					copy.Domains = nil
					copy.RemoteLists = []string{"https://source.invalid/list"}
					f.g.base.Rules = append(f.g.base.Rules, copy)
				}
			}
			out, err := f.g.ExplainRoute(ctx, st, singbox.ExplainRequest{PeerID: p.ID, Transport: "wg", Domain: "example.com"})
			if err != nil || out.State != "unknown" || out.Marker != "" || !strings.Contains(out.Reason, "budget") {
				t.Fatalf("%s: %+v %v", kind, out, err)
			}
			if kind == "committed" && compiled {
				t.Fatal("oversized committed authority reached compiler")
			}
		})
	}
}
