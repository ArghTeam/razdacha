package singbox

import (
	"context"
	"encoding/binary"
	"encoding/hex"
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

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/store"
	C "github.com/sagernet/sing-box/constant"
)

func TestRealityActivitySingletonRoutesPreserveSourceAndOrder(t *testing.T) {
	snap, live := realitySniffFixture()
	snap.Rules[0].PeerScope = store.ScopeAll
	snap.Rules[0].PeerIDs = nil
	opts, err := GenerateWithReality(snap, nil, live)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	fallback := map[string]int{}
	lastDestination := -1
	for i, r := range opts.Route.Rules {
		if r.Type != C.RuleTypeDefault {
			continue
		}
		d := r.DefaultOptions
		if len(d.RuleSet) > 0 {
			lastDestination = i
		}
		if len(d.Inbound) != 1 || d.Inbound[0] != TagRealityIn || d.Action != C.RuleActionTypeRoute {
			continue
		}
		if len(d.AuthUser) != 1 {
			t.Fatal("terminal REALITY route does not identify one authenticated user")
		}
		if len(d.RuleSet) > 0 {
			seen[d.AuthUser[0]]++
		} else if d.RouteOptions.Outbound == TagDirect {
			fallback[d.AuthUser[0]] = i
		}
	}
	for _, u := range live.Users {
		if seen[u.Name] != 1 || fallback[u.Name] <= lastDestination {
			t.Fatal("source or direct fallback changed policy order", seen, fallback)
		}
	}
}

