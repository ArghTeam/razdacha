//go:build linux

package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// This fixture reuses the private NET+MOUNT namespace, real nft/TProxy runtime,
// two source identities and independent AF_PACKET/drop-accounted sink collector.
// The incoming wg0 device is a veth adapter, not an encrypted WG handshake.
func peerEgressWireExercise(t *testing.T) {
	wireMust(t, os.Setenv("RAZDACHA_WIRE_PEER_CASE", "grant"))
	p := newPeerWire(t)
	p.controls(t, "egress-precondition")
	p.rule.Domains = append(p.rule.Domains, "private-address.example")
	wireMust(t, p.st.UpdateRule(p.h.ctx, p.rule))
	_, err := p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	// Obtain a real cached FakeIP before switching A from shared rules.
	cmd := wireExec(t, p.h.client, "egress-fake", p.a.Address, "private.example.")
	out, err := cmd.CombinedOutput()
	wireMust(t, err)
	var fake string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "EGRESS-FAKE-IP=") {
			fake = strings.TrimPrefix(line, "EGRESS-FAKE-IP=")
		}
	}
	addr, err := netip.ParseAddr(fake)
	wireMust(t, err)
	if !netip.MustParsePrefix("198.18.0.0/15").Contains(addr) {
		t.Fatal("missing cached FakeIP precondition")
	}
	privateCommand := wireExec(t, p.h.client, "egress-fake", p.a.Address, "private-address.example.")
	privateOut, privateErr := privateCommand.CombinedOutput()
	wireMust(t, privateErr)
	var privateFake string
	for _, line := range strings.Split(string(privateOut), "\n") {
		if strings.HasPrefix(line, "EGRESS-FAKE-IP=") {
			privateFake = strings.TrimPrefix(line, "EGRESS-FAKE-IP=")
		}
	}
	privateAddr, err := netip.ParseAddr(privateFake)
	wireMust(t, err)
	if !netip.MustParsePrefix("198.18.0.0/15").Contains(privateAddr) {
		t.Fatal("missing private stale FakeIP precondition")
	}
	relay := newEgressWireRelay(t)
	relay.proof = p.relay
	target, err := p.st.Tunnel(p.h.ctx, p.rule.TunnelID)
	wireMust(t, err)
	target.Parsed, err = json.Marshal(map[string]any{"server": "127.0.0.1", "server_port": relay.listener.Addr().(*net.TCPAddr).Port, "version": "5"})
	wireMust(t, err)
	wireMust(t, p.st.UpdateTunnel(p.h.ctx, target))
	p.a.EgressTunnelID = &target.ID
	wireMust(t, p.st.UpdatePeer(p.h.ctx, p.a))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	for _, proto := range []string{"tcp4", "udp4"} {
		p.probe(t, p.a.Address, proto, "203.0.113.20:12080", "private", "egress-literal")
		p.probe(t, p.a.Address, proto, net.JoinHostPort(fake, "12080"), "private", "egress-stale-fake")
	}
	for _, proto := range []string{"dns", "dnstcp"} {
		p.probe(t, p.a.Address, proto, "whole-egress.example.", "real", "egress-dns")
	}
	relay.mu.Lock()
	dnsProof := relay.questions["whole-egress.example."] > 0 && relay.questions["private.example."] > 0
	relay.mu.Unlock()
	if !dnsProof {
		t.Fatal("no selected-exit DNS receipts for normal and stale FakeIP resolution")
	}
	// Source A's private relay receipts are maintained by the dedicated relay.
	for _, proto := range []string{"tcp4", "udp4"} {
		p.probe(t, p.a.Address, proto, net.JoinHostPort(privateFake, "12080"), "deny", "egress-stale-private")
	}
	relay.mu.Lock()
	privateDNS := relay.questions["private-address.example."] > 0
	privateAttempts := relay.privateAttempts
	relay.mu.Unlock()
	if !privateDNS || privateAttempts != 0 {
		t.Fatal("resolved private destination bypassed post-resolve reject")
	}
	p.controls(t, "egress-shared-unaffected")
	for _, proto := range []string{"1", "253"} {
		p.rawEgress(t, p.b.Address, proto, true, "before")
		p.rawEgress(t, p.a.Address, proto, false, "selected")
		p.rawEgress(t, p.b.Address, proto, true, "after")
	}
	// Pending clear must not escape via background list/pool refresh or recovery.
	p.a.EgressTunnelID = nil
	wireMust(t, p.st.UpdatePeer(p.h.ctx, p.a))
	_, err = p.gate.refreshLists(p.h.ctx)
	wireMust(t, err)
	fresh, err := p.st.Snapshot(p.h.ctx)
	wireMust(t, err)
	_, err = p.gate.applyPools(p.h.ctx, fresh)
	wireMust(t, err)
	for _, proto := range []string{"tcp4", "udp4"} {
		p.probe(t, p.a.Address, proto, "203.0.113.20:12080", "private", "egress-pending")
	}
	// Disabled targets retain their choice and reject DNS as well as literal IPs.
	p.a.EgressTunnelID = &target.ID
	wireMust(t, p.st.UpdatePeer(p.h.ctx, p.a))
	target.Enabled = false
	wireMust(t, p.st.UpdateTunnel(p.h.ctx, target))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	for _, proto := range []string{"tcp4", "udp4"} {
		p.probe(t, p.a.Address, proto, "203.0.113.20:12080", "deny", "egress-disabled")
	}
	for _, proto := range []string{"dns", "dnstcp"} {
		p.probe(t, p.a.Address, proto, "disabled-egress.example.", "refused", "egress-disabled")
	}
	p.controls(t, "egress-disabled-other-client")
	// An enabled but unreachable transport must not fall through to the global DNS.
	target.Enabled = true
	target.Parsed = []byte(`{"server":"127.0.0.1","server_port":1,"version":"5"}`)
	wireMust(t, p.st.UpdateTunnel(p.h.ctx, target))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	for _, proto := range []string{"tcp4", "udp4"} {
		p.probe(t, p.a.Address, proto, "203.0.113.20:12080", "deny", "egress-unreachable")
	}
	cmd = wireExec(t, p.h.client, "egress-dns-failure", p.a.Address, "unreachable-egress.example.")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("unreachable DNS guard: %v %s", err, out)
	}
	p.controls(t, "egress-unreachable-other-client")
	// Explicitly restoring shared mode restores direct Internet only for A.
	p.a.EgressTunnelID = nil
	wireMust(t, p.st.UpdatePeer(p.h.ctx, p.a))
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	for _, proto := range []string{"tcp4", "udp4"} {
		p.probe(t, p.a.Address, proto, "203.0.113.20:12080", "direct", "egress-cleared")
	}
	p.controls(t, "egress-cleared-other-client")
	p.h.assertNoForbidden(t)
	t.Log("231-PEER-EGRESS-WIRE-PASS TCP UDP DNS stale-FakeIP unavailable source-isolation raw-protocols pending-clear")
}

