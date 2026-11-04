//go:build linux

package dnsruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/miekg/dns"
	"github.com/vishvananda/netlink"
)

// This fixture starts the exact product compiler output in the pinned executable.
// It requires a fresh network namespace and contacts only synthetic owned peers.
func TestRealityGeneratedProduct(t *testing.T) {
	if os.Getenv("RAZDACHA_REALITY_PRODUCT_TEST") != "1" {
		t.Skip("isolated Linux namespace fixture is opt-in")
	}
	if os.Getenv("RAZDACHA_REALITY_PARENT_NETNS") == "" {
		t.Fatal("fresh namespace parent identity is required")
	}
	realityNamespace(t)
	lo, err := netlink.LinkByName("lo")
	realityMust(t, err)
	for _, cidr := range []string{"10.8.0.1/32", "198.51.100.2/32", "198.51.100.3/32"} {
		a, e := netlink.ParseAddr(cidr)
		realityMust(t, e)
		realityMust(t, netlink.AddrAdd(lo, a))
	}
	binary := os.Getenv("RAZDACHA_TEST_SINGBOX")
	if !filepath.IsAbs(binary) {
		t.Fatal("absolute pinned executable required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	version, err := exec.CommandContext(ctx, binary, "version").Output()
	cancel()
	if err != nil || !strings.Contains(string(version), "sing-box version 1.12.25\n") {
		t.Fatal("pinned sing-box 1.12.25 required")
	}
	target := realityHandshakeTarget(t)
	sink := productSink(t)
	upstream := productResolver(t)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	realityMust(t, err)
	t.Cleanup(func() { _ = st.Close() })
	settings := store.DefaultSettings()
	settings.RealityEnabled = true
	settings.RealityListenPort = realityPort(t)
	settings.RealityServerName = "fixture.example"
	settings.RealityHandshakeServer = "127.0.0.1"
	settings.RealityHandshakePort = target
	settings.DNSUpstream = upstream
	settings.EndpointHost = "gateway.fixture.example"
	realityMust(t, st.SaveSettings(context.Background(), settings))
	peers := make([]store.Peer, 2)
	for i := range peers {
		peers[i], err = st.CreatePeer(context.Background(), store.Peer{Name: fmt.Sprintf("Client %d", i), PublicKey: fmt.Sprintf("public-%d", i), PrivateKey: "synthetic-private", PresharedKey: "synthetic-psk", Address: fmt.Sprintf("10.8.0.%d", i+2), Enabled: true, RealityEnabled: true})
		realityMust(t, err)
	}
	nativeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":3,"rules":[{"domain_suffix":["native.fixture.example"],"ip_cidr":["198.51.100.3/32"]}]}`))
	}))
	defer nativeServer.Close()
	source := nativeServer.URL + "/native.json"
	fetcher, e := lists.NewFetcher(lists.Options{Dir: t.TempDir(), Client: nativeServer.Client()})
	realityMust(t, e)
	artifact, e := fetcher.Parse(context.Background(), source)
	realityMust(t, e)
	if artifact.Native == nil {
		t.Fatal("native fixture did not load")
	}
	plain := func(string) (singbox.PlainList, bool) {
		return singbox.PlainList{Native: artifact.Native, Subnets: artifact.Subnets}, true
	}
	snap, err := st.Snapshot(context.Background())
	realityMust(t, err)
	snap.Rules = []store.Rule{
		{ID: "selected", Name: "Selected native", Enabled: true, Action: store.ActionBlock, PeerScope: store.ScopeSelected, PeerIDs: []string{peers[0].ID}, RemoteLists: []string{source}},
		{ID: "all", Name: "Shared", Enabled: true, Action: store.ActionBlock, PeerScope: store.ScopeAll, Domains: []string{"everyone.fixture.example"}},
	}
	live, err := st.RealityRuntime(context.Background(), snap)
	realityMust(t, err)
	start := func(policy store.Snapshot, live *store.RealityRuntime) *realityProcess {
		opts, e := singbox.GenerateWithReality(policy, plain, live)
		realityMust(t, e)
		// Cache/controller files are fixture-owned; all generated ingress, DNS and
		// routing/outbound rules remain unchanged.
		opts.Experimental = nil
		data, e := singbox.Marshal(opts)
		realityMust(t, e)
		var config map[string]any
		realityMust(t, json.Unmarshal(data, &config))
		return realityStart(t, binary, config, settings.RealityListenPort)
	}
	// Check the delivered complete client profile with the same pinned engine.
	profile, e := singbox.RealityClientProfile(settings, live.Users[0].UUID, live.Identity.PublicKey, live.Identity.ShortID)
	realityMust(t, e)
	profilePath := filepath.Join(t.TempDir(), "client.json")
	realityMust(t, os.WriteFile(profilePath, profile, 0600))
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 5*time.Second)
	checkErr := exec.CommandContext(checkCtx, binary, "check", "-c", profilePath).Run()
	checkCancel()
	if checkErr != nil {
		t.Fatal("pinned runtime rejected delivered client profile")
	}
	server := start(snap, live)
	initialUsers := map[string]store.RealityUser{}
	for _, u := range live.Users {
		initialUsers[u.PeerID] = u
	}
	client := func(index int, host string, port int) [2]int {
		return productClient(t, binary, settings.RealityListenPort, host, port, initialUsers[peers[index].ID].UUID, live.Identity.PublicKey, live.Identity.ShortID)
	}
	for i := range peers {
		allowed := client(i, "198.51.100.2", sink.port)
		denied := client(i, "198.51.100.3", sink.port)
		nativeDomain := client(i, "native.fixture.example", sink.port)
		shared := client(i, "everyone.fixture.example", sink.port)
		private := client(i, "private.fixture.example", sink.port)
		mapped := client(i, "::ffff:127.0.0.1", sink.port)
		for _, network := range []string{"tcp", "udp"} {
			productExpect(t, sink, network, allowed[0], true)
			productExpect(t, sink, network, denied[0], i == 1)
			productExpect(t, sink, network, nativeDomain[0], i == 1)
			productExpect(t, sink, network, shared[0], false)
			productExpect(t, sink, network, private[0], false)
			productExpect(t, sink, network, mapped[0], false)
			for _, name := range []string{"allowed.fixture.example", "native.fixture.example", "everyone.fixture.example"} {
				query := new(dns.Msg)
				query.SetQuestion(name+".", dns.TypeA)
				response, _, e := (&dns.Client{Net: network, Timeout: 2 * time.Second}).Exchange(query, fmt.Sprintf("127.0.0.1:%d", allowed[1]))
				want := name == "allowed.fixture.example" || (i == 1 && name == "native.fixture.example")
				accepted := e == nil && response.Rcode == dns.RcodeSuccess && len(response.Answer) > 0
				if accepted != want {
					t.Fatalf("client %d %s DNS %s accepted=%v want=%v", i, network, name, accepted, want)
				}
			}
		}
	}
	// A checked restart with the live intersection removes both existing sessions
	// and historical credentials. The replacement remains unadmitted in this snap.
	connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", client(0, "198.51.100.2", sink.port)[0]), time.Second)
	realityMust(t, err)
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = connection.Write([]byte("established\n"))
	realityMust(t, err)
	b := make([]byte, 128)
	_, err = connection.Read(b)
	realityMust(t, err)
	old := initialUsers[peers[0].ID]
	_, err = st.RotateReality(context.Background(), old.PeerID, peers[0].RealityRevision)
	realityMust(t, err)
	server.stop(t)
	_, err = connection.Read(b)
	if err == nil {
		t.Fatal("restart retained an established flow")
	}
	_ = connection.Close()
	live, err = st.RealityRuntime(context.Background(), snap)
	realityMust(t, err)
	server = start(snap, live)
	stale := productClient(t, binary, settings.RealityListenPort, "198.51.100.2", sink.port, old.UUID, live.Identity.PublicKey, live.Identity.ShortID)
	for _, network := range []string{"tcp", "udp"} {
		productExpect(t, sink, network, stale[0], false)
	}
	server.stop(t)
	// Healthy whole-client routing keeps DNS on that exit, overrides shared
	// blocks and resolves a cached FakeIP under the new authenticated user.
	proxyPort := realityPort(t)
	remoteDNS := productResolverAnswer(t, "198.51.100.3")
	_, upstreamPort, _ := net.SplitHostPort(upstream)
	_, remotePort, _ := net.SplitHostPort(remoteDNS)
	upstreamNumber, _ := strconv.Atoi(upstreamPort)
	remoteNumber, _ := strconv.Atoi(remotePort)
	realityStart(t, binary, map[string]any{
		"inbounds":  []any{map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": proxyPort}},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "exit"}},
		"route":     map[string]any{"final": "exit", "rules": []any{map[string]any{"port": []int{upstreamNumber}, "action": "route", "outbound": "exit", "override_address": "127.0.0.1", "override_port": remoteNumber}}},
	}, proxyPort)
	targetID := "healthy"
	snap, err = st.Snapshot(context.Background())
	realityMust(t, err)
	snap.Peers[1].EgressTunnelID = &targetID
	// Snapshot peers are sorted by creation/id, so select explicitly by stable ID.
	for i := range snap.Peers {
		if snap.Peers[i].ID == peers[1].ID {
			snap.Peers[i].EgressTunnelID = &targetID
		} else {
			snap.Peers[i].EgressTunnelID = nil
		}
	}
	snap.Tunnels = []store.Tunnel{{ID: targetID, Name: "Owned exit", Type: store.TunnelSOCKS, Source: store.SourceURL, Raw: "synthetic", Enabled: true, Parsed: []byte(fmt.Sprintf(`{"server":"127.0.0.1","server_port":%d,"version":"5"}`, proxyPort))}}
	snap.Rules = []store.Rule{{ID: "fake", Name: "Selected FakeIP", Enabled: true, Action: store.ActionTunnel, TunnelID: targetID, PeerScope: store.ScopeSelected, PeerIDs: []string{peers[0].ID}, Domains: []string{"fake.fixture.example"}}, {ID: "shared", Name: "Shared block", Enabled: true, Action: store.ActionBlock, PeerScope: store.ScopeAll, Domains: []string{"everyone.fixture.example"}}}
	live, err = st.RealityRuntime(context.Background(), snap)
	realityMust(t, err)
	server = start(snap, live)
	byPeer := map[string]store.RealityUser{}
	for _, u := range live.Users {
		byPeer[u.PeerID] = u
	}
	whole := byPeer[peers[1].ID]
	selected := byPeer[peers[0].ID]
	wholeClient := productClient(t, binary, settings.RealityListenPort, "everyone.fixture.example", sink.port, whole.UUID, live.Identity.PublicKey, live.Identity.ShortID)
	selectedClient := productClient(t, binary, settings.RealityListenPort, "198.51.100.2", sink.port, selected.UUID, live.Identity.PublicKey, live.Identity.ShortID)
	queryDNS := func(port int, name string) *dns.Msg {
		q := new(dns.Msg)
		q.SetQuestion(name+".", dns.TypeA)
		r, _, e := (&dns.Client{Timeout: 3 * time.Second}).Exchange(q, fmt.Sprintf("127.0.0.1:%d", port))
		realityMust(t, e)
		if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
			t.Fatal("expected successful DNS")
		}
		return r
	}
	answer := queryDNS(wholeClient[1], "everyone.fixture.example").Answer[0].(*dns.A).A.String()
	if answer != "198.51.100.3" {
		t.Fatal("whole-client DNS escaped its owned exit", answer)
	}
	fake := queryDNS(selectedClient[1], "fake.fixture.example").Answer[0].(*dns.A).A
	if !strings.HasPrefix(fake.String(), "198.18.") && !strings.HasPrefix(fake.String(), "198.19.") {
		t.Fatal("selected tunnel did not produce FakeIP", fake)
	}
	cached := productClient(t, binary, settings.RealityListenPort, fake.String(), sink.port, whole.UUID, live.Identity.PublicKey, live.Identity.ShortID)
	privateWhole := productClient(t, binary, settings.RealityListenPort, "private.fixture.example", sink.port, whole.UUID, live.Identity.PublicKey, live.Identity.ShortID)
	for _, network := range []string{"tcp", "udp"} {
		productExpect(t, sink, network, wholeClient[0], true)
		productExpect(t, sink, network, cached[0], true)
		productExpect(t, sink, network, privateWhole[0], false)
	}
	server.stop(t)
	// An unavailable whole-client exit rejects every destination despite an all
	// clients direct rule. It also rejects DNS under the same authenticated user.
	targetID = "unavailable"
	for i := range snap.Peers {
		if snap.Peers[i].ID == peers[1].ID {
			snap.Peers[i].EgressTunnelID = &targetID
		} else {
			snap.Peers[i].EgressTunnelID = nil
		}
	}
	snap.Tunnels = []store.Tunnel{{ID: targetID, Name: "Unavailable", Type: store.TunnelSOCKS, Source: store.SourceURL, Raw: "synthetic", Enabled: false, Parsed: []byte(`{"server":"127.0.0.1","server_port":19080,"version":"5"}`)}}
	snap.Rules = []store.Rule{{ID: "direct", Name: "Shared direct", Enabled: true, Action: store.ActionDirect, PeerScope: store.ScopeAll, Subnets: []string{"0.0.0.0/0"}}}
	server = start(snap, live)
	unavailable := productClient(t, binary, settings.RealityListenPort, "198.51.100.2", sink.port, whole.UUID, live.Identity.PublicKey, live.Identity.ShortID)
	for _, network := range []string{"tcp", "udp"} {
		productExpect(t, sink, network, unavailable[0], false)
	}
}

