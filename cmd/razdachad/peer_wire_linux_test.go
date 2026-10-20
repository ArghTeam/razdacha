//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/miekg/dns"
	"github.com/vishvananda/netlink"
)

// These helpers are overlaid unchanged on the archived pre-fix baseline.
// A and B are distinct bound source identities in a disposable client namespace;
// this adapter exercises policy matching, not an encrypted WireGuard handshake.
type peerWire struct {
	h         *wireHarness
	st        *store.Store
	gate      *applyGate
	a, b      store.Peer
	rule      store.Rule
	mode, dir string
	relay     *peerWireRelay
	sequence  int
}

func newPeerWire(t *testing.T) *peerWire {
	t.Helper()
	p := &peerWire{h: newWireHarness(t), mode: os.Getenv("RAZDACHA_WIRE_PEER_CASE"), dir: t.TempDir()}
	if p.mode != "grant" && p.mode != "reject" {
		t.Fatal("invalid synthetic peer case")
	}
	handle, err := netlink.NewHandleAt(p.h.client)
	wireMust(t, err)
	wireLink(t, handle, "client0", "10.8.0.3/24")
	handle.Close()
	p.st, err = store.Open(p.h.ctx, filepath.Join(p.dir, "state.db"))
	wireMust(t, err)
	t.Cleanup(func() { _ = p.st.Close() })
	settings := p.h.settings
	wireMust(t, p.st.SaveSettings(p.h.ctx, settings))
	p.a, err = api.CreatePeer(p.h.ctx, p.st, "ClientA", time.Now())
	wireMust(t, err)
	p.b, err = api.CreatePeer(p.h.ctx, p.st, "ClientB", time.Now())
	wireMust(t, err)
	if p.a.Address != "10.8.0.2" || p.b.Address != "10.8.0.3" {
		t.Fatal("unexpected synthetic source allocation")
	}
	p.relay = newPeerWireRelay(t)
	action := store.ActionBlock
	tunnelID := ""
	if p.mode == "grant" {
		action = store.ActionTunnel
		parsed, _ := json.Marshal(map[string]any{"server": "127.0.0.1", "server_port": p.relay.listener.Addr().(*net.TCPAddr).Port, "version": "5"})
		tunnel, err := p.st.CreateTunnel(p.h.ctx, store.Tunnel{Name: "Private synthetic relay", Type: store.TunnelSOCKS, Source: store.SourceURL, Raw: "synthetic", Parsed: parsed, Enabled: true})
		wireMust(t, err)
		tunnelID = tunnel.ID
	}
	p.rule, err = p.st.CreateRule(p.h.ctx, store.Rule{Name: "ClientA private policy", Action: action, TunnelID: tunnelID, Enabled: true, PeerScope: store.ScopeSelected, PeerIDs: []string{p.a.ID}, Domains: []string{"private.example"}, Subnets: []string{"203.0.113.10/32"}, RemoteLists: []string{p.h.url}})
	wireMust(t, err)
	runtime := &wireReload{bin: os.Getenv("RAZDACHA_WIRE_BIN"), path: filepath.Join(p.dir, "config.json"), entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(runtime.stop)
	inner := &singbox.Applier{ConfigPath: runtime.path, Checker: singbox.BinaryChecker{Binary: runtime.bin}, Reloader: runtime, Log: quietSlog()}
	publication := &networkPublication{manager: p.h.mgr, path: filepath.Join(p.dir, "generation.json"), uncertain: true, guard: netstack.SetClientGuard, wg: func(_ context.Context, s store.Settings) error { return wireConfigureWG(s) }, route: func(context.Context) error { return netstack.NewRoute().Apply() }, nft: func(ctx context.Context, s store.Snapshot, content map[string]lists.List) (int, error) {
		nf, err := netstack.NewNft()
		if err != nil {
			return 0, err
		}
		subnets := generationSubnets(s, content)
		result, err := nf.Apply(netstack.NftConfig{WANInterface: "wan0", ResolverAddr: s.Settings.WGServerAddress, Subnets: subnets})
		if err == nil && result.SkippedSubnets != 0 {
			err = fmt.Errorf("invalid synthetic subnet")
		}
		return len(subnets), err
	}}
	p.gate = newApplyGate(inner, publication, quietSlog())
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	return p
}

func (p *peerWire) probe(t *testing.T, source, proto, addr, want, stage string) {
	t.Helper()
	p.sequence++
	nonce := fmt.Sprintf("peer215-%s-%s-%s-%d", p.mode, stage, proto, p.sequence)
	if want == "deny" {
		nonce = "blocked-final-" + nonce
	}
	cmd := wireExec(t, p.h.client, "peer-probe", source, proto, addr, nonce, want)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bound synthetic probe %s %s %s: %v %s", stage, source, proto, err, out)
	}
	if !strings.Contains(string(out), "215-BOUND-PROBE-PASS source="+source+" protocol="+proto+" expected="+want) {
		t.Fatal("missing source-bound probe proof")
	}
	if want == "private" {
		p.relay.mu.Lock()
		receipt := p.relay.receipts[proto+"/"+nonce]
		p.relay.mu.Unlock()
		if !receipt {
			t.Fatal("no nonce-matched private relay delivery receipt")
		}
	}
	if proto == "tcp4" || proto == "udp4" {
		if want != "deny" {
			p.h.captureBarrier(t)
			p.h.mu.Lock()
			received := p.h.seen[nonce]
			p.h.mu.Unlock()
			if !received {
				t.Fatal("successful probe absent from sink capture")
			}
		}
	}
}

