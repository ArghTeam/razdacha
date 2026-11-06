package singbox

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/miekg/dns"
)

type denialDNS struct {
	mu      sync.Mutex
	counts  map[string]int
	address string
}

func controlledDenialDNS(t *testing.T, answer string) *denialDNS {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &denialDNS{counts: map[string]int{}, address: pc.LocalAddr().String()}
	server := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(r)
		for _, q := range r.Question {
			d.mu.Lock()
			d.counts[q.Name]++
			d.mu.Unlock()
			if q.Qtype == dns.TypeA {
				response.Answer = append(response.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}, A: net.ParseIP(answer)})
			}
		}
		_ = w.WriteMsg(response)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })
	return d
}

func (d *denialDNS) count(name string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.counts[dns.Fqdn(name)]
}

// Both terminal protocols stay on owned loopback, with counters identifying the
// original positive winner. Only terminal dial overrides and ingress TLS are
// adapted; every generated route predicate and DNS action remains intact.
func denialEchoSink(t *testing.T, name string, events chan<- string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	pc, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close(); _ = pc.Close() })
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(time.Second))
				b := make([]byte, 1024)
				n, e := c.Read(b)
				if e == nil {
					events <- name
					_, _ = c.Write(b[:n])
				}
			}()
		}
	}()
	go func() {
		b := make([]byte, 1024)
		for {
			n, a, e := pc.ReadFrom(b)
			if e != nil {
				return
			}
			events <- name
			_, _ = pc.WriteTo(b[:n], a)
		}
	}()
	return port
}