func productClient(t *testing.T, binary string, serverPort int, host string, port int, credential, key, short string) [2]int {
	t.Helper()
	ports := [2]int{realityPort(t), realityPort(t)}
	cfg := map[string]any{"inbounds": []any{map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": ports[0], "override_address": host, "override_port": port}, map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": ports[1], "override_address": "10.8.0.1", "override_port": 53}}, "outbounds": []any{map[string]any{"type": "vless", "tag": "proxy", "server": "127.0.0.1", "server_port": serverPort, "uuid": credential, "tls": map[string]any{"enabled": true, "server_name": "fixture.example", "utls": map[string]any{"enabled": true, "fingerprint": "chrome"}, "reality": map[string]any{"enabled": true, "public_key": key, "short_id": short}}}}, "route": map[string]any{"final": "proxy"}}
	realityStart(t, binary, cfg, ports[0])
	return ports
}
func productExpect(t *testing.T, sink *realitySink, network string, port int, want bool) {
	t.Helper()
	payload := fmt.Sprintf("product-%d", time.Now().UnixNano())
	got, err := realityExchange(network, port, []byte(payload))
	if (err == nil && string(got) == "public:"+payload) != want {
		t.Fatalf("%s product delivery accepted=%v want=%v error=%v", network, err == nil, want, err)
	}
	if !want && sink.seen(payload) {
		t.Fatal("rejected delivery reached a sink")
	}
}
func productSink(t *testing.T) *realitySink {
	t.Helper()
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	realityMust(t, err)
	sink := &realitySink{name: "public", port: listener.Addr().(*net.TCPAddr).Port, payloads: map[string]bool{}}
	udp, err := net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", sink.port))
	realityMust(t, err)
	t.Cleanup(func() { _ = listener.Close(); _ = udp.Close() })
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(90 * time.Second))
				b := make([]byte, 4096)
				for {
					n, e := c.Read(b)
					if e != nil {
						return
					}
					_, _ = c.Write(sink.response([]byte(strings.TrimSuffix(string(b[:n]), "\n"))))
				}
			}()
		}
	}()
	go func() {
		b := make([]byte, 4096)
		for {
			n, a, e := udp.ReadFrom(b)
			if e != nil {
				return
			}
			_, _ = udp.WriteTo(sink.response(b[:n]), a)
		}
	}()
	return sink
}
func productResolver(t *testing.T) string { return productResolverAnswer(t, "198.51.100.2") }
func productResolverAnswer(t *testing.T, public string) string {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	realityMust(t, err)
	server := &dns.Server{PacketConn: conn, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		r := new(dns.Msg)
		r.SetReply(q)
		answer := public
		if strings.HasPrefix(q.Question[0].Name, "native.") {
			answer = "198.51.100.3"
		}
		if strings.HasPrefix(q.Question[0].Name, "private.") {
			answer = "127.0.0.1"
		}
		if q.Question[0].Qtype == dns.TypeA {
			r.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}, A: net.ParseIP(answer)}}
		}
		_ = w.WriteMsg(r)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown(); _ = conn.Close() })
	return conn.LocalAddr().String()
}
