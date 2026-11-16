package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/ingressguard"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestClientEvidenceAuthorityLifetimeAndCheckedBytes(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	authority := func(transport string) api.ClientEvidenceAuthority {
		t.Helper()
		out, err := f.g.ClientEvidenceAuthority(ctx, f.st, f.p.ID, transport)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := authority("wg"); out.Epoch != "" {
		t.Fatal("startup admitted observation")
	}
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	before := authority("wg")
	reality := authority("reality")
	if before.Epoch == "" || reality.Epoch == "" || before.Epoch == reality.Epoch || before.SourceEpoch != reality.SourceEpoch {
		t.Fatal(before, reality)
	}
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	after := authority("wg")
	if after.Epoch == before.Epoch || after.SourceEpoch == before.SourceEpoch {
		t.Fatal("same-byte Apply retained lifetime")
	}
	// Process restart cannot revive an old comparison epoch at identical publication.
	other := newApplyGate(f.g.inner, f.g.network, quietSlog())
	other.base = f.g.base
	other.activityEpoch = f.g.activityEpoch
	reboot, err := other.ClientEvidenceAuthority(ctx, f.st, f.p.ID, "wg")
	if err != nil || reboot.Epoch == after.Epoch || reboot.SourceEpoch == after.SourceEpoch || reboot.Epoch == "" {
		t.Fatal(reboot, err)
	}
	original, err := os.ReadFile(f.g.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.g.configPath(), append(original, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := authority("wg"); out.Epoch != "" {
		t.Fatal("unchecked bytes admitted")
	}
	if err = os.WriteFile(f.g.configPath(), original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = f.g.emergencyPublicGuard(); err != nil {
		t.Fatal(err)
	}
	if out := authority("wg"); out.Epoch != "" {
		t.Fatal("guard retained authority")
	}
}

func TestClientEvidenceWGOnlyAdmissionABA(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	peer, err := api.CreatePeer(ctx, st, "WG only", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f := newPublicationFixture(t, nil)
	dir, err := filepath.EvalSymlinks(filepath.Dir(f.g.network.path))
	if err != nil {
		t.Fatal(err)
	}
	f.g.network.path = filepath.Join(dir, filepath.Base(f.g.network.path))
	f.g.inner.ConfigPath = filepath.Join(dir, "config.json")
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f.g.store = st
	f.g.network.publicGuard = func([]ingressguard.Endpoint, bool) error { return nil }
	if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"enabled", "wireguard_disabled"} {
		if field == "wireguard_disabled" {
			yes := true
			if _, err = f.g.PatchPeer(ctx, st, peer.ID, store.PeerPatch{RealityEnabled: &yes}); err != nil {
				t.Fatal(err)
			}
			if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
				t.Fatal(err)
			}
		}
		before, err := f.g.ClientEvidenceAuthority(ctx, st, peer.ID, "wg")
		if err != nil || before.Epoch == "" {
			t.Fatal(before, err)
		}
		originalEvents := append([]string(nil), f.events...)
		disabled, enabled := false, true
		patch := store.PeerPatch{Enabled: &disabled}
		if field == "wireguard_disabled" {
			patch = store.PeerPatch{WireGuardEnabled: &disabled}
		}
		if _, err = f.g.PatchPeer(ctx, st, peer.ID, patch); err != nil {
			t.Fatal(err)
		}
		during, err := f.g.ClientEvidenceAuthority(ctx, st, peer.ID, "wg")
		if err != nil || during.Epoch != "" || during.State != "disabled" {
			t.Fatal(during, err)
		}
		patch = store.PeerPatch{Enabled: &enabled}
		if field == "wireguard_disabled" {
			patch = store.PeerPatch{WireGuardEnabled: &enabled}
		}
		if _, err = f.g.PatchPeer(ctx, st, peer.ID, patch); err != nil {
			t.Fatal(err)
		}
		after, err := f.g.ClientEvidenceAuthority(ctx, st, peer.ID, "wg")
		if err != nil || after.Epoch == "" || after.Epoch == before.Epoch || after.SourceEpoch == before.SourceEpoch {
			t.Fatal("ABA retained comparison/source epoch", after, err)
		}
		if !reflect.DeepEqual(f.events, originalEvents) {
			t.Fatal("diagnostics lifetime change mutated publication")
		}
	}
}

func TestClientExitPlanAppliedNotEditableAndFullChain(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	p, err := api.CreatePeer(ctx, st, "Client", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.CreateTunnel(ctx, store.Tunnel{Name: "First", Type: store.TunnelSOCKS, Source: store.SourceURL, Raw: "synthetic", Parsed: []byte(`{"server":"192.0.2.1","server_port":1080,"version":"5"}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// A WARP second leg is eligible for the existing compiled chain contract.
	second, err := st.CreateTunnel(ctx, store.Tunnel{Name: "WARP", Type: store.TunnelWireGuard, Source: store.SourceWARP, Raw: "synthetic", Parsed: []byte(`{"server":"192.0.2.2","server_port":2408,"local_address":["172.16.0.2/32"],"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","peer_public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","reserved":[1,2,3]}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.CreateRule(ctx, store.Rule{Name: "Chain", Action: store.ActionTunnel, Enabled: true, PeerScope: store.ScopeAll, Domains: []string{"example.com"}, TunnelID: first.ID, ViaTunnelID: second.ID})
	if err != nil {
		t.Fatal(err)
	}
	f := newPublicationFixture(t, nil)
	dir, err := filepath.EvalSymlinks(filepath.Dir(f.g.network.path))
	if err != nil {
		t.Fatal(err)
	}
	f.g.network.path = filepath.Join(dir, filepath.Base(f.g.network.path))
	f.g.inner.ConfigPath = filepath.Join(dir, "config.json")
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f.g.store = st
	f.g.network.publicGuard = func([]ingressguard.Endpoint, bool) error { return nil }
	if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	req := singbox.ExplainRequest{PeerID: p.ID, Transport: "wg", Domain: "example.com"}
	plan, err := f.g.ClientExitPlan(ctx, st, req)
	if err != nil || plan.Tag != singbox.ChainTag(second.ID, first.ID) || plan.Pool {
		t.Fatalf("chain %+v %v", plan, err)
	}
	r.Action = store.ActionDirect
	r.TunnelID = ""
	r.ViaTunnelID = ""
	if err = st.UpdateRule(ctx, r); err != nil {
		t.Fatal(err)
	}
	plan, err = f.g.ClientExitPlan(ctx, st, req)
	if err != nil || plan.Tag != singbox.ChainTag(second.ID, first.ID) || !plan.Policy.Pending {
		t.Fatal("editable policy leaked", plan, err)
	}
	if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	plan, err = f.g.ClientExitPlan(ctx, st, req)
	if err != nil || plan.Tag != singbox.TagDirect {
		t.Fatal(plan, err)
	}
	r.Action = store.ActionBlock
	if err = st.UpdateRule(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	plan, err = f.g.ClientExitPlan(ctx, st, req)
	if err != nil || plan.Tag != "" || plan.Policy.Routing.Action != "reject" {
		t.Fatal(plan, err)
	}
}

func TestClientExitPlanUnknownDNSAndChangedAuthority(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	req := singbox.ExplainRequest{PeerID: f.p.ID, Transport: "reality", Domain: "example.com"}
	f.g.explainCompile = func(_ store.Snapshot, _ singbox.PlainLists, r singbox.ExplainRequest) (singbox.PolicyExplanation, error) {
		p := singbox.UnknownExplanation(r, "native_dns_predicate")
		p.State = "conditional"
		p.Routing = singbox.PolicyOutcome{State: "conditional", Action: "direct"}
		return p, nil
	}
	plan, err := f.g.ClientExitPlan(ctx, f.st, req)
	if err != nil || plan.Tag != "" {
		t.Fatal("unknown gateway DNS selected probe", plan, err)
	}
	// Expensive compilation must run without the publication mutex, and a changed
	// publication lifetime during compilation must invalidate the final plan.
	f.g.explainCompile = func(s store.Snapshot, l singbox.PlainLists, r singbox.ExplainRequest) (singbox.PolicyExplanation, error) {
		f.g.mu.Lock()
		f.g.activityEpoch++
		f.g.mu.Unlock()
		return singbox.ExplainPolicy(s, l, r)
	}
	plan, err = f.g.ClientExitPlan(ctx, f.st, req)
	if err != nil || plan.Tag != "" || plan.Authority.Epoch != "" {
		t.Fatal("changed authority selected probe", plan, err)
	}
}

func TestClientExitPlanPoolAndMissingCheckedTag(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	p, err := api.CreatePeer(ctx, st, "Client", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pool, err := st.CreateTunnel(ctx, store.Tunnel{Name: "Pool", Type: store.TunnelVLESS, Source: store.SourcePool, Enabled: true, Raw: "https://catalog.invalid", Pool: []store.PoolServer{{URL: "vless://00000000-0000-0000-0000-000000000000@192.0.2.9:443?security=tls&sni=fixture.invalid"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateRule(ctx, store.Rule{Name: "Pool", Action: store.ActionTunnel, Enabled: true, PeerScope: store.ScopeAll, Domains: []string{"example.com"}, TunnelID: pool.ID})
	if err != nil {
		t.Fatal(err)
	}
	f := newPublicationFixture(t, nil)
	dir, err := filepath.EvalSymlinks(filepath.Dir(f.g.network.path))
	if err != nil {
		t.Fatal(err)
	}
	f.g.network.path = filepath.Join(dir, filepath.Base(f.g.network.path))
	f.g.inner.ConfigPath = filepath.Join(dir, "config.json")
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f.g.store = st
	f.g.network.publicGuard = func([]ingressguard.Endpoint, bool) error { return nil }
	if _, err = f.g.ApplyCurrent(ctx, st); err != nil {
		t.Fatal(err)
	}
	req := singbox.ExplainRequest{PeerID: p.ID, Transport: "wg", Domain: "example.com"}
	plan, err := f.g.ClientExitPlan(ctx, st, req)
	if err != nil || !plan.Pool || plan.Tag != singbox.TunnelTag(pool.ID) {
		t.Fatal(plan, err)
	}
	m, err := ingressguard.Read(f.g.manifestPath())
	if err != nil {
		t.Fatal(err)
	}
	m.Guarded = true
	if err = ingressguard.Write(f.g.manifestPath(), m); err != nil {
		t.Fatal(err)
	}
	plan, err = f.g.ClientExitPlan(ctx, st, req)
	if err != nil || plan.Tag != "" || plan.Authority.Epoch != "" {
		t.Fatal(plan, err)
	}
}

func TestClientObservationSourceRunsOutsidePublicationGate(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	if err := api.SetPassword(ctx, f.st, "synthetic-evidence-password"); err != nil {
		t.Fatal(err)
	}
	srv, err := api.New(ctx, api.Config{Store: f.st, Applier: f.g, Logger: quietSlog(), PeerStats: func(context.Context) (map[string]netstack.WGPeerStat, error) {
		if !f.g.mu.TryLock() {
			t.Error("device source read under publication mutex")
			return nil, nil
		}
		f.g.activityEpoch++
		f.g.mu.Unlock()
		return map[string]netstack.WGPeerStat{f.p.PublicKey: {RxBytes: 123}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	srv.Handler().ServeHTTP(login, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"synthetic-evidence-password"}`)))
	cookies := login.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal(login.Body.String())
	}
	req := httptest.NewRequest("POST", "/api/clients/observe", strings.NewReader(`{"peer_id":"`+f.p.ID+`","transport":"wg"}`))
	req.AddCookie(cookies[0])
	out := httptest.NewRecorder()
	srv.Handler().ServeHTTP(out, req)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"reason":"authority_changed"`) || !strings.Contains(out.Body.String(), `"epoch":null`) || strings.Contains(out.Body.String(), `"rx_bytes":123`) {
		t.Fatal(out.Body.String())
	}
}
