//go:build linux

package dnsruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/miekg/dns"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// TestRealityCompleteClientWire runs the exact delivered TUN JSON inside an
// anonymous second network namespace. The caller must already isolate the parent
// in a fresh network/mount namespace; no operator routes or providers are used.
func TestRealityCompleteClientWire(t *testing.T) {
	switch os.Getenv("RAZDACHA_FULL_CLIENT_ROLE") {
	case "enter":
		runtime.LockOSThread()
		realityMust(t, unix.Setns(3, unix.CLONE_NEWNET))
		realityMust(t, unix.Exec(os.Args[0], os.Args, wireEnv("RAZDACHA_FULL_CLIENT_ROLE", "client")))
		return
	case "client":
		productCompleteClient(t)
		return
	}
	if os.Getenv("RAZDACHA_REALITY_FULL_CLIENT_TEST") != "1" {
		t.Skip("isolated complete-client fixture is opt-in")
	}
	if os.Getenv("RAZDACHA_REALITY_PARENT_NETNS") == "" {
		t.Fatal("fresh outer namespace identity is required")
	}
	realityNamespace(t)
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
	childNS := wireNewNS(t)
	veth := &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: "reality-srv"}, PeerName: "reality-cli", PeerNamespace: netlink.NsFd(childNS)}
	realityMust(t, netlink.LinkAdd(veth))
	t.Cleanup(func() { _ = netlink.LinkDel(veth) })
	outer, err := netlink.NewHandle()
	realityMust(t, err)
	defer outer.Close()
	child, err := netlink.NewHandleAt(childNS)
	realityMust(t, err)
	defer child.Close()
	wireLink(t, outer, "reality-srv", "198.51.100.1/24")
	wireLink(t, child, "lo", "127.0.0.1/8")
	wireLink(t, child, "reality-cli", "198.51.100.4/24")
	wireRoute(t, child, "", "198.51.100.1", "reality-cli")
	for _, entry := range []struct {
		h             *netlink.Handle
		name, address string
	}{
		{outer, "lo", "10.8.0.1/32"}, {outer, "lo", "198.51.101.2/32"}, {outer, "lo", "198.51.101.53/32"},
		{outer, "reality-srv", "2001:db8:c0de::1/64"}, {child, "reality-cli", "2001:db8:c0de::4/64"}, {outer, "lo", "2001:db8:fa::2/128"},
	} {
		l, e := entry.h.LinkByName(entry.name)
		realityMust(t, e)
		a, e := netlink.ParseAddr(entry.address)
		realityMust(t, e)
		a.Flags = unix.IFA_F_NODAD
		realityMust(t, entry.h.AddrAdd(l, a))
	}
	wireRoute(t, child, "::/0", "2001:db8:c0de::1", "reality-cli")
	http4 := productHTTP(t, "tcp4", "0.0.0.0:0")
	http6 := productHTTP(t, "tcp6", "[::]:0")
	directDNS := productBaselineDNS(t)
	defer directDNS.Shutdown()
	upstream := productResolverAnswer(t, "198.51.101.2")
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	realityMust(t, err)
	defer st.Close()
	settings := store.DefaultSettings()
	settings.RealityEnabled = true
	settings.RealityListenPort = realityPort(t)
	settings.RealityServerName = "fixture.example"
	settings.RealityHandshakeServer = "127.0.0.1"
	settings.RealityHandshakePort = realityHandshakeTarget(t)
	settings.EndpointHost = "198.51.100.1"
	settings.DNSUpstream = upstream
	realityMust(t, st.SaveSettings(context.Background(), settings))
	_, err = st.CreatePeer(context.Background(), store.Peer{Name: "Full client", PublicKey: "synthetic-public", PrivateKey: "synthetic-private", PresharedKey: "synthetic-psk", Address: "10.8.0.2", Enabled: true, RealityEnabled: true})
	realityMust(t, err)
	snap, err := st.Snapshot(context.Background())
	realityMust(t, err)
	live, err := st.RealityRuntime(context.Background(), snap)
	realityMust(t, err)
	options, err := singbox.GenerateWithReality(snap, nil, live)
	realityMust(t, err)
	options.Experimental = nil
	data, err := singbox.Marshal(options)
	realityMust(t, err)
	var config map[string]any
	realityMust(t, json.Unmarshal(data, &config))
	server := realityStart(t, binary, config, settings.RealityListenPort)
	profile, err := singbox.RealityClientProfile(settings, live.Users[0].UUID, live.Identity.PublicKey, live.Identity.ShortID)
	realityMust(t, err)
	profilePath := filepath.Join(t.TempDir(), "complete-client.json")
	realityMust(t, os.WriteFile(profilePath, profile, 0600))
	fd, err := unix.Dup(int(childNS))
	realityMust(t, err)
	nsFile := os.NewFile(uintptr(fd), "client-namespace")
	defer nsFile.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestRealityCompleteClientWire$", "-test.v", "-test.timeout=90s")
	command.Env = append(wireEnv("RAZDACHA_FULL_CLIENT_ROLE", "enter"), "RAZDACHA_FULL_CLIENT_PROFILE="+profilePath, "RAZDACHA_FULL_HTTP4="+http4, "RAZDACHA_FULL_HTTP6="+http6)
	command.ExtraFiles = []*os.File{nsFile}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	output, err := command.StdoutPipe()
	realityMust(t, err)
	input, err := command.StdinPipe()
	realityMust(t, err)
	diagnostic, err := os.OpenFile(filepath.Join(t.TempDir(), "child.log"), os.O_CREATE|os.O_WRONLY, 0600)
	realityMust(t, err)
	defer diagnostic.Close()
	command.Stderr = diagnostic
	realityMust(t, command.Start())
	t.Cleanup(func() { _ = unix.Kill(-command.Process.Pid, unix.SIGKILL) })
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "FULL_CLIENT_READY") {
				ready <- true
				_, _ = io.Copy(diagnostic, output)
				return
			}
			_, _ = fmt.Fprintln(diagnostic, line)
		}
		ready <- false
	}()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("complete client failed before readiness")
		}
	case err := <-done:
		t.Fatal("complete client exited before readiness", err)
	case <-time.After(45 * time.Second):
		t.Fatal("complete client readiness timeout")
	}
	server.stop(t)
	_, err = io.WriteString(input, "SERVER_STOPPED\n")
	realityMust(t, err)
	_ = input.Close()
	select {
	case err := <-done:
		realityMust(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("complete client did not finish no-fallback proof")
	}
}

