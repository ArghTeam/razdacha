//go:build linux

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/sagernet/sing-box/common/srs"
	"github.com/sagernet/sing-box/option"
	SJSON "github.com/sagernet/sing/common/json"
)

func nativeTransportWireExercise(t *testing.T) {
	wireMust(t, os.Setenv("RAZDACHA_WIRE_PEER_CASE", "grant"))
	p := newPeerWire(t)
	targetID := p.rule.TunnelID
	var wrongPort atomic.Bool
	format := os.Getenv("RAZDACHA_WIRE_TRANSPORT")
	if format != "json" && format != "srs" {
		t.Fatal("invalid transport fixture format")
	}
	encode := func(port int) []byte {
		body := []byte(fmt.Sprintf(`{"version":3,"rules":[{"domain_suffix":"transport.example"},{"network":"udp","ip_cidr":"203.0.113.0/28","port_range":"%d:%d"}]}`, port, port))
		if format == "srs" {
			var c option.PlainRuleSetCompat
			wireMust(t, SJSON.Unmarshal(body, &c))
			var b bytes.Buffer
			wireMust(t, srs.Write(&b, c.Options, 3))
			return b.Bytes()
		}
		return body
	}
	selected, miss := encode(12080), encode(12081)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if wrongPort.Load() {
			_, _ = w.Write(miss)
		} else {
			_, _ = w.Write(selected)
		}
	}))
	t.Cleanup(server.Close)
	url := server.URL + "/transport." + format
	p.h.mgr.SetSources([]lists.Source{{URL: url, Format: lists.Format(format)}})
	wireMust(t, p.h.mgr.Refresh(p.h.ctx))
	p.rule.Domains = nil
	p.rule.Subnets = nil
	p.rule.RemoteLists = []string{url}
	p.rule.Action = store.ActionTunnel
	p.rule.TunnelID = targetID
	wireMust(t, p.st.UpdateRule(p.h.ctx, p.rule))
	_, err := p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	p.probe(t, p.a.Address, "udp4", "203.0.113.10:12080", "private", "transport-enabled-exit")
	p.probe(t, p.a.Address, "tcp4", "203.0.113.10:12080", "direct", "transport-enabled-wrong-protocol")
	p.rule.Action = store.ActionBlock
	p.rule.TunnelID = ""
	wireMust(t, p.st.UpdateRule(p.h.ctx, p.rule))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	p.probe(t, p.a.Address, "udp4", "203.0.113.10:12080", "deny", "transport-positive")
	p.probe(t, p.a.Address, "tcp4", "203.0.113.10:12080", "direct", "transport-wrong-protocol")
	p.probe(t, p.a.Address, "udp4", "203.0.113.20:12080", "direct", "transport-wrong-address")
	p.probe(t, p.b.Address, "udp4", "203.0.113.10:12080", "direct", "transport-other-peer")
	// A different port is tested against the same reachable sink, avoiding a false
	// pass caused by a nonexistent service on the control destination port.
	wrongPort.Store(true)
	wireMust(t, p.h.mgr.Refresh(p.h.ctx))
	_, err = p.gate.refreshLists(p.h.ctx)
	wireMust(t, err)
	p.probe(t, p.a.Address, "udp4", "203.0.113.10:12080", "direct", "transport-wrong-port")
	wrongPort.Store(false)
	wireMust(t, p.h.mgr.Refresh(p.h.ctx))
	_, err = p.gate.refreshLists(p.h.ctx)
	wireMust(t, err)
	direct, err := p.st.CreateRule(p.h.ctx, store.Rule{Name: "Earlier direct exception", Enabled: true, Action: store.ActionDirect, PeerScope: store.ScopeAll, Subnets: []string{"203.0.113.10/32"}})
	wireMust(t, err)
	wireMust(t, p.st.ReorderRules(p.h.ctx, []string{direct.ID, p.rule.ID}))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	p.probe(t, p.a.Address, "udp4", "203.0.113.10:12080", "direct", "transport-rule-order")
	// Remove the ordinary all-protocol subnet mark before checking ICMP isolation.
	wireMust(t, p.st.DeleteRule(p.h.ctx, direct.ID))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	// Raw probes target .10 inside the envelope and must remain unmarked.
	nativeTransportRaw(t, p, "1")
	nativeTransportRaw(t, p, "253")
	p.probe(t, p.a.Address, "dns", "transport.example.", "deny", "transport-domain-dns")
	p.probe(t, p.a.Address, "dns", "unrelated.example.", "real", "transport-unrelated-dns")
	target, err := p.st.Tunnel(p.h.ctx, targetID)
	wireMust(t, err)
	target.Enabled = false
	wireMust(t, p.st.UpdateTunnel(p.h.ctx, target))
	p.rule.Action = store.ActionTunnel
	p.rule.TunnelID = targetID
	wireMust(t, p.st.UpdateRule(p.h.ctx, p.rule))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	p.probe(t, p.a.Address, "udp4", "203.0.113.10:12080", "deny", "transport-unavailable")
	p.probe(t, p.a.Address, "tcp4", "203.0.113.10:12080", "direct", "transport-unavailable-protocol-control")
	p.a.EgressTunnelID = &targetID
	wireMust(t, p.st.UpdatePeer(p.h.ctx, p.a))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	p.probe(t, p.a.Address, "tcp4", "203.0.113.10:12080", "deny", "transport-peer-egress-precedence")
	p.probe(t, p.b.Address, "udp4", "203.0.113.10:12080", "direct", "transport-egress-other-peer")
	p.a.EgressTunnelID = nil
	wireMust(t, p.st.UpdatePeer(p.h.ctx, p.a))
	p.rule.Action = store.ActionBlock
	p.rule.TunnelID = ""
	wireMust(t, p.st.UpdateRule(p.h.ctx, p.rule))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	gen, err := readGeneration(p.gate.network.path)
	wireMust(t, err)
	if gen.Version != 4 || len(gen.Lists[url].TransportSubnets) != 1 {
		t.Fatal("transport authority missing")
	}
	p.gate.network.uncertain = true
	wireMust(t, p.gate.recover(p.h.ctx, filepath.Join(p.dir, "absent")))
	p.probe(t, p.a.Address, "udp4", "203.0.113.10:12080", "deny", "transport-recovery")
	p.h.assertNoForbidden(t)
	t.Log("235-NATIVE-TRANSPORT-WIRE-PASS UDP positive TCP/address/port negative peer-scope ordering recovery")
}

func nativeTransportRaw(t *testing.T, p *peerWire, proto string) {
	t.Helper()
	nonce := "transport-raw-" + proto
	out, err := wireExec(t, p.h.client, "egress-raw", p.a.Address, proto, "203.0.113.10", nonce).CombinedOutput()
	if err != nil {
		t.Fatalf("raw probe: %v %s", err, out)
	}
	p.h.captureBarrier(t)
	p.h.mu.Lock()
	got := p.h.seen[nonce]
	p.h.mu.Unlock()
	if !got {
		t.Fatalf("transport envelope intercepted raw protocol %s", proto)
	}
}
