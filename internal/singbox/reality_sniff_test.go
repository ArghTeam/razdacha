package singbox

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
	C "github.com/sagernet/sing-box/constant"
	"golang.org/x/net/proxy"
)

func realitySniffFixture() (store.Snapshot, *store.RealityRuntime) {
	snap := peerExitSnapshot()
	snap.Peers[0].EgressTunnelID = nil
	snap.Peers[0].RealityEnabled = true
	snap.Peers[1].RealityEnabled = true
	snap.Settings.RealityEnabled = true
	snap.Settings.EndpointHost = "gateway.fixture.example"
	snap.Settings.RealityListenPort = 18443
	snap.RealityIdentityID = "fixture"
	snap.Rules = []store.Rule{{ID: "selected", Name: "Selected name", Enabled: true, Action: store.ActionTunnel, TunnelID: "exit", PeerScope: store.ScopeSelected, PeerIDs: []string{"a"}, Domains: []string{"selected.fixture.example"}}}
	return snap, &store.RealityRuntime{Identity: &store.RealityIdentity{ID: "fixture", PrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ShortID: "0011223344556677"}, LocalAddresses: []string{"198.51.100.9/32"}, Users: []store.RealityUser{{PeerID: "a", Name: store.RealityUserName("a"), UUID: "00000000-0000-4000-8000-000000000001"}, {PeerID: "b", Name: store.RealityUserName("b"), UUID: "00000000-0000-4000-8000-000000000002"}}}
}

func TestRealitySniffRetainsGuardsScopeAndExitPrecedence(t *testing.T) {
	snap, live := realitySniffFixture()
	exit := "exit"
	snap.Peers[1].EgressTunnelID = &exit
	opts, err := GenerateWithReality(snap, nil, live)
	if err != nil {
		t.Fatal(err)
	}
	sniff, firstSet, wholeExit := -1, -1, -1
	for i, r := range opts.Route.Rules {
		if r.Type != C.RuleTypeDefault {
			continue
		}
		d := r.DefaultOptions
		if d.Action == C.RuleActionTypeSniff {
			if sniff >= 0 {
				t.Fatal("duplicate sniff action")
			}
			sniff = i
			if strings.Join(d.Inbound, ",") != TagRealityIn || d.IPVersion != 4 || strings.Join(d.Network, ",") != "tcp" || strings.Join(d.SniffOptions.Sniffer, ",") != "tls,http" || time.Duration(d.SniffOptions.Timeout) != 300*time.Millisecond {
				t.Fatalf("sniff scope or resource bound changed: %+v", d)
			}
		}
		if len(d.RuleSet) > 0 && firstSet < 0 {
			firstSet = i
		}
		if strings.Join(d.AuthUser, ",") == store.RealityUserName("b") && d.Action == C.RuleActionTypeRoute && len(d.RuleSet) == 0 {
			wholeExit = i
		}
	}
	if sniff < 7 || firstSet <= sniff || wholeExit < 0 || wholeExit >= sniff {
		t.Fatalf("incorrect rule ordering: exit=%d sniff=%d set=%d", wholeExit, sniff, firstSet)
	}
	before := opts.Route.Rules[sniff-1].DefaultOptions
	after := opts.Route.Rules[sniff+1].DefaultOptions
	if before.Action != C.RuleActionTypeReject || strings.Join(before.IPCIDR, ",") != strings.Join(append([]string{"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4"}, live.LocalAddresses...), ",") {
		t.Fatal("private destination guard no longer precedes sniff")
	}
	if after.Action != C.RuleActionTypeReject || strings.Join(after.Domain, ",") != snap.Settings.EndpointHost {
		t.Fatal("recovered gateway name is not rejected")
	}
	if opts.Route.Rules[0].LogicalOptions.Action != C.RuleActionTypeReject || opts.Route.Rules[1].DefaultOptions.IPVersion != 6 || opts.Route.Rules[2].DefaultOptions.Port[0] != 853 || opts.Route.Rules[3].DefaultOptions.Action != C.RuleActionTypeHijackDNS {
		t.Fatal("initial admission/DNS/IPv6 guards displaced")
	}
	data, err := Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "override_destination") {
		t.Fatal("sniff must never retarget the original IP")
	}
	snap.Rules = nil
	opts, err = GenerateWithReality(snap, nil, live)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range opts.Route.Rules {
		if r.DefaultOptions.Action == C.RuleActionTypeSniff {
			t.Fatal("no destination policy should not delay connections")
		}
	}
}