func denialRuntime(t *testing.T, binaryPath, dir string, snap store.Snapshot, live *store.RealityRuntime, plain PlainLists, direct, selected int) (int, func()) {
	t.Helper()
	opts, err := GenerateWithReality(snap, plain, live)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	port := sniffFreePort(t)
	var users []any
	for _, u := range live.Users {
		users = append(users, map[string]any{"name": u.Name, "uuid": u.UUID})
	}
	cfg["inbounds"] = []any{map[string]any{"type": "vless", "tag": TagRealityIn, "listen": "127.0.0.1", "listen_port": port, "users": users}}
	cfg["outbounds"] = []any{map[string]any{"type": "direct", "tag": TagDirect}, map[string]any{"type": "direct", "tag": TunnelTag("exit"), "connect_timeout": "2s"}}
	for _, item := range cfg["route"].(map[string]any)["rules"].([]any) {
		r := item.(map[string]any)
		if _, ok := r["auth_user"]; !ok {
			continue
		}
		if out, ok := r["outbound"].(string); ok {
			r["override_address"] = "127.0.0.1"
			r["override_port"] = direct
			if out == TunnelTag("exit") {
				r["override_port"] = selected
			}
		}
	}
	cfg["experimental"] = map[string]any{"cache_file": map[string]any{"enabled": true, "path": filepath.Join(dir, "cache.db"), "store_fakeip": true}}
	data, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, e := exec.CommandContext(ctx, binaryPath, "check", "-c", path).CombinedOutput(); e != nil {
		t.Fatalf("check %v: %s", e, out)
	}
	log, err := os.Create(filepath.Join(dir, "runtime.log"))
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command(binaryPath, "run", "-D", dir, "-c", path)
	process.Stdout = log
	process.Stderr = log
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() { _ = process.Process.Signal(os.Interrupt); _ = process.Wait(); _ = log.Close() })
	}
	t.Cleanup(stop)
	until := time.Now().Add(5 * time.Second)
	for {
		c, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 20*time.Millisecond)
		if e == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(until) {
			body, _ := os.ReadFile(log.Name())
			t.Fatalf("runtime start: %s", body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return port, stop
}

func denialVLESS(t *testing.T, port int, user store.RealityUser, host string, dstPort uint16, udp bool) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(1200 * time.Millisecond))
	id, err := hex.DecodeString(strings.ReplaceAll(user.UUID, "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	request := append([]byte{0}, id...)
	request = append(request, 0, 1)
	if udp {
		request[len(request)-1] = 2
	}
	request = binary.BigEndian.AppendUint16(request, dstPort)
	if ip := net.ParseIP(host).To4(); ip != nil {
		request = append(request, 1)
		request = append(request, ip...)
	} else {
		request = append(request, 2, byte(len(host)))
		request = append(request, host...)
	}
	if _, err = c.Write(request); err != nil {
		t.Fatal(err)
	}
	return c
}

func denialPayload(t *testing.T, port int, user store.RealityUser, host string, udp bool) {
	t.Helper()
	c := denialVLESS(t, port, user, host, 443, udp)
	defer c.Close()
	body := []byte("owned fixture payload")
	if udp {
		body = append(binary.BigEndian.AppendUint16(nil, uint16(len(body))), body...)
	}
	_, _ = c.Write(body)
	_, _ = io.ReadAll(c)
}

func denialGatewayQuery(t *testing.T, port int, user store.RealityUser, host string, qtype uint16, udp bool) *dns.Msg {
	t.Helper()
	c := denialVLESS(t, port, user, "10.8.0.1", 53, udp)
	defer c.Close()
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(host), qtype)
	packet, err := q.Pack()
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(packet))), packet...))
	if err != nil {
		t.Fatal(err)
	}
	h := make([]byte, 2)
	if _, err = io.ReadFull(c, h); err != nil {
		return nil
	}
	if h[0] != 0 {
		t.Fatalf("VLESS response version %d", h[0])
	}
	if h[1] > 0 {
		if _, err = io.CopyN(io.Discard, c, int64(h[1])); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = io.ReadFull(c, h); err != nil {
		return nil
	}
	packet = make([]byte, int(binary.BigEndian.Uint16(h)))
	if _, err = io.ReadFull(c, packet); err != nil {
		return nil
	}
	response := new(dns.Msg)
	if err = response.Unpack(packet); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestRealityDenialPinnedRuntime(t *testing.T) {
	binaryPath := os.Getenv("RAZDACHA_TEST_SINGBOX")
	if binaryPath == "" {
		t.Skip("set RAZDACHA_TEST_SINGBOX for controlled DNS proof")
	}
	version, err := exec.Command(binaryPath, "version").Output()
	if err != nil || !strings.Contains(string(version), "sing-box version 1.12.25\n") {
		t.Fatal("pinned sing-box 1.12.25 required")
	}
	for _, tc := range []struct {
		name, kind, answer, user, want string
		queries                        int
	}{
		{"block", "block", "203.0.113.8", "a", "", 0},
		{"disabled", "disabled", "203.0.113.8", "a", "", 0},
		{"empty-pool", "empty", "203.0.113.8", "a", "", 0},
		{"plain", "plain", "203.0.113.8", "a", "", 0},
		{"native", "native", "203.0.113.8", "a", "", 0},
		{"native-or-deny", "native-or-deny", "203.0.113.8", "a", "", 0},
		{"native-context-positive", "native-positive", "203.0.113.8", "a", "", 1},
		{"unselected", "block", "203.0.113.8", "b", "direct", 1},
		{"earlier-direct", "direct", "203.0.113.8", "a", "direct", 1},
		{"earlier-all-direct", "direct", "203.0.113.8", "a", "direct", 1},
		{"earlier-tunnel", "tunnel", "203.0.113.8", "a", "selected", 1},
		{"other-user-no-exception", "direct", "203.0.113.8", "b", "", 0},
		{"earlier-deny", "later-allow", "203.0.113.8", "a", "", 0},
		{"earlier-cidr-positive", "cidr", "203.0.113.8", "a", "direct", 1},
		{"earlier-cidr-miss", "cidr", "198.51.100.8", "a", "", 1},
		{"earlier-cidr-deny", "cidr-deny", "203.0.113.8", "a", "", 0},
		{"private-answer", "direct", "10.1.2.3", "a", "", 1},
		{"loopback-answer", "direct", "127.0.0.1", "a", "", 1},
		{"metadata-answer", "direct", "169.254.169.254", "a", "", 1},
		{"local-answer", "direct", "198.51.100.9", "a", "", 1},
		{"whole-client", "whole", "203.0.113.8", "a", "selected", 1},
		{"whole-client-disabled", "whole-disabled", "203.0.113.8", "a", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := controlledDenialDNS(t, tc.answer)
			events := make(chan string, 16)
			direct := denialEchoSink(t, "direct", events)
			selected := denialEchoSink(t, "selected", events)
			snap, live := realitySniffFixture()
			snap.Settings.DNSType = "udp"
			snap.Settings.DNSUpstream = upstream.address
			blocked := snap.Rules[0]
			blocked.Action = store.ActionBlock
			blocked.Domains = []string{"fixture.example"}
			blocked.Priority = 2
			host := tc.name + ".fixture.example"
			var plain PlainLists
			switch tc.kind {
			case "disabled":
				blocked.Action = store.ActionTunnel
				snap.Tunnels[0].Enabled = false
			case "empty":
				blocked.Action = store.ActionTunnel
				snap.Tunnels[0].Source = store.SourcePool
				snap.Tunnels[0].Pool = nil
			case "plain":
				blocked.Domains = nil
				blocked.RemoteLists = []string{"https://fixture.example/plain"}
				plain = func(string) (PlainList, bool) { return PlainList{Domains: []string{"fixture.example"}}, true }
			case "native", "native-or-deny", "native-positive":
				dir := t.TempDir()
				path := filepath.Join(dir, "domain.json")
				body := []byte(`{"version":3,"rules":[{"domain_suffix":["fixture.example"]}]}`)
				if tc.kind != "native" {
					body = []byte(`{"version":3,"rules":[{"type":"logical","mode":"or","rules":[{"domain_suffix":["fixture.example"]},{"ip_cidr":["203.0.113.0/24"],"network":"tcp","port":8443}]}]}`)
					if tc.kind == "native-positive" {
						body = []byte(strings.ReplaceAll(string(body), "fixture.example", "unrelated.example"))
					}
				}
				projection, e := lists.ProjectNative(body, lists.FormatJSON)
				if e != nil {
					t.Fatal(e)
				}
				dnsPath := filepath.Join(dir, "dns.json")
				if e := os.WriteFile(dnsPath, projection.DNS, 0o600); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(path, body, 0o600); e != nil {
					t.Fatal(e)
				}
				blocked.Domains = nil
				blocked.RemoteLists = []string{"https://fixture.example/native.json"}
				plain = func(string) (PlainList, bool) {
					return PlainList{Native: &lists.NativeArtifact{Version: lists.ProjectionVersion, Format: lists.FormatJSON, Path: path, DNSPath: dnsPath}, Subnets: projection.Subnets, TransportSubnets: projection.TransportSubnets, NativeSummaryComplete: true}, true
				}
			}
			snap.Rules = []store.Rule{blocked}
			if tc.kind == "native-positive" {
				positive := blocked
				positive.ID = "native-positive"
				positive.Action = store.ActionDirect
				positive.Priority = 1
				blocked.RemoteLists = nil
				blocked.Domains = []string{"fixture.example"}
				snap.Rules = []store.Rule{positive, blocked}
			}
			if tc.kind == "direct" || tc.kind == "tunnel" || tc.kind == "cidr" || tc.kind == "cidr-deny" || tc.kind == "later-allow" {
				earlier := blocked
				earlier.ID = "earlier"
				earlier.Priority = 1
				earlier.Action = store.ActionDirect
				earlier.Domains = []string{host}
				if tc.name == "earlier-all-direct" {
					earlier.PeerScope = store.ScopeAll
					earlier.PeerIDs = nil
				}
				if tc.kind == "tunnel" {
					earlier.Action = store.ActionTunnel
				}
				if tc.kind == "cidr" || tc.kind == "cidr-deny" {
					earlier.Domains = nil
					earlier.Subnets = []string{"203.0.113.0/24"}
				}
				if tc.kind == "cidr-deny" {
					earlier.Action = store.ActionBlock
				}
				if tc.kind == "later-allow" {
					earlier.Priority = 3
				}
				snap.Rules = append([]store.Rule{earlier}, snap.Rules...)
			}
			if tc.name == "other-user-no-exception" {
				snap.Rules[len(snap.Rules)-1].PeerScope = store.ScopeAll
			}
			if strings.HasPrefix(tc.kind, "whole") {
				exit := "exit"
				snap.Peers[0].EgressTunnelID = &exit
				if tc.kind == "whole-disabled" {
					snap.Tunnels[0].Enabled = false
				}
			}
			port, _ := denialRuntime(t, binaryPath, t.TempDir(), snap, live, plain, direct, selected)
			user := live.Users[0]
			if tc.user == "b" {
				user = live.Users[1]
			}
			// Prefixes retain suffix predicates while giving each protocol a cold key.
			for _, udp := range []bool{false, true} {
				flowHost := host
				if udp {
					flowHost = "udp." + host
				}
				denialPayload(t, port, user, flowHost, udp)
				select {
				case observed := <-events:
					if observed != tc.want {
						t.Fatalf("UDP=%v sink=%s want=%s", udp, observed, tc.want)
					}
				case <-time.After(30 * time.Millisecond):
					if tc.want != "" {
						t.Fatalf("UDP=%v missing sink %s", udp, tc.want)
					}
				}
				if got := upstream.count(flowHost); got != tc.queries {
					t.Fatalf("UDP=%v upstream questions=%d want=%d", udp, got, tc.queries)
				}
			}
			t.Logf("TCP+UDP cold questions=%d+%d; sink target=%q", upstream.count(host), upstream.count("udp."+host), tc.want)
		})
	}
}

