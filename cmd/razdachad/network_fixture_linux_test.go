//go:build linux

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/miekg/dns"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

func wireMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wireEnv(key, value string) []string {
	out := []string{}
	for _, s := range os.Environ() {
		if !strings.HasPrefix(s, key+"=") {
			out = append(out, s)
		}
	}
	return append(out, key+"="+value)
}

func wireExec(t *testing.T, ns netns.NsHandle, role string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestNetworkGenerationWire$", "-test.timeout=80s")
	cmd.Env = append(wireEnv("RAZDACHA_WIRE_ROLE", "enter"), "RAZDACHA_WIRE_NEXT="+role, "RAZDACHA_WIRE_ARGS="+strings.Join(args, "|"))
	fd, err := unix.Dup(int(ns))
	wireMust(t, err)
	file := os.NewFile(uintptr(fd), "wire-netns")
	t.Cleanup(func() { _ = file.Close() })
	cmd.ExtraFiles = []*os.File{file}
	return cmd
}

func wireNewNS(t *testing.T) netns.NsHandle {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old, err := netns.Get()
	wireMust(t, err)
	defer old.Close()
	ns, err := netns.New()
	wireMust(t, err)
	wireMust(t, netns.Set(old))
	t.Cleanup(func() { _ = ns.Close() })
	return ns
}

func wireLink(t *testing.T, h *netlink.Handle, name, addr string) {
	t.Helper()
	l, err := h.LinkByName(name)
	wireMust(t, err)
	a, err := netlink.ParseAddr(addr)
	wireMust(t, err)
	wireMust(t, h.AddrAdd(l, a))
	wireMust(t, h.LinkSetUp(l))
}

func wireRoute(t *testing.T, h *netlink.Handle, dst, gw, dev string) {
	t.Helper()
	l, err := h.LinkByName(dev)
	wireMust(t, err)
	var d *net.IPNet
	if dst != "" {
		_, d, err = net.ParseCIDR(dst)
		wireMust(t, err)
	}
	wireMust(t, h.RouteAdd(&netlink.Route{LinkIndex: l.Attrs().Index, Dst: d, Gw: net.ParseIP(gw)}))
}

func wireTopology(t *testing.T) (netns.NsHandle, netns.NsHandle) {
	t.Helper()
	client, sink := wireNewNS(t), wireNewNS(t)
	gateway, err := netlink.NewHandle()
	wireMust(t, err)
	defer gateway.Close()
	ch, err := netlink.NewHandleAt(client)
	wireMust(t, err)
	defer ch.Close()
	sh, err := netlink.NewHandleAt(sink)
	wireMust(t, err)
	defer sh.Close()
	for _, h := range []*netlink.Handle{gateway, ch, sh} {
		l, err := h.LinkByName("lo")
		wireMust(t, err)
		wireMust(t, h.LinkSetUp(l))
	}
	for _, v := range []struct {
		name, peer, addr, paddr string
		ns                      netns.NsHandle
		h                       *netlink.Handle
	}{{"wg0", "client0", "10.8.0.1/24", "10.8.0.2/24", client, ch}, {"wan0", "sink0", "192.0.2.1/24", "192.0.2.2/24", sink, sh}} {
		wireMust(t, gateway.LinkAdd(&netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: v.name}, PeerName: v.peer}))
		peer, err := gateway.LinkByName(v.peer)
		wireMust(t, err)
		wireMust(t, gateway.LinkSetNsFd(peer, int(v.ns)))
		wireLink(t, gateway, v.name, v.addr)
		wireLink(t, v.h, v.peer, v.paddr)
	}
	wireLink(t, sh, "lo", "203.0.113.10/32")
	wireLink(t, sh, "lo", "203.0.113.20/32")
	wireLink(t, sh, "lo", "203.0.113.30/32")
	wireLink(t, sh, "lo", "203.0.113.99/32")
	wireRoute(t, gateway, "203.0.113.0/24", "192.0.2.2", "wan0")
	// sing-box's network monitor pauses DNS without a default interface. This
	// synthetic WAN terminates at the disposable sink and has no host uplink.
	wireRoute(t, gateway, "", "192.0.2.2", "wan0")
	wireRoute(t, ch, "", "10.8.0.1", "client0")
	wireRoute(t, sh, "", "192.0.2.1", "sink0")
	wireMust(t, os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0o600))
	return client, sink
}