// This fixture retains the generated route and user policy but substitutes a
// loopback authenticated SOCKS inbound for REALITY and two loopback SOCKS sinks
// for egress. It never dials the synthetic destination or changes host routes.
func TestRealityNumericTCPNamesPinnedRuntime(t *testing.T) {
	binary := os.Getenv("RAZDACHA_TEST_SINGBOX")
	if binary == "" {
		t.Skip("set RAZDACHA_TEST_SINGBOX for pinned-runtime flow checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, binary, "version").Output()
	if err != nil || !strings.Contains(string(version), "sing-box version 1.12.25\n") {
		t.Fatal("pinned sing-box 1.12.25 required")
	}
	observations := make(chan sniffObservation, 16)
	selectedPort := sniffSink(t, "selected", observations)
	directPort := sniffSink(t, "direct", observations)
	snap, live := realitySniffFixture()
	snap.Tunnels[0].Parsed = json.RawMessage(fmt.Sprintf(`{"server":"127.0.0.1","server_port":%d,"version":"5"}`, selectedPort))
	opts, err := GenerateWithReality(snap, nil, live)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	port := sniffFreePort(t)
	config["inbounds"] = []any{map[string]any{"type": "socks", "tag": TagRealityIn, "listen": "127.0.0.1", "listen_port": port, "users": []any{map[string]any{"username": store.RealityUserName("a"), "password": "fixture"}, map[string]any{"username": store.RealityUserName("b"), "password": "fixture"}}}}
	for _, item := range config["outbounds"].([]any) {
		out := item.(map[string]any)
		if out["tag"] == TagDirect {
			for key := range out {
				delete(out, key)
			}
			out["type"] = "socks"
			out["tag"] = TagDirect
			out["server"] = "127.0.0.1"
			out["server_port"] = directPort
			out["version"] = "5"
		}
	}
	config["experimental"] = nil
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "runtime.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	process := exec.Command(binary, "run", "-D", dir, "-c", path)
	process.Stdout = log
	process.Stderr = log
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, e := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if e == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			body, _ := os.ReadFile(log.Name())
			t.Fatalf("runtime did not start: %s", body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, tc := range []struct{ name, user, host, payload, want string }{
		{"tls-selected", "a", "selected.fixture.example", "tls", "selected"},
		{"fragmented-tls-selected", "a", "selected.fixture.example", "fragmented-tls", "selected"},
		{"http-selected", "a", "selected.fixture.example", "http", "selected"},
		{"unselected-client", "b", "selected.fixture.example", "tls", "direct"},
		{"unselected-name", "a", "other.fixture.example", "tls", "direct"},
		{"no-sni", "a", "", "tls", "direct"},
		{"opaque", "a", "", "opaque", "direct"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialer, e := proxy.SOCKS5("tcp", address, &proxy.Auth{User: store.RealityUserName(tc.user), Password: "fixture"}, &net.Dialer{Timeout: 2 * time.Second})
			if e != nil {
				t.Fatal(e)
			}
			conn, e := dialer.Dial("tcp", "203.0.113.8:443")
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			switch tc.payload {
			case "tls", "fragmented-tls":
				if tc.payload == "fragmented-tls" {
					conn = &sniffFragmentConn{Conn: conn}
				}
				tlsConn := tls.Client(conn, &tls.Config{ServerName: tc.host, InsecureSkipVerify: true})
				_ = tlsConn.HandshakeContext(context.Background()) // The sink intentionally has no TLS server.
			case "http":
				_, e = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", tc.host)
				if e != nil {
					t.Fatal(e)
				}
				_, _ = io.ReadAll(conn)
			default:
				_, e = conn.Write([]byte("opaque fixture data"))
				if e != nil {
					t.Fatal(e)
				}
				_, _ = io.ReadAll(conn)
			}
			select {
			case observed := <-observations:
				if observed.exit != tc.want || observed.destination != "203.0.113.8:443" {
					t.Fatalf("observed %+v, want exit=%s and original numeric destination", observed, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("no egress observation")
			}
		})
	}
	// A server-first/idle stream must be released after the fixed sniff deadline.
	t.Run("idle-deadline", func(t *testing.T) {
		dialer, e := proxy.SOCKS5("tcp", address, &proxy.Auth{User: store.RealityUserName("a"), Password: "fixture"}, &net.Dialer{Timeout: time.Second})
		if e != nil {
			t.Fatal(e)
		}
		started := time.Now()
		conn, e := dialer.Dial("tcp", "203.0.113.8:443")
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		select {
		case observed := <-observations:
			elapsed := time.Since(started)
			if observed.exit != "direct" || elapsed < 250*time.Millisecond || elapsed > 1500*time.Millisecond {
				t.Fatalf("idle fallback: %+v after %s", observed, elapsed)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("idle sniff did not release the connection")
		}
	})
	// Recovering the gateway name must not bypass its hostname rejection.
	t.Run("recovered-gateway-name", func(t *testing.T) {
		dialer, e := proxy.SOCKS5("tcp", address, &proxy.Auth{User: store.RealityUserName("a"), Password: "fixture"}, &net.Dialer{Timeout: time.Second})
		if e != nil {
			t.Fatal(e)
		}
		conn, e := dialer.Dial("tcp", "203.0.113.8:443")
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: gateway.fixture.example\r\n\r\n")
		_, _ = io.ReadAll(conn)
		select {
		case observed := <-observations:
			t.Fatalf("gateway name reached egress: %+v", observed)
		case <-time.After(100 * time.Millisecond):
		}
	})
	// Numeric protected destinations must never reach either egress sink.
	dialer, err := proxy.SOCKS5("tcp", address, &proxy.Auth{User: store.RealityUserName("a"), Password: "fixture"}, &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{"127.0.0.1:443", "198.51.100.9:443"} {
		conn, e := dialer.Dial("tcp", destination)
		if e == nil {
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: selected.fixture.example\r\n\r\n")
			_, _ = io.ReadAll(conn)
			_ = conn.Close()
		}
	}
	select {
	case observed := <-observations:
		t.Fatalf("protected destination reached egress: %+v", observed)
	case <-time.After(100 * time.Millisecond):
	}
}

type sniffObservation struct{ exit, destination string }

func sniffFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func sniffSink(t *testing.T, exit string, observations chan<- sniffObservation) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				var e error
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
				header := make([]byte, 2)
				if _, e = io.ReadFull(conn, header); e != nil {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, e = io.ReadFull(conn, methods); e != nil {
					return
				}
				if _, e = conn.Write([]byte{5, 0}); e != nil {
					return
				}
				request := make([]byte, 4)
				if _, e = io.ReadFull(conn, request); e != nil {
					return
				}
				var host string
				switch request[3] {
				case 1:
					ip := make([]byte, 4)
					if _, e = io.ReadFull(conn, ip); e != nil {
						return
					}
					host = net.IP(ip).String()
				case 3:
					size := make([]byte, 1)
					if _, e = io.ReadFull(conn, size); e != nil {
						return
					}
					name := make([]byte, int(size[0]))
					if _, e = io.ReadFull(conn, name); e != nil {
						return
					}
					host = string(name)
				default:
					return
				}
				port := make([]byte, 2)
				if _, e = io.ReadFull(conn, port); e != nil {
					return
				}
				observations <- sniffObservation{exit, net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port))))}
				if _, e = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); e != nil {
					return
				}
				buffer := make([]byte, 16384)
				_, _ = conn.Read(buffer)
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// Separate delayed writes force ClientHello bytes to arrive over several reads.
type sniffFragmentConn struct{ net.Conn }

func (c *sniffFragmentConn) Write(data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		size := min(len(data), 64)
		n, err := c.Conn.Write(data[:size])
		total += n
		if err != nil {
			return total, err
		}
		if n != size {
			return total, io.ErrShortWrite
		}
		data = data[n:]
		if len(data) > 0 {
			time.Sleep(2 * time.Millisecond)
		}
	}
	return total, nil
}