func TestRealityDenialRetainedFakeIPPinnedRuntime(t *testing.T) {
	binaryPath := os.Getenv("RAZDACHA_TEST_SINGBOX")
	if binaryPath == "" {
		t.Skip("set RAZDACHA_TEST_SINGBOX for retained FakeIP proof")
	}
	upstream := controlledDenialDNS(t, "203.0.113.8")
	events := make(chan string, 8)
	direct := denialEchoSink(t, "direct", events)
	selected := denialEchoSink(t, "selected", events)
	snap, live := realitySniffFixture()
	snap.Settings.DNSType = "udp"
	snap.Settings.DNSUpstream = upstream.address
	dir := t.TempDir()
	port, stop := denialRuntime(t, binaryPath, dir, snap, live, nil, direct, selected)
	host := "selected.fixture.example"
	reply := denialGatewayQuery(t, port, live.Users[0], host, dns.TypeA, false)
	if reply == nil || len(reply.Answer) != 1 {
		t.Fatalf("FakeIP allocation response %+v", reply)
	}
	fake, ok := reply.Answer[0].(*dns.A)
	if !ok || !strings.HasPrefix(fake.A.String(), "198.18.") {
		t.Fatalf("expected FakeIP: %v", reply.Answer)
	}
	// Confirm the mapping survives a second captured query before publication.
	reply2 := denialGatewayQuery(t, port, live.Users[0], host, dns.TypeA, true)
	if reply2 == nil || len(reply2.Answer) != 1 || reply2.Answer[0].(*dns.A).A.String() != fake.A.String() {
		t.Fatal("FakeIP mapping changed before transition")
	}
	stop()
	snap.Rules[0].Action = store.ActionBlock
	port, _ = denialRuntime(t, binaryPath, dir, snap, live, nil, direct, selected)
	for _, udp := range []bool{false, true} {
		denialPayload(t, port, live.Users[0], fake.A.String(), udp)
	}
	if n := upstream.count(host); n != 0 {
		t.Fatalf("retained FakeIP disclosed %d questions", n)
	}
	select {
	case sink := <-events:
		t.Fatalf("retained FakeIP reached %s sink", sink)
	case <-time.After(50 * time.Millisecond):
	}
	t.Logf("retained %s -> %s across checked policy restart: TCP+UDP questions=0 sinks=0", fake.A, host)
}