func (p *peerWire) controls(t *testing.T, stage string) {
	t.Helper()
	for _, proto := range []string{"tcp4", "udp4"} {
		p.probe(t, p.b.Address, proto, "203.0.113.20:12080", "direct", stage+"-control")
	}
	for _, proto := range []string{"dns", "dnstcp"} {
		p.probe(t, p.b.Address, proto, "unrelated.example.", "real", stage+"-control")
	}
}

func (p *peerWire) target(t *testing.T, source, want, stage string) {
	t.Helper()
	for _, proto := range []string{"tcp4", "udp4"} {
		p.probe(t, source, proto, "203.0.113.10:12080", want, stage)
	}
	dnsWant := "real"
	if want == "private" {
		dnsWant = "fake"
	}
	if want == "deny" {
		dnsWant = "refused"
	}
	for _, proto := range []string{"dns", "dnstcp"} {
		p.probe(t, source, proto, "private.example.", dnsWant, stage)
	}
}

func (p *peerWire) initial(t *testing.T) {
	t.Helper()
	want := "private"
	if p.mode == "reject" {
		want = "deny"
	}
	p.target(t, p.a.Address, want, "before-A")
	p.target(t, p.b.Address, "direct", "before-B")
	p.controls(t, "before")
	p.h.assertNoForbidden(t)
	t.Logf("215-PEER-PRECONDITIONS-PASS case=%s", p.mode)
}

// peerProbe binds and verifies the source of every TCP, UDP and DNS socket.
// Denial is accepted only with explicit successful controls in its caller.
func peerProbe(t *testing.T) {
	args := strings.Split(os.Getenv("RAZDACHA_WIRE_ARGS"), "|")
	if len(args) != 5 {
		t.Fatal("invalid peer probe arguments")
	}
	source, proto, target, nonce, want := args[0], args[1], args[2], args[3], args[4]
	ip := net.ParseIP(source)
	if ip == nil {
		t.Fatal("invalid synthetic source")
	}
	if proto == "dns" || proto == "dnstcp" {
		network := "udp"
		if proto == "dnstcp" {
			network = "tcp"
		}
		dialer := &net.Dialer{Timeout: time.Second}
		if network == "udp" {
			dialer.LocalAddr = &net.UDPAddr{IP: ip}
		} else {
			dialer.LocalAddr = &net.TCPAddr{IP: ip}
		}
		c, err := dialer.Dial(network, "203.0.113.20:53")
		wireMust(t, err)
		defer c.Close()
		if !strings.HasPrefix(c.LocalAddr().String(), source+":") {
			t.Fatal("DNS source binding mismatch")
		}
		wireMust(t, c.SetDeadline(time.Now().Add(time.Second)))
		dc := &dns.Conn{Conn: c}
		q := new(dns.Msg)
		q.SetQuestion(target, dns.TypeA)
		wireMust(t, dc.WriteMsg(q))
		answer, err := dc.ReadMsg()
		wireMust(t, err)
		if want == "refused" {
			if answer.Rcode != dns.RcodeRefused {
				t.Fatalf("expected policy REFUSED, got %d", answer.Rcode)
			}
		} else {
			if answer.Rcode != dns.RcodeSuccess || len(answer.Answer) != 1 {
				t.Fatal("invalid synthetic DNS answer")
			}
			a, ok := answer.Answer[0].(*dns.A)
			if !ok {
				t.Fatal("unexpected DNS type")
			}
			fake := netip.MustParsePrefix("198.18.0.0/15").Contains(netip.MustParseAddr(a.A.String()))
			if want == "fake" && !fake || want == "real" && (fake || a.A.String() != "203.0.113.20") {
				t.Fatal("DNS identity mismatch")
			}
		}
	} else {
		d := net.Dialer{Timeout: 500 * time.Millisecond}
		if proto == "udp4" {
			d.LocalAddr = &net.UDPAddr{IP: ip}
		} else {
			d.LocalAddr = &net.TCPAddr{IP: ip}
		}
		c, err := d.Dial(proto, target)
		if err != nil {
			if want != "deny" {
				t.Fatal(err)
			}
		} else {
			defer c.Close()
			if !strings.HasPrefix(c.LocalAddr().String(), source+":") {
				t.Fatal("socket source binding mismatch")
			}
			wireMust(t, c.SetDeadline(time.Now().Add(500*time.Millisecond)))
			_, err = c.Write([]byte(nonce + "\n"))
			expected := nonce + "\n"
			if want == "private" {
				expected = "private:" + nonce + "\n"
			}
			buf := make([]byte, len(expected))
			if err == nil {
				_, err = io.ReadFull(c, buf)
			}
			if want == "deny" {
				if err == nil {
					t.Fatal("protected target echoed")
				}
			} else {
				wireMust(t, err)
				if string(buf) != expected {
					t.Fatal("private/direct delivery identity mismatch")
				}
			}
		}
	}
	fmt.Printf("215-BOUND-PROBE-PASS source=%s protocol=%s expected=%s\n", source, proto, want)
}