// Real pinned VLESS authentication and its TCP/UDP trackers are exercised on
// loopback. Only the TLS wrapper and egress dial targets are fixture substitutes.
// The generated authenticated route policy and controller serialization are real.
func TestRealityActivityPinnedRuntime(t *testing.T) {
	runtimeBinary := os.Getenv("RAZDACHA_TEST_SINGBOX")
	if runtimeBinary == "" {
		t.Skip("set RAZDACHA_TEST_SINGBOX for pinned runtime observations")
	}
	version, err := exec.Command(runtimeBinary, "version").Output()
	if err != nil || !strings.Contains(string(version), "sing-box version 1.12.25\n") {
		t.Fatal("pinned runtime required", err)
	}
	events := make(chan string, 8)
	selected := activitySink(t, "selected", events)
	direct := activitySink(t, "direct", events)
	snap, live := realitySniffFixture()
	ids := []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}
	for i, id := range ids {
		snap.Peers[i].ID = id
		live.Users[i].PeerID = id
		live.Users[i].Name = store.RealityUserName(id)
	}
	snap.Rules[0].PeerIDs = []string{ids[0]}
	snap.Rules[0].Domains = nil
	snap.Rules[0].Subnets = []string{"203.0.113.8/32"}
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
	serverPort, controllerPort := sniffFreePort(t), sniffFreePort(t)
	users := []any{}
	for _, u := range live.Users {
		users = append(users, map[string]any{"name": u.Name, "uuid": u.UUID})
	}
	config["inbounds"] = []any{map[string]any{"type": "vless", "tag": TagRealityIn, "listen": "127.0.0.1", "listen_port": serverPort, "users": users}}
	config["outbounds"] = []any{
		map[string]any{"type": "direct", "tag": TunnelTag("exit")},
		map[string]any{"type": "direct", "tag": TagDirect},
	}
	// Fixture-only terminal dial overrides leave all matching predicates intact
	// and keep both TCP and UDP destinations on loopback.
	for _, item := range config["route"].(map[string]any)["rules"].([]any) {
		rule := item.(map[string]any)
		if _, authenticated := rule["auth_user"]; !authenticated {
			continue
		}
		if outbound, ok := rule["outbound"].(string); ok {
			rule["override_address"] = "127.0.0.1"
			if outbound == TunnelTag("exit") {
				rule["override_port"] = selected
			} else {
				rule["override_port"] = direct
			}
		}
	}
	config["experimental"] = map[string]any{"clash_api": map[string]any{"external_controller": net.JoinHostPort("127.0.0.1", strconv.Itoa(controllerPort))}}
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
	check := exec.Command(runtimeBinary, "check", "-c", path)
	if out, e := check.CombinedOutput(); e != nil {
		t.Fatalf("runtime check: %v %s", e, out)
	}
	process := exec.Command(runtimeBinary, "run", "-D", dir, "-c", path)
	process.Stdout = log
	process.Stderr = log
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(serverPort))
	until := time.Now().Add(5 * time.Second)
	for {
		conn, e := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if e == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(until) {
			out, _ := os.ReadFile(log.Name())
			t.Fatalf("runtime startup: %s", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
	connections := []net.Conn{}
	defer func() {
		for _, c := range connections {
			_ = c.Close()
		}
	}()
	for _, command := range []byte{1, 2} {
		for _, u := range live.Users {
			conn, e := net.DialTimeout("tcp", address, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			connections = append(connections, conn)
			raw, e := hex.DecodeString(strings.ReplaceAll(u.UUID, "-", ""))
			if e != nil {
				t.Fatal(e)
			}
			header := append([]byte{0}, raw...)
			header = append(header, 0, command, 1, 187, 1, 203, 0, 113, 8)
			payload := []byte(u.Name)
			if command == 2 {
				framed := make([]byte, 2)
				binary.BigEndian.PutUint16(framed, uint16(len(payload)))
				header = append(header, framed...)
			}
			_, e = conn.Write(append(header, payload...))
			if e != nil {
				t.Fatal(e)
			}
			if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			responseHeader := make([]byte, 2)
			if _, err := io.ReadFull(conn, responseHeader); err != nil {
				t.Fatal("missing VLESS response", err)
			}
			if responseHeader[0] != 0 || responseHeader[1] != 0 {
				t.Fatal("unexpected VLESS response header")
			}
			response := []byte("reply/" + u.Name)
			if command == 2 {
				length := make([]byte, 2)
				if _, err := io.ReadFull(conn, length); err != nil || int(binary.BigEndian.Uint16(length)) != len(response) {
					t.Fatal("unexpected VLESS UDP response length", err)
				}
			}
			actual := make([]byte, len(response))
			if _, err := io.ReadFull(conn, actual); err != nil || string(actual) != string(response) {
				t.Fatal("wrong fixture response", err)
			}
		}
	}
	wantEvents := map[string]bool{
		"selected/tcp/" + live.Users[0].Name: true, "selected/udp/" + live.Users[0].Name: true,
		"direct/tcp/" + live.Users[1].Name: true, "direct/udp/" + live.Users[1].Name: true,
	}
	for range 4 {
		select {
		case event := <-events:
			if !wantEvents[event] {
				t.Fatal("wrong source-scoped egress", event)
			}
			delete(wantEvents, event)
		case <-time.After(3 * time.Second):
			t.Fatal("missing egress event", wantEvents)
		}
	}
	client := clash.New(clash.Options{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(controllerPort))})
	until = time.Now().Add(3 * time.Second)
	for {
		counts, e := client.RealityConnections(context.Background())
		valid := e == nil
		for _, user := range live.Users {
			value := counts[user.Name]
			valid = valid && value.OpenFlows == 2 && value.OpenUploadBytes != nil && *value.OpenUploadBytes == int64(2*len(user.Name)) && value.OpenDownloadBytes != nil && *value.OpenDownloadBytes == int64(2*len("reply/"+user.Name))
		}
		if valid {
			break
		}
		if time.Now().After(until) {
			out, _ := os.ReadFile(log.Name())
			t.Fatalf("authenticated TCP/UDP attribution: counts=%v err=%v log=%s", counts, e, out)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The controller reports the current snapshot, not accumulated samples or
	// closed-flow history. Other authenticated users retain their own observation.
	_ = connections[0].Close()
	until = time.Now().Add(3 * time.Second)
	for {
		observations, err := client.RealityConnections(context.Background())
		first, second := observations[live.Users[0].Name], observations[live.Users[1].Name]
		if err == nil && first.OpenFlows == 1 && first.OpenUploadBytes != nil && *first.OpenUploadBytes == int64(len(live.Users[0].Name)) && first.OpenDownloadBytes != nil && *first.OpenDownloadBytes == int64(len("reply/"+live.Users[0].Name)) && second.OpenFlows == 2 {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("closed flow remained in current snapshot: %+v %v", observations, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func activitySink(t *testing.T, name string, events chan<- string) int {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := tcp.Addr().(*net.TCPAddr).Port
	udp, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tcp.Close(); _ = udp.Close() })
	go func() {
		for {
			conn, e := tcp.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				data := make([]byte, len("client-00000000-0000-4000-8000-000000000001"))
				if n, err := io.ReadFull(conn, data); err == nil {
					events <- name + "/tcp/" + string(data[:n])
					_, _ = conn.Write(append([]byte("reply/"), data[:n]...))
				}
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	go func() {
		data := make([]byte, 4096)
		for {
			n, address, e := udp.ReadFrom(data)
			if e != nil {
				return
			}
			events <- name + "/udp/" + string(data[:n])
			_, _ = udp.WriteTo(append([]byte("reply/"), data[:n]...), address)
		}
	}()
	return port
}

func activityScaleFixture() (store.Snapshot, *store.RealityRuntime) {
	snap, live := realitySniffFixture()
	peer := snap.Peers[0]
	user := live.Users[0]
	rule := snap.Rules[0]
	snap.Peers = nil
	live.Users = nil
	snap.Rules = nil
	for i := 0; i < 100; i++ {
		p := peer
		p.ID = fmt.Sprintf("%08x-0000-4000-8000-000000000001", i)
		p.Address = fmt.Sprintf("10.8.0.%d", i+2)
		snap.Peers = append(snap.Peers, p)
		u := user
		u.PeerID = p.ID
		u.Name = store.RealityUserName(p.ID)
		u.UUID = p.ID
		live.Users = append(live.Users, u)
		r := rule
		r.ID = fmt.Sprintf("rule-%d", i)
		r.PeerScope = store.ScopeAll
		r.PeerIDs = nil
		r.Domains = []string{fmt.Sprintf("site%d.fixture.example", i)}
		snap.Rules = append(snap.Rules, r)
	}
	return snap, live
}

func BenchmarkRealityActivityHundredClientsHundredRules(b *testing.B) {
	snap, live := activityScaleFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		opts, err := GenerateWithReality(snap, nil, live)
		if err != nil {
			b.Fatal(err)
		}
		data, err := Marshal(opts)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(len(data)), "config-bytes")
		b.ReportMetric(float64(len(opts.Route.Rules)), "route-rules")
	}
}

func TestRealityActivityScalePinnedCheck(t *testing.T) {
	runtimeBinary := os.Getenv("RAZDACHA_TEST_SINGBOX")
	if runtimeBinary == "" {
		t.Skip("set RAZDACHA_TEST_SINGBOX for scale check")
	}
	snap, live := activityScaleFixture()
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
	users := []any{}
	for _, u := range live.Users {
		users = append(users, map[string]any{"name": u.Name, "uuid": u.UUID})
	}
	config["inbounds"] = []any{map[string]any{"type": "vless", "tag": TagRealityIn, "listen": "127.0.0.1", "listen_port": sniffFreePort(t), "users": users}}
	data, err = json.Marshal(config)
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
	if out, e := exec.CommandContext(ctx, runtimeBinary, "check", "-c", path).CombinedOutput(); e != nil {
		t.Fatalf("scale check %v: %s", e, out)
	}
	t.Logf("100 clients x 100 rules: %d route rules; %d compact config bytes; runtime check %s", len(opts.Route.Rules), len(data), time.Since(started))
}