func TestRealityDenialCapturedDNSPinnedRuntime(t *testing.T) {
	binaryPath := os.Getenv("RAZDACHA_TEST_SINGBOX")
	if binaryPath == "" {
		t.Skip("set RAZDACHA_TEST_SINGBOX for captured DNS controls")
	}
	upstream := controlledDenialDNS(t, "203.0.113.8")
	events := make(chan string, 8)
	direct := denialEchoSink(t, "direct", events)
	selected := denialEchoSink(t, "selected", events)
	snap, live := realitySniffFixture()
	snap.Settings.DNSType = "udp"
	snap.Settings.DNSUpstream = upstream.address
	blocked := snap.Rules[0]
	blocked.ID = "block"
	blocked.Domains = []string{"blocked.fixture.example"}
	blocked.Action = store.ActionBlock
	snap.Rules = append(snap.Rules, blocked)
	port, _ := denialRuntime(t, binaryPath, t.TempDir(), snap, live, nil, direct, selected)
	for _, udp := range []bool{false, true} {
		for _, qtype := range []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeHTTPS} {
			for _, kind := range []string{"allowed", "selected", "blocked"} {
				name := kind + ".fixture.example"
				if udp {
					name = "udp." + name
				}
				response := denialGatewayQuery(t, port, live.Users[0], name, qtype, udp)
				allowed := qtype == dns.TypeA && kind != "blocked"
				if allowed {
					if response == nil || response.Rcode != dns.RcodeSuccess || len(response.Answer) != 1 {
						t.Fatalf("UDP=%v name=%s type=%d response=%+v", udp, name, qtype, response)
					}
				} else if response != nil && response.Rcode == dns.RcodeSuccess && len(response.Answer) > 0 {
					t.Fatalf("rejected DNS answered: %+v", response)
				}
				want := 0
				if kind == "allowed" {
					want = 1
				}
				if got := upstream.count(name); got != want {
					t.Fatalf("UDP=%v name=%s type=%d upstream=%d want=%d", udp, name, qtype, got, want)
				}
			}
		}
	}
	select {
	case event := <-events:
		t.Fatalf("DNS reached payload sink %s", event)
	default:
	}
	t.Log("captured TCP/UDP DNS: ordinary A upstream=1; FakeIP A=0; denied/AAAA/HTTPS add zero; payload sinks=0")
}

func TestRealityDenialScalePinnedCheck(t *testing.T) {
	binaryPath := os.Getenv("RAZDACHA_TEST_SINGBOX")
	if binaryPath == "" {
		t.Skip("set RAZDACHA_TEST_SINGBOX for bounded scale proof")
	}
	snap, live := activityScaleFixture()
	for i := range snap.Rules {
		if i%2 == 1 {
			snap.Rules[i].Action = store.ActionBlock
		}
	}
	opts, err := GenerateWithReality(snap, nil, live)
	if err != nil {
		t.Fatal(err)
	}
	opts.Inbounds = nil
	data, err := Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "scale.json")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started := time.Now()
	if out, e := exec.CommandContext(ctx, binaryPath, "check", "-c", path).CombinedOutput(); e != nil {
		t.Fatalf("scale check %v: %s", e, out)
	}
	t.Logf("100 clients x 100 alternating positive/deny rules: %d routes, %d JSON bytes, check %s", len(opts.Route.Rules), len(data), time.Since(started))
}
