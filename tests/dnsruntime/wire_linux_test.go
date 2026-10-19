//go:build linux

package dnsruntime

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/singbox"
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
	cmd := exec.Command(os.Args[0], "-test.run=^TestDNSProjectionWire$", "-test.timeout=80s")
	cmd.Env = append(wireEnv("RAZDACHA_WIRE_ROLE", "enter"), "RAZDACHA_WIRE_NEXT="+role, "RAZDACHA_WIRE_ARGS="+strings.Join(args, "|"))
	fd, err := unix.Dup(int(ns))
	wireMust(t, err)
	file := os.NewFile(uintptr(fd), "wire-netns")
	t.Cleanup(func() { _ = file.Close() })
	cmd.ExtraFiles = []*os.File{file}
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
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
	case "sink":
		wireSink(t)
		return true
	case "probe":
		wireProbe(t)
		return true
	}
	return false
}

type wirePayloadDetector struct{ tail string }

func (d *wirePayloadDetector) feed(data []byte) bool {
	s := d.tail + string(data)
	bad := strings.Contains(s, "blocked-final-")
	if len(s) > 128 {
		s = s[len(s)-128:]
	}
	d.tail = s
	return bad
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
	go func() {
		buf := make([]byte, 65536)
		for {
			n, _, err := unix.Recvfrom(raw, buf, 0)
			if err != nil {
				return
			}
			if n < 34 || binary.BigEndian.Uint16(buf[12:14]) != unix.ETH_P_IP {
				continue
			}
			ip := buf[14:n]
			ihl := int(ip[0]&15) * 4
			if len(ip) < ihl+4 {
				continue
			}
			if ip[9] != unix.IPPROTO_TCP && ip[9] != unix.IPPROTO_UDP {
				continue
			}
			port := binary.BigEndian.Uint16(ip[ihl+2 : ihl+4])
			if port == 12080 && net.IP(ip[16:20]).String() == "203.0.113.20" {
				fmt.Println("CONTROL-PACKET")
			}

			if port == 53 {
				fmt.Println("FORBIDDEN-DIRECT-DNS-PACKET")
			}
			if port == 12081 {
				fmt.Println("FORBIDDEN-CIDR-PACKET")
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
	if strings.HasPrefix(args[0], "dns") {
		q := new(dns.Msg)
		q.SetQuestion(args[2], dns.TypeA)
		network := "udp"
		if strings.HasPrefix(args[0], "dnstcp") {
			network = "tcp"
		}
		answer, _, err := (&dns.Client{Net: network, Timeout: 400 * time.Millisecond}).Exchange(q, args[1])
		if err != nil {
			t.Fatalf("DNS transport failed: %v", err)
		}
		if answer == nil {
			t.Fatal("DNS returned no response")
		}
		if strings.HasSuffix(args[0], "reject") {
			if answer.Rcode != dns.RcodeRefused {
				t.Fatalf("expected DNS REFUSED, got rcode=%d answers=%d", answer.Rcode, len(answer.Answer))
			}
			return
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

func TestDNSProjectionWire(t *testing.T) {
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
		cmd := exec.Command(os.Args[0], "-test.run=^TestDNSProjectionWire$", "-test.timeout=80s")
		cmd.Env = append(wireEnv("RAZDACHA_WIRE_ROLE", "gateway"), "RAZDACHA_WIRE_PARENT="+parent, "RAZDACHA_WIRE_PARENT_MOUNT="+parentMount, "RAZDACHA_WIRE_BIN="+bin)
		cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWNET | unix.CLONE_NEWNS, Pdeathsig: syscall.SIGKILL}
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
	dnsWireExercise(t)
}

func dnsWireExercise(t *testing.T) {
	clientNS, sinkNS := wireTopology(t)
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	sink := wireExec(t, sinkNS, "sink")
	pipe, err := sink.StdoutPipe()
	wireMust(t, err)
	sink.Stderr = os.Stderr
	wireMust(t, sink.Start())
	defer func() { _ = sink.Process.Kill(); _ = sink.Wait() }()
	var mu sync.Mutex
	var observations []string
	ready := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "READY" {
				close(ready)
			}
			mu.Lock()
			observations = append(observations, line)
			mu.Unlock()
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	upstream, err := net.ListenPacket("udp4", "127.0.0.1:15353")
	wireMust(t, err)
	dnsServer := &dns.Server{PacketConn: upstream, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		r := new(dns.Msg)
		r.SetReply(q)
		rr, _ := dns.NewRR(q.Question[0].Name + " 0 IN A 203.0.113.20")
		r.Answer = []dns.RR{rr}
		_ = w.WriteMsg(r)
	})}
	go func() { _ = dnsServer.ActivateAndServe() }()
	defer dnsServer.Shutdown()
	q := new(dns.Msg)
	q.SetQuestion("upstream-control.example.", dns.TypeA)
	answer, _, err := (&dns.Client{Timeout: time.Second}).Exchange(q, upstream.LocalAddr().String())
	wireMust(t, err)
	if answer.Rcode != dns.RcodeSuccess || len(answer.Answer) != 1 {
		t.Fatal("controlled upstream precondition failed")
	}
	probe := func(proto, addr, nonce string, want bool) {
		t.Helper()
		cmd := wireExec(t, clientNS, "probe", proto, addr, nonce)
		out, err := cmd.CombinedOutput()
		if (err == nil) != want {
			t.Fatalf("wire assertion protocol=%s want_success=%t error=%v detail=%s", proto, want, err, out)
		}
	}
	for _, proto := range []string{"tcp4", "udp4"} {
		probe(proto, "203.0.113.20:12080", "positive-control", true)
	}
	wireMust(t, netstack.NewRoute().Apply())
	nft, err := netstack.NewNft()
	wireMust(t, err)
	nftConfig := func(subnets []string) netstack.NftConfig {
		return netstack.NftConfig{WGInterface: "wg0", WANInterface: "wan0", ResolverAddr: "10.8.0.1", Subnets: subnets}
	}
	settings := store.DefaultSettings()
	settings.DNSType = "udp"
	settings.DNSUpstream = "127.0.0.1:15353"
	baseline, err := singbox.Generate(store.Snapshot{Settings: settings}, nil)
	wireMust(t, err)
	baselineBytes, err := singbox.Marshal(baseline)
	wireMust(t, err)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	var process *exec.Cmd
	stop := func() {
		if process != nil {
			_ = process.Process.Kill()
			_ = process.Wait()
			process = nil
		}
	}
	defer stop()
	start := func(data []byte) {
		t.Helper()
		wireMust(t, os.WriteFile(configPath, data, 0o600))
		check := exec.CommandContext(ctx, os.Getenv("RAZDACHA_WIRE_BIN"), "check", "-c", configPath)
		if out, err := check.CombinedOutput(); err != nil {
			t.Fatalf("generated configuration check failed: %v %s", err, out)
		}
		stop()
		process = exec.CommandContext(ctx, os.Getenv("RAZDACHA_WIRE_BIN"), "run", "-c", configPath)
		process.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
		process.Stderr = os.Stderr
		process.Stdout = os.Stderr
		wireMust(t, process.Start())
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			c := http.Client{Timeout: 100 * time.Millisecond}
			resp, err := c.Get("http://127.0.0.1:9090/version")
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == 200 {
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("isolated runtime did not become ready")
	}
	start(baselineBytes)
	for _, source := range []string{"manual", "plain"} {
		for _, shape := range []string{"cidr", "mixed"} {
			for _, mode := range []string{"block", "unavailable"} {
				name := source + "-" + shape + "-" + mode
				t.Run(name, func(t *testing.T) {
					content := singbox.PlainList{Subnets: []string{"203.0.113.10/32"}}
					if shape == "mixed" {
						content.Domains = []string{"selected.example"}
					}
					policy := store.Rule{ID: "synthetic", Name: "Synthetic policy", Enabled: true, Action: store.ActionBlock, PeerScope: store.ScopeAll, Domains: content.Domains, Subnets: content.Subnets}
					var plain singbox.PlainLists
					if source == "plain" {
						policy.Domains = nil
						policy.Subnets = nil
						policy.RemoteLists = []string{"https://example.invalid/plain"}
						plain = func(string) (singbox.PlainList, bool) { return content, true }
					}
					var tunnels []store.Tunnel
					if mode == "unavailable" {
						policy.Action = store.ActionTunnel
						policy.TunnelID = "unavailable"
						policy.ResolveRealIP = true
						tunnels = []store.Tunnel{{ID: "unavailable", Name: "Synthetic unavailable", Enabled: false}}
					}
					opts, err := singbox.Generate(store.Snapshot{Settings: settings, Rules: []store.Rule{policy}, Tunnels: tunnels}, plain)
					wireMust(t, err)
					data, err := singbox.Marshal(opts)
					wireMust(t, err)
					start(data)
					_, err = nft.Apply(nftConfig(content.Subnets))
					wireMust(t, err)
					// Both TCP and UDP port-53 requests are intercepted into the real runtime.
					for _, proto := range []string{"dns", "dnstcp"} {
						probe(proto, "203.0.113.99:53", "unrelated.example.", true)
						if shape == "mixed" {
							probe(proto+"reject", "203.0.113.99:53", "selected.example.", true)
						}
					}
					for _, proto := range []string{"tcp4", "udp4"} {
						probe(proto, "203.0.113.10:12081", "blocked-final-"+name, false)
						probe(proto, "203.0.113.20:12080", "permitted-"+name, true)
					}
					// Invalid prospective bytes must not replace or stop the working runtime.
					invalid := filepath.Join(dir, "invalid.json")
					wireMust(t, os.WriteFile(invalid, []byte(`{"invalid":true}`), 0o600))
					if err := exec.CommandContext(ctx, os.Getenv("RAZDACHA_WIRE_BIN"), "check", "-c", invalid).Run(); err == nil {
						t.Fatal("invalid configuration unexpectedly validated")
					}
					probe("dns", "10.8.0.1:53", "unrelated-after-failed-check.example.", true)
					// Rehearse restoration of this fixture's prior checked bytes and nft state.
					start(baselineBytes)
					_, err = nft.Apply(nftConfig(nil))
					wireMust(t, err)
					for _, proto := range []string{"tcp4", "udp4"} {
						probe(proto, "203.0.113.10:12080", "restored-"+name, true)
					}
					// Restart the same checked policy and confirm domain/CIDR behavior survives.
					start(data)
					_, err = nft.Apply(nftConfig(content.Subnets))
					wireMust(t, err)
					probe("dns", "10.8.0.1:53", "unrelated-after-restart.example.", true)
					probe("udp4", "203.0.113.10:12081", "blocked-final-restart-"+name, false)
				})
			}
		}
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	controlObserved := false
	for _, line := range observations {
		if line == "CONTROL-PACKET" {
			controlObserved = true
		}
		if strings.HasPrefix(line, "FORBIDDEN-") {
			t.Fatalf("forbidden packet observed at disposable sink: %s", line)
		}
	}
	if !controlObserved {
		t.Fatal("packet observer did not capture positive control")
	}
	t.Log("Controlled DNS succeeds; selected domains reject; selected CIDR TCP/UDP packets absent at sink; prior config/nft restore and restart passed")
}