// A private SOCKS5 relay forwards only to the contained echo sink and stamps a
// successful nonce-matched echo. A connection count alone is never path proof.
type peerWireRelay struct {
	listener net.Listener
	mu       sync.Mutex
	receipts map[string]bool
}

func newPeerWireRelay(t *testing.T) *peerWireRelay {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	wireMust(t, err)
	r := &peerWireRelay{listener: ln, receipts: map[string]bool{}}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go r.serve(c)
		}
	}()
	return r
}

func (r *peerWireRelay) delivered(proto string, body []byte) bool {
	d := net.Dialer{Timeout: time.Second}
	c, err := d.Dial(proto, "203.0.113.10:12080")
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	if _, err = c.Write(body); err != nil {
		return false
	}
	got := make([]byte, len(body))
	if _, err = io.ReadFull(c, got); err != nil || string(got) != string(body) {
		return false
	}
	r.mu.Lock()
	r.receipts[proto+"/"+strings.TrimSuffix(string(body), "\n")] = true
	r.mu.Unlock()
	return true
}

func (r *peerWireRelay) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(60 * time.Second))
	header := make([]byte, 2)
	if _, err := io.ReadFull(c, header); err != nil || header[0] != 5 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return
	}
	request := make([]byte, 10)
	if _, err := io.ReadFull(c, request[:4]); err != nil || request[0] != 5 || request[3] != 1 {
		return
	}
	if _, err := io.ReadFull(c, request[4:]); err != nil {
		return
	}
	if request[1] == 3 {
		udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			return
		}
		defer udp.Close()
		_ = udp.SetDeadline(time.Now().Add(60 * time.Second))
		reply := []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}
		binary.BigEndian.PutUint16(reply[8:], uint16(udp.LocalAddr().(*net.UDPAddr).Port))
		if _, err = c.Write(reply); err != nil {
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			buf := make([]byte, 4096)
			for {
				n, addr, err := udp.ReadFrom(buf)
				if err != nil {
					return
				}
				if n < 10 || buf[2] != 0 || buf[3] != 1 || net.IP(buf[4:8]).String() != "203.0.113.10" || binary.BigEndian.Uint16(buf[8:10]) != 12080 {
					return
				}
				body := append([]byte(nil), buf[10:n]...)
				if !r.delivered("udp4", body) {
					return
				}
				response := append(append(append([]byte(nil), buf[:10]...), []byte("private:")...), body...)
				if _, err = udp.WriteTo(response, addr); err != nil {
					return
				}
			}
		}()
		_, _ = io.Copy(io.Discard, c)
		_ = udp.Close()
		<-done
		return
	}
	if request[1] != 1 || net.IP(request[4:8]).String() != "203.0.113.10" || binary.BigEndian.Uint16(request[8:]) != 12080 {
		return
	}
	if _, err := c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		return
	}
	body, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || len(body) > 2048 || !r.delivered("tcp4", []byte(body)) {
		return
	}
	_, _ = c.Write(append([]byte("private:"), []byte(body)...))
}