func (p *peerWire) rawEgress(t *testing.T, source, proto string, want bool, stage string) {
	t.Helper()
	p.sequence++
	nonce := fmt.Sprintf("egress-raw-%s-%s-%d", stage, proto, p.sequence)
	if !want {
		nonce = "blocked-final-" + nonce
	}
	cmd := wireExec(t, p.h.client, "egress-raw", source, proto, "203.0.113.20", nonce)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("raw send: %v %s", err, out)
	}
	p.h.captureBarrier(t)
	p.h.mu.Lock()
	got := p.h.seen[nonce]
	p.h.mu.Unlock()
	if got != want {
		t.Fatalf("raw protocol %s sink delivery=%v want=%v", proto, got, want)
	}
	p.h.assertNoForbidden(t)
}

func peerEgressProbe(t *testing.T) {
	args := strings.Split(os.Getenv("RAZDACHA_WIRE_ARGS"), "|")
	if os.Getenv("RAZDACHA_WIRE_ROLE") == "egress-raw" {
		if len(args) != 4 {
			t.Fatal("invalid raw args")
		}
		proto, err := strconv.Atoi(args[1])
		wireMust(t, err)
		c, err := net.DialIP(fmt.Sprintf("ip4:%d", proto), &net.IPAddr{IP: net.ParseIP(args[0])}, &net.IPAddr{IP: net.ParseIP(args[2])})
		wireMust(t, err)
		defer c.Close()
		payload := []byte(args[3])
		if proto == 1 {
			payload = append([]byte{8, 0, 0, 0, 0, 1, 0, 1}, payload...)
			var sum uint32
			for i := 0; i+1 < len(payload); i += 2 {
				sum += uint32(binary.BigEndian.Uint16(payload[i : i+2]))
			}
			if len(payload)%2 != 0 {
				sum += uint32(payload[len(payload)-1]) << 8
			}
			for sum>>16 != 0 {
				sum = (sum & 65535) + (sum >> 16)
			}
			binary.BigEndian.PutUint16(payload[2:4], ^uint16(sum))
		}
		wireMust(t, c.SetWriteDeadline(time.Now().Add(time.Second)))
		_, err = c.Write(payload)
		wireMust(t, err)
		fmt.Println("231-RAW-SEND-PASS")
		return
	}
	if len(args) != 2 {
		t.Fatal("invalid DNS args")
	}
	d := &net.Dialer{Timeout: time.Second, LocalAddr: &net.UDPAddr{IP: net.ParseIP(args[0])}}
	c, err := d.Dial("udp4", "203.0.113.20:53")
	wireMust(t, err)
	defer c.Close()
	wireMust(t, c.SetDeadline(time.Now().Add(time.Second)))
	dc := &dns.Conn{Conn: c}
	q := new(dns.Msg)
	q.SetQuestion(args[1], dns.TypeA)
	wireMust(t, dc.WriteMsg(q))
	answer, err := dc.ReadMsg()
	if os.Getenv("RAZDACHA_WIRE_ROLE") == "egress-dns-failure" {
		if err == nil && answer.Rcode == dns.RcodeSuccess && len(answer.Answer) > 0 {
			t.Fatal("unreachable exit fell through to successful DNS")
		}
		fmt.Println("231-DNS-FAILURE-PASS")
		return
	}
	wireMust(t, err)
	if answer.Rcode != dns.RcodeSuccess || len(answer.Answer) != 1 {
		t.Fatal("invalid initial fake answer")
	}
	a, ok := answer.Answer[0].(*dns.A)
	if !ok {
		t.Fatal("not A response")
	}
	fmt.Println("EGRESS-FAKE-IP=" + a.A.String())
}