func wireRole(t *testing.T) bool {
	switch os.Getenv("RAZDACHA_WIRE_ROLE") {
	case "enter":
		runtime.LockOSThread()
		wireMust(t, unix.Setns(3, unix.CLONE_NEWNET))
		wireMust(t, unix.Exec(os.Args[0], os.Args, wireEnv("RAZDACHA_WIRE_ROLE", os.Getenv("RAZDACHA_WIRE_NEXT"))))
		return true
	case "egress-fake", "egress-raw", "egress-dns-failure":
		peerEgressProbe(t)
		return true
	case "peer-probe":
		peerProbe(t)
		return true
	case "native-dns-reject":
		query := new(dns.Msg)
		query.SetQuestion(os.Getenv("RAZDACHA_WIRE_ARGS"), dns.TypeA)
		answer, _, err := (&dns.Client{Timeout: 400 * time.Millisecond}).Exchange(query, "203.0.113.20:53")
		wireMust(t, err)
		if answer == nil || answer.Rcode != dns.RcodeRefused {
			t.Fatal("selected native name was not refused")
		}
		return true
	case "sink":
		wireSink(t)
		return true
	case "probe":
		wireProbe(t)
		return true
	case "stream":
		wireStreamRole(t)
		return true
	}
	return false
}

func wireSink(t *testing.T) {
	// Wildcard UDP WriteTo can choose the main interface as reply source, which
	// connected probes correctly reject. Bind each destination explicitly so
	// its reply keeps the synthetic address that received the request.
	var udpSockets []net.PacketConn
	for _, addr := range []string{"203.0.113.10:12080", "203.0.113.20:12080", "203.0.113.30:12080", "203.0.113.99:12080"} {
		udp, err := net.ListenPacket("udp4", addr)
		wireMust(t, err)
		udpSockets = append(udpSockets, udp)
	}
	tcp, err := net.Listen("tcp4", ":12080")
	wireMust(t, err)
	protocol := uint16(unix.ETH_P_IP)
	protocol = (protocol << 8) | (protocol >> 8)
	raw, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(protocol))
	wireMust(t, err)
	sinkInterface, err := net.InterfaceByName("sink0")
	wireMust(t, err)
	wireMust(t, unix.Bind(raw, &unix.SockaddrLinklayer{Protocol: protocol, Ifindex: sinkInterface.Index}))
	// This receiver is the only authority for capture barriers and packet losses.
	go func() {
		streams := make(map[string]*wirePayloadDetector)
		buf := make([]byte, 65536)
		for {
			n, _, err := unix.Recvfrom(raw, buf, 0)
			if err != nil {
				fmt.Printf("COLLECTOR-ERROR:receive: %v\n", err)
				return
			}
			if n < 34 || binary.BigEndian.Uint16(buf[12:14]) != unix.ETH_P_IP {
				continue
			}
			ip := buf[14:n]
			ihl := int(ip[0]&15) * 4
			totalLength := int(binary.BigEndian.Uint16(ip[2:4]))
			if ihl < 20 || totalLength < ihl+4 || totalLength > len(ip) {
				continue
			}
			ip = ip[:totalLength]
			if ip[9] != unix.IPPROTO_TCP && ip[9] != unix.IPPROTO_UDP {
				if (os.Getenv("RAZDACHA_WIRE_EGRESS") == "1" || os.Getenv("RAZDACHA_WIRE_TRANSPORT") != "") && (ip[9] == 1 || ip[9] == 253) {
					payload := ip[ihl:]
					if ip[9] == 1 && len(payload) >= 8 {
						payload = payload[8:]
					}
					if strings.HasPrefix(string(payload), "egress-raw-") || strings.HasPrefix(string(payload), "blocked-final-egress-raw-") || (os.Getenv("RAZDACHA_WIRE_TRANSPORT") != "" && strings.HasPrefix(string(payload), "transport-raw-")) {
						fmt.Printf("SINK:%s\n", payload)
					}
				}
				continue
			}
			port := binary.BigEndian.Uint16(ip[ihl+2 : ihl+4])
			if ip[9] == unix.IPPROTO_UDP && len(ip) >= ihl+8 {
				udpLength := int(binary.BigEndian.Uint16(ip[ihl+4 : ihl+6]))
				if udpLength < 8 || udpLength > len(ip)-ihl {
					fmt.Println("COLLECTOR-ERROR:invalid UDP length")
					return
				}
				payload := ip[ihl+8 : ihl+udpLength]
				if port == 12082 && strings.HasPrefix(string(payload), "capture-barrier-") {
					stats, statsErr := unix.GetsockoptTpacketStats(raw, unix.SOL_PACKET, unix.PACKET_STATISTICS)
					if statsErr != nil {
						fmt.Printf("COLLECTOR-ERROR:statistics: %v\n", statsErr)
						return
					}
					if stats.Drops != 0 {
						fmt.Printf("COLLECTOR-ERROR:dropped packets: %d\n", stats.Drops)
						return
					}
					fmt.Printf("COLLECTOR-ACK:%s\n", payload)
				}
				var detector wirePayloadDetector
				if port == 12080 && detector.feed(payload) {
					fmt.Println("FORBIDDEN-UDP-PACKET-PAYLOAD")
				}
			}
			if ip[9] == unix.IPPROTO_TCP && len(ip) >= ihl+20 {
				header := int(ip[ihl+12]>>4) * 4
				if header >= 20 && len(ip) >= ihl+header && port == 12080 {
					// Separate directions and connection tuples; the bounded detector
					// also catches markers split across captured TCP payloads.
					key := string(ip[12:20]) + string(ip[ihl:ihl+4])
					detector := streams[key]
					if detector == nil {
						detector = new(wirePayloadDetector)
						streams[key] = detector
					}
					if detector.feed(ip[ihl+header:]) {
						fmt.Println("FORBIDDEN-TCP-PACKET-PAYLOAD")
					}
				}
			}
			if port == 53 {
				fmt.Println("FORBIDDEN-DIRECT-DNS-PACKET")
			}
			if port == 12081 {
				fmt.Println("FORBIDDEN-GUARD-PACKET")
			}
			if port == 53 && ip[9] == unix.IPPROTO_UDP && len(ip) >= ihl+8 {
				m := new(dns.Msg)
				if m.Unpack(ip[ihl+8:]) == nil && len(m.Question) > 0 && strings.HasPrefix(m.Question[0].Name, "guarded-") {
					fmt.Println("FORBIDDEN-GUARD-DNS")
				}
			}
		}
	}()
	for _, udp := range udpSockets {
		go func(udp net.PacketConn) {
			buf := make([]byte, 2048)
			for {
				n, addr, err := udp.ReadFrom(buf)
				if err != nil {
					return
				}
				fmt.Printf("SINK:%s\n", buf[:n])
				_, _ = udp.WriteTo(buf[:n], addr)
			}
		}(udp)
	}

	go func() {
		for {
			c, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 2048)
				var detector wirePayloadDetector
				for {
					n, err := c.Read(buf)
					if n > 0 {
						fmt.Printf("SINK:%s\n", buf[:n])
						if detector.feed(buf[:n]) {
							fmt.Println("FORBIDDEN-TCP-PAYLOAD")
						}
						_, _ = c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	fmt.Println("READY")
	select {}
}

func wireProbe(t *testing.T) {
	args := strings.Split(os.Getenv("RAZDACHA_WIRE_ARGS"), "|")
	if len(args) != 3 {
		t.Fatal("invalid probe arguments")
	}
	if args[0] == "dns" || args[0] == "dnstcp" {
		q := new(dns.Msg)
		q.SetQuestion(args[2], dns.TypeA)
		network := "udp"
		if args[0] == "dnstcp" {
			network = "tcp"
		}
		answer, _, err := (&dns.Client{Net: network, Timeout: 400 * time.Millisecond}).Exchange(q, args[1])
		if err != nil {
			t.Fatalf("DNS transport failed: %v", err)
		}
		if answer == nil {
			t.Fatal("DNS returned no response")
		}
		if answer.Rcode != dns.RcodeSuccess || len(answer.Answer) == 0 {
			t.Fatalf("DNS response: rcode=%s(%d) answers=%d", dns.RcodeToString[answer.Rcode], answer.Rcode, len(answer.Answer))
		}
		return
	}
	c, err := net.DialTimeout(args[0], args[1], 400*time.Millisecond)
	wireMust(t, err)
	defer c.Close()
	wireMust(t, c.SetDeadline(time.Now().Add(400*time.Millisecond)))
	_, err = c.Write([]byte(args[2]))
	wireMust(t, err)
	buf := make([]byte, len(args[2]))
	_, err = io.ReadFull(c, buf)
	wireMust(t, err)
	if string(buf) != args[2] {
		t.Fatal("unexpected echo")
	}
}

// A stream keeps the same TCP/connected-UDP socket through the guarded change.
func wireStreamRole(t *testing.T) {
	args := strings.Split(os.Getenv("RAZDACHA_WIRE_ARGS"), "|")
	if len(args) != 2 {
		t.Fatal("invalid stream arguments")
	}
	c, err := net.DialTimeout(args[0], args[1], 400*time.Millisecond)
	wireMust(t, err)
	defer c.Close()
	fmt.Println("CONNECTED")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		nonce := scanner.Text()
		var ok bool
		_ = c.SetDeadline(time.Now().Add(400 * time.Millisecond))
		if _, err := c.Write([]byte(nonce)); err != nil {
			ok = false
		} else {
			buf := make([]byte, len(nonce))
			_, err := io.ReadFull(c, buf)
			ok = err == nil && string(buf) == nonce
		}
		fmt.Printf("RESULT:%s:%t\n", nonce, ok)
	}
}

type wireStream struct {
	input   io.WriteCloser
	results <-chan string
}

func (h *wireHarness) stream(t *testing.T, proto, addr string) *wireStream {
	t.Helper()
	cmd := wireExec(t, h.client, "stream", proto, addr)
	input, err := cmd.StdinPipe()
	wireMust(t, err)
	output, err := cmd.StdoutPipe()
	wireMust(t, err)
	cmd.Stderr = os.Stderr
	wireMust(t, cmd.Start())
	t.Cleanup(func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	results := make(chan string, 8)
	go func() {
		defer close(results)
		s := bufio.NewScanner(output)
		for s.Scan() {
			results <- s.Text()
		}
	}()
	select {
	case line := <-results:
		if line != "CONNECTED" {
			t.Fatalf("stream not connected: %s", line)
		}
	case <-h.ctx.Done():
		t.Fatal(h.ctx.Err())
	}
	return &wireStream{input: input, results: results}
}

func (s *wireStream) exchange(t *testing.T, nonce string, want bool) {
	t.Helper()
	_, err := fmt.Fprintln(s.input, nonce)
	wireMust(t, err)
	select {
	case result := <-s.results:
		if result != fmt.Sprintf("RESULT:%s:%t", nonce, want) {
			t.Fatalf("established flow: %s", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("established flow timed out")
	}
}

type wireReload struct {
	restarts         atomic.Int64
	bin, path        string
	cmd              *exec.Cmd
	done             chan struct{}
	exit             error
	pause            atomic.Bool
	entered, release chan struct{}
}

func (r *wireReload) stop() {
	if r.cmd != nil {
		_ = r.cmd.Process.Kill()
		<-r.done
		r.cmd = nil
	}
}

func (r *wireReload) Reload(ctx context.Context) error {
	r.restarts.Add(1)
	if r.pause.Swap(false) {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.stop()
	cmd := exec.Command(r.bin, "run", "-c", r.path)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	r.cmd = cmd
	r.done = make(chan struct{})
	go func() { r.exit = cmd.Wait(); close(r.done) }()
	deadline := time.Now().Add(5 * time.Second)
	client := &http.Client{Timeout: 200 * time.Millisecond}
	for time.Now().Before(deadline) {
		select {
		case <-r.done:
			if r.exit == nil {
				return errors.New("fixture runtime exited before readiness")
			}
			return fmt.Errorf("fixture runtime exited: %w", r.exit)
		default:
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:9090/version", nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return fmt.Errorf("fixture runtime readiness timed out")
}

type wireHarness struct {
	ctx           context.Context
	client        netns.NsHandle
	mgr           *lists.Manager
	next          atomic.Bool
	extended      atomic.Bool
	url           string
	settings      store.Settings
	mu            sync.Mutex
	seen          map[string]bool
	collectorErr  error
	collectorDone chan struct{}
	collectorACK  chan string
}

func newWireHarness(t *testing.T) *wireHarness {
	t.Helper()
	clientNS, sinkNS := wireTopology(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	h := &wireHarness{ctx: ctx, client: clientNS, seen: make(map[string]bool), collectorDone: make(chan struct{}), collectorACK: make(chan string, 16)}
	sink := wireExec(t, sinkNS, "sink")
	pipe, err := sink.StdoutPipe()
	wireMust(t, err)
	sink.Stderr = os.Stderr
	wireMust(t, sink.Start())
	t.Cleanup(func() { _ = sink.Process.Kill(); _ = sink.Wait() })
	ready := make(chan struct{})
	go func() {
		defer close(h.collectorDone)
		s := bufio.NewScanner(pipe)
		for s.Scan() {
			v := s.Text()
			if strings.HasPrefix(v, "COLLECTOR-ERROR:") {
				h.mu.Lock()
				h.collectorErr = errors.New(v)
				h.mu.Unlock()
			}
			if strings.HasPrefix(v, "COLLECTOR-ACK:") {
				select {
				case h.collectorACK <- strings.TrimPrefix(v, "COLLECTOR-ACK:"):
				default:
					h.mu.Lock()
					h.collectorErr = errors.New("collector ACK queue overflow")
					h.mu.Unlock()
				}
			}
			if v == "READY" {
				close(ready)
			}
			if strings.HasPrefix(v, "SINK:") || strings.HasPrefix(v, "FORBIDDEN-") {
				h.mu.Lock()
				h.seen[strings.TrimPrefix(v, "SINK:")] = true
				h.mu.Unlock()
			}
		}
		h.mu.Lock()
		h.collectorErr = fmt.Errorf("collector output ended: %w", errors.Join(io.EOF, s.Err()))
		h.mu.Unlock()
	}()
	select {
	case <-h.collectorDone:
		t.Fatal("collector stopped before readiness")
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	dnsSocket, err := net.ListenPacket("udp4", "127.0.0.1:15353")
	wireMust(t, err)
	dnsServer := &dns.Server{PacketConn: dnsSocket, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		answer := new(dns.Msg)
		answer.SetReply(q)
		rr, _ := dns.NewRR(q.Question[0].Name + " 0 IN A 203.0.113.20")
		answer.Answer = []dns.RR{rr}
		_ = w.WriteMsg(answer)
	})}
	dnsDone := make(chan error, 1)
	go func() { dnsDone <- dnsServer.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = dnsServer.Shutdown()
		if err := <-dnsDone; err != nil {
			t.Errorf("fixture DNS server failed: %v", err)
		}
	})
	// Check the local upstream independently of sing-box policy and DNS hijack.
	query := new(dns.Msg)
	query.SetQuestion("fixture-upstream.example.", dns.TypeA)
	response, _, err := (&dns.Client{Net: "udp", Timeout: 400 * time.Millisecond}).Exchange(query, "127.0.0.1:15353")
	wireMust(t, err)
	if response == nil {
		t.Fatal("local fixture DNS upstream returned no response")
	}
	if response.Rcode != dns.RcodeSuccess || len(response.Answer) != 1 {
		t.Fatalf("local fixture DNS upstream: rcode=%s(%d) answers=%d", dns.RcodeToString[response.Rcode], response.Rcode, len(response.Answer))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if h.next.Load() {
			_, _ = io.WriteString(w, "blocked.example\n203.0.113.10/32\n")
			if h.extended.Load() {
				_, _ = io.WriteString(w, "extra.example\n")
			}
		} else {
			_, _ = io.WriteString(w, "old.example\n192.0.2.100/32\n")
		}
	}))
	t.Cleanup(srv.Close)
	h.url = srv.URL
	fetcher, err := lists.NewFetcher(lists.Options{Dir: t.TempDir(), Logger: quietSlog()})
	wireMust(t, err)
	h.mgr = lists.NewManager(lists.ManagerOptions{Fetcher: fetcher, Logger: quietSlog()})
	h.mgr.SetSources([]lists.Source{{URL: h.url, Format: lists.FormatPlain}})
	wireMust(t, h.mgr.Refresh(ctx))
	select {
	case <-h.mgr.Updates():
	default:
	}
	// Prove UDP echo reply identity on every synthetic alias before installing
	// application policy; a broken fixture cannot satisfy negative leak tests.
	for i, addr := range []string{"203.0.113.10:12080", "203.0.113.20:12080", "203.0.113.30:12080", "203.0.113.99:12080"} {
		h.probe(t, "udp4", addr, fmt.Sprintf("fixture-udp-precondition-%d", i), true)
	}
	h.settings = store.DefaultSettings()
	h.settings.DNSType = "udp"
	h.settings.DNSUpstream = "127.0.0.1:15353"
	return h
}

func (h *wireHarness) probe(t *testing.T, proto, addr, nonce string, want bool) {
	t.Helper()
	cmd := wireExec(t, h.client, "probe", proto, addr, nonce)
	out, err := cmd.CombinedOutput()
	if (err == nil) != want {
		t.Fatalf("probe %s %s want=%t error=%v output=%s", proto, addr, want, err, out)
	}
}

// captureBarrier observes only packets queued before its nonce in the isolated
// receiver. It checks zero reported kernel drops; it is not a lossless-network claim.
func (h *wireHarness) captureBarrier(t *testing.T) {
	t.Helper()
	var random [24]byte
	_, err := rand.Read(random[:])
	wireMust(t, err)
	nonce := fmt.Sprintf("capture-barrier-%x", random)
	conn, err := net.Dial("udp4", "192.0.2.2:12082")
	wireMust(t, err)
	_, err = conn.Write([]byte(nonce))
	_ = conn.Close()
	wireMust(t, err)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case ack := <-h.collectorACK:
		if ack != nonce {
			t.Fatalf("unexpected capture barrier ACK: %q", ack)
		}
	case <-h.collectorDone:
		t.Fatal("collector output ended before evidence barrier")
	case <-timer.C:
		t.Fatal("capture barrier ACK timed out")
	case <-h.ctx.Done():
		t.Fatal(h.ctx.Err())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.collectorErr != nil {
		t.Fatal(h.collectorErr)
	}
	if os.Getenv("RAZDACHA_WIRE_NATIVE_CASE") != "" || os.Getenv("RAZDACHA_WIRE_PEER_CASE") != "" {
		t.Log("220-CAPTURE-BARRIER-PASS drops=0")
	} else {
		t.Log("214-CAPTURE-BARRIER-PASS drops=0")
	}
}

func (h *wireHarness) assertNoForbidden(t *testing.T) {
	t.Helper()
	h.captureBarrier(t)
	h.mu.Lock()
	defer h.mu.Unlock()
	for nonce := range h.seen {
		if strings.HasPrefix(nonce, "guarded-") || strings.HasPrefix(nonce, "blocked-final-") || strings.HasPrefix(nonce, "FORBIDDEN-") {
			t.Fatalf("forbidden direct packet or payload reached fixture sink: %s", nonce)
		}
	}
}

// TestNetworkGenerationWire only mutates a freshly cloned network and mount
// namespace. A private tmpfs hides sing-box's hardcoded cache directory.
func TestNetworkGenerationWire(t *testing.T) {
	if os.Getenv("RAZDACHA_NETNS_TEST") != "1" {
		t.Skip("explicit RAZDACHA_NETNS_TEST=1 is required")
	}
	if os.Geteuid() != 0 {
		t.Fatal("root is required for this opt-in fixture")
	}
	if wireRole(t) {
		return
	}
	if os.Getenv("RAZDACHA_WIRE_ROLE") != "gateway" {
		bin, err := exec.LookPath(os.Getenv("RAZDACHA_TEST_SINGBOX"))
		wireMust(t, err)
		bin, err = filepath.Abs(bin)
		wireMust(t, err)
		parent, err := os.Readlink("/proc/self/ns/net")
		wireMust(t, err)
		parentMount, err := os.Readlink("/proc/self/ns/mnt")
		wireMust(t, err)
		args := []string{"-test.run=^TestNetworkGenerationWire$", "-test.timeout=80s"}
		if testing.Verbose() {
			args = append(args, "-test.v")
		}
		cmd := exec.Command(os.Args[0], args...)
		cmd.Env = append(wireEnv("RAZDACHA_WIRE_ROLE", "gateway"), "RAZDACHA_WIRE_PARENT="+parent, "RAZDACHA_WIRE_PARENT_MOUNT="+parentMount, "RAZDACHA_WIRE_BIN="+bin)
		cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWNET | unix.CLONE_NEWNS}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated fixture failed: %v\n%s", err, out)
		}
		t.Logf("%s", out)
		return
	}
	current, err := os.Readlink("/proc/self/ns/net")
	wireMust(t, err)
	if current == os.Getenv("RAZDACHA_WIRE_PARENT") || os.Getenv("RAZDACHA_WIRE_PARENT") == "" {
		t.Fatal("refusing host namespace")
	}
	currentMount, err := os.Readlink("/proc/self/ns/mnt")
	wireMust(t, err)
	if currentMount == os.Getenv("RAZDACHA_WIRE_PARENT_MOUNT") || os.Getenv("RAZDACHA_WIRE_PARENT_MOUNT") == "" {
		t.Fatal("refusing host mount namespace")
	}
	wireMust(t, unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""))
	wireMust(t, unix.Mount("tmpfs", "/var/lib", "tmpfs", 0, "size=16m"))
	wireMust(t, os.MkdirAll("/var/lib/sing-box", 0o700))
	switch {
	case os.Getenv("RAZDACHA_WIRE_TRANSPORT") != "":
		nativeTransportWireExercise(t)
	case os.Getenv("RAZDACHA_WIRE_EGRESS") == "1":
		peerEgressWireExercise(t)
	case os.Getenv("RAZDACHA_WIRE_PEER_CASE") != "":
		peerWireExercise(t)
	case os.Getenv("RAZDACHA_WIRE_NATIVE_CASE") != "":
		nativeWireExercise(t)
	default:
		wireExercise(t)
	}
}

// wireConfigureWG uses the production WG settings parser and the same address
// replacement semantics on a disposable veth; it never configures a real key.
func wireConfigureWG(settings store.Settings) error {
	cfg, err := netstack.WGConfigFromSettings(settings)
	if err != nil {
		return err
	}
	link, err := netlink.LinkByName("wg0")
	if err != nil {
		return err
	}
	addresses, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	desired, err := netlink.ParseAddr(cfg.Address.String())
	if err != nil {
		return err
	}
	for _, addr := range addresses {
		if addr.String() != desired.String() {
			if err := netlink.AddrDel(link, &addr); err != nil {
				return err
			}
		}
	}
	if err := netlink.AddrReplace(link, desired); err != nil {
		return err
	}
	if err := netlink.LinkSetMTU(link, cfg.MTU); err != nil {
		return err
	}
	return netlink.LinkSetUp(link)
}

func wireResolverIsLocal(settings store.Settings) error {
	link, err := netlink.LinkByName("wg0")
	if err != nil {
		return err
	}
	addresses, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	for _, addr := range addresses {
		if addr.IP.String() == settings.WGServerAddress {
			return nil
		}
	}
	return fmt.Errorf("committed resolver is not local on the fixture interface")
}