func productCompleteClient(t *testing.T) {
	binary := os.Getenv("RAZDACHA_TEST_SINGBOX")
	profile := os.Getenv("RAZDACHA_FULL_CLIENT_PROFILE")
	current, err := os.Readlink("/proc/self/ns/net")
	realityMust(t, err)
	if current == os.Getenv("RAZDACHA_REALITY_PARENT_NETNS") {
		t.Fatal("client entered the operator namespace")
	}
	http4 := "http://198.51.101.2:" + os.Getenv("RAZDACHA_FULL_HTTP4")
	http6 := "http://[2001:db8:fa::2]:" + os.Getenv("RAZDACHA_FULL_HTTP6")
	if got, err := productHTTPGet(http4); err != nil || got != "198.51.100.4" {
		t.Fatal("physical IPv4 baseline failed", got, err)
	}
	if _, err := productHTTPGet(http6); err != nil {
		t.Fatal("physical IPv6 baseline failed", err)
	}
	before, err := productDNSQuery("before.fixture.example", dns.TypeA)
	if err != nil || before != "198.51.101.99" {
		t.Fatal("physical DNS baseline failed", before, err)
	}
	directory := t.TempDir()
	logs, err := os.OpenFile(filepath.Join(directory, "runtime.log"), os.O_CREATE|os.O_WRONLY, 0600)
	realityMust(t, err)
	defer logs.Close()
	command := exec.Command(binary, "run", "-D", directory, "-c", profile)
	command.Stdout = logs
	command.Stderr = logs
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	realityMust(t, command.Start())
	done := make(chan struct{})
	var runtimeErr error
	go func() { runtimeErr = command.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = command.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for {
		answer, e := productDNSQuery(fmt.Sprintf("ready-%d.fixture.example", time.Now().UnixNano()), dns.TypeA)
		if e == nil && answer == "198.51.101.2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivered complete profile did not capture DNS through proxy", e)
		}
		select {
		case <-done:
			t.Fatal("complete client runtime exited", runtimeErr)
		default:
		}
		time.Sleep(30 * time.Millisecond)
	}
	got, err := productHTTPGet(http4)
	if err != nil || got == "198.51.100.4" {
		t.Fatal("complete profile did not proxy HTTP", got, err)
	}
	if _, err := productHTTPGet(http6); err == nil {
		t.Fatal("IPv6 escaped complete profile")
	}
	if answer, err := productDNSQuery("aaaa.fixture.example", dns.TypeAAAA); err == nil && answer != "" {
		t.Fatal("AAAA escaped IPv4-only DNS")
	}
	fmt.Println("FULL_CLIENT_READY")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	realityMust(t, err)
	if line != "SERVER_STOPPED\n" {
		t.Fatal("unexpected supervisor instruction")
	}
	if _, err := productHTTPGet(http4); err == nil {
		t.Fatal("complete profile fell back to direct HTTP")
	}
	if answer, err := productDNSQuery(fmt.Sprintf("after-stop-%d.fixture.example", time.Now().UnixNano()), dns.TypeA); err == nil && answer != "" {
		t.Fatal("complete profile fell back to physical DNS")
	}
}
func productHTTP(t *testing.T, network, address string) string {
	listener, err := net.Listen(network, address)
	realityMust(t, err)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		_, _ = io.WriteString(w, host)
	}), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
}
func productHTTPGet(address string) (string, error) {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	response, err := client.Get(address)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	return string(data), err
}
func productDNSQuery(name string, kind uint16) (string, error) {
	q := new(dns.Msg)
	q.SetQuestion(name+".", kind)
	response, _, err := (&dns.Client{Timeout: time.Second}).Exchange(q, "198.51.101.53:53")
	if err != nil {
		return "", err
	}
	if response.Rcode != dns.RcodeSuccess {
		return "", fmt.Errorf("DNS response rejected")
	}
	if len(response.Answer) == 0 {
		return "", nil
	}
	if a, ok := response.Answer[0].(*dns.A); ok {
		return a.A.String(), nil
	}
	return response.Answer[0].String(), nil
}
func productBaselineDNS(t *testing.T) *dns.Server {
	conn, err := net.ListenPacket("udp4", "198.51.101.53:53")
	realityMust(t, err)
	server := &dns.Server{PacketConn: conn, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		r := new(dns.Msg)
		r.SetReply(q)
		r.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}, A: net.ParseIP("198.51.101.99")}}
		_ = w.WriteMsg(r)
	})}
	go func() { _ = server.ActivateAndServe() }()
	return server
}