// The separate synthetic exit records decoded DNS names and stamps echo replies.
// It permits only fixed fixture DNS and echo destinations inside this namespace.
type egressWireRelay struct {
	proof           *peerWireRelay
	listener        net.Listener
	mu              sync.Mutex
	questions       map[string]int
	privateAttempts int
}

func newEgressWireRelay(t *testing.T) *egressWireRelay {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	wireMust(t, err)
	r := &egressWireRelay{listener: ln, questions: map[string]int{}}
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

func (r *egressWireRelay) exchange(proto, target string, body []byte) ([]byte, error) {
	if target == "127.0.0.1:15353" {
		q := new(dns.Msg)
		if err := q.Unpack(body); err != nil || len(q.Question) != 1 {
			return nil, fmt.Errorf("invalid DNS question")
		}
		r.mu.Lock()
		r.questions[q.Question[0].Name]++
		r.mu.Unlock()
		answer := new(dns.Msg)
		answer.SetReply(q)
		if q.Question[0].Qtype == dns.TypeA {
			answerIP := "203.0.113.20"
			if q.Question[0].Name == "private-address.example." {
				answerIP = "10.20.0.1"
			}
			rr, _ := dns.NewRR(q.Question[0].Name + " 0 IN A " + answerIP)
			answer.Answer = []dns.RR{rr}
		}
		return answer.Pack()
	}
	if target == "10.20.0.1:12080" {
		r.mu.Lock()
		r.privateAttempts++
		r.mu.Unlock()
		return append([]byte("private:"), body...), nil
	}
	if target != "203.0.113.10:12080" && target != "203.0.113.20:12080" {
		return nil, fmt.Errorf("unexpected destination")
	}
	c, err := net.DialTimeout(proto, target, time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	if _, err = c.Write(body); err != nil {
		return nil, err
	}
	got := make([]byte, len(body))
	if _, err = io.ReadFull(c, got); err != nil {
		return nil, err
	}
	if string(got) != string(body) {
		return nil, fmt.Errorf("echo identity mismatch")
	}
	r.proof.mu.Lock()
	r.proof.receipts[proto+"/"+strings.TrimSuffix(string(body), "\n")] = true
	r.proof.mu.Unlock()
	return append([]byte("private:"), got...), nil
}

func (r *egressWireRelay) serve(c net.Conn) {
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
	command, target, err := readEgressSOCKSRequest(c)
	if err != nil {
		return
	}
	if command == 3 {
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
				n, source, err := udp.ReadFrom(buf)
				if err != nil {
					return
				}
				if n < 10 || buf[2] != 0 || buf[3] != 1 {
					return
				}
				target := net.JoinHostPort(net.IP(buf[4:8]).String(), strconv.Itoa(int(binary.BigEndian.Uint16(buf[8:10]))))
				body, err := r.exchange("udp4", target, append([]byte(nil), buf[10:n]...))
				if err != nil {
					return
				}
				response := append(append([]byte(nil), buf[:10]...), body...)
				if _, err = udp.WriteTo(response, source); err != nil {
					return
				}
			}
		}()
		_, _ = io.Copy(io.Discard, c)
		_ = udp.Close()
		<-done
		return
	}
	if command != 1 {
		return
	}
	if _, err := c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		return
	}
	body, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || len(body) > 2048 {
		return
	}
	response, err := r.exchange("tcp4", target, []byte(body))
	if err == nil {
		_, _ = c.Write(response)
	}
}
