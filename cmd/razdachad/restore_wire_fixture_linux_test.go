//go:build linux

package main

// This opt-in fixture never uses installed services. Incoming wg0 is a veth;
// outgoing WG is a real sing-box userspace endpoint and independent kernel peer.
// Full CLI durability/failure proofs remain separate from this bounded wire test.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
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
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/restorestate"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/miekg/dns"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const rwTest = "^TestRestoreWireGuardContained$"
const rwMarker = "synthetic-authenticated-marker"

type rwProcess struct {
	cmd   *exec.Cmd
	start string
	done  chan struct{}
	err   error
}

func rwIdentity(pid int) (string, error) {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return "", e
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", errors.New("invalid stat")
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) <= 19 {
		return "", errors.New("short stat")
	}
	return fields[19], nil
}

// Keep the spawning OS thread alive through Wait: PDEATHSIG belongs to that
// Linux task. Cleanup kills only this exact PID after checking its start identity.
func rwStart(cmd *exec.Cmd) (*rwProcess, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	cmd.SysProcAttr.Setpgid = true
	p := &rwProcess{cmd: cmd, done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if e := cmd.Start(); e != nil {
			ready <- e
			close(p.done)
			return
		}
		var e error
		p.start, e = rwIdentity(cmd.Process.Pid)
		if e != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			ready <- e
			close(p.done)
			return
		}
		ready <- nil
		p.err = cmd.Wait()
		close(p.done)
	}()
	if e := <-ready; e != nil {
		return nil, e
	}
	return p, nil
}
func (p *rwProcess) stop() error {
	select {
	case <-p.done:
		return nil
	default:
	}
	identity, e := rwIdentity(p.cmd.Process.Pid)
	if e == nil {
		if identity != p.start {
			return errors.New("refusing PID-reuse cleanup")
		}
		if e = p.cmd.Process.Kill(); e != nil && !errors.Is(e, os.ErrProcessDone) {
			return e
		}
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("owned child stop timed out")
	}
}
func rwEnv(values map[string]string) []string {
	out := []string{}
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		if !strings.HasPrefix(k, "RAZDACHA_RW_") {
			out = append(out, v)
		}
	}
	for k, v := range values {
		out = append(out, k+"="+v)
	}
	return out
}
func rwCommand(role string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run="+rwTest, "-test.timeout=100s", "-test.v")
	cmd.Env = rwEnv(map[string]string{"RAZDACHA_RW_ROLE": role})
	return cmd
}
func rwRun(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	p, e := rwStart(cmd)
	wireMust(t, e)
	t.Cleanup(func() {
		if e := p.stop(); e != nil {
			t.Error(e)
		}
	})
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatalf("owned child failed: %v\n%s", p.err, out.String())
		}
	case <-time.After(90 * time.Second):
		wireMust(t, p.stop())
		t.Fatal("owned child deadline exceeded")
	}
	return out.String()
}
func rwInNS(t *testing.T, ns netns.NsHandle, next string, values map[string]string) *exec.Cmd {
	t.Helper()
	fd, e := unix.Dup(int(ns))
	wireMust(t, e)
	f := os.NewFile(uintptr(fd), "restore-wire-netns")
	t.Cleanup(func() { _ = f.Close() })
	for _, kind := range []string{"NET", "MNT"} {
		values["RAZDACHA_RW_HOST_"+kind] = os.Getenv("RAZDACHA_RW_HOST_" + kind)
	}
	values["RAZDACHA_RW_ROLE"] = "enter"
	values["RAZDACHA_RW_NEXT"] = next
	cmd := rwCommand("enter")
	cmd.Env = rwEnv(values)
	cmd.ExtraFiles = []*os.File{f}
	return cmd
}
func TestRestoreWireGuardContained(t *testing.T) {
	if os.Getenv("RAZDACHA_RESTORE_WIRE_TEST") != "1" {
		t.Skip("requires explicit opt-in and separately reviewed Linux namespace runner")
	}
	if os.Geteuid() != 0 {
		t.Fatal("root is required inside the reviewed disposable runner")
	}
	switch os.Getenv("RAZDACHA_RW_ROLE") {
	case "enter":
		runtime.LockOSThread()
		wireMust(t, unix.Setns(3, unix.CLONE_NEWNET))
		wireMust(t, unix.Close(3))
		env := os.Environ()
		for i, v := range env {
			if strings.HasPrefix(v, "RAZDACHA_RW_ROLE=") {
				env[i] = "RAZDACHA_RW_ROLE=" + os.Getenv("RAZDACHA_RW_NEXT")
			}
		}
		wireMust(t, unix.Exec(os.Args[0], os.Args, env))
		return
	case "sink":
		rwVerifyContained(t)
		rwSink(t)
		return
	case "probe":
		rwVerifyContained(t)
		rwProbe(t)
		return
	case "isolated":
		parent, e := strconv.Atoi(os.Getenv("RAZDACHA_RW_PARENT_PID"))
		wireMust(t, e)
		identity, e := rwIdentity(parent)
		wireMust(t, e)
		if parent != os.Getppid() || identity != os.Getenv("RAZDACHA_RW_PARENT_START") {
			t.Fatal("parent identity mismatch")
		}
		for _, kind := range []string{"net", "mnt"} {
			current, e := os.Readlink("/proc/self/ns/" + kind)
			wireMust(t, e)
			actualParent, e := os.Readlink(fmt.Sprintf("/proc/%d/ns/%s", parent, kind))
			wireMust(t, e)
			expected := os.Getenv("RAZDACHA_RW_HOST_" + strings.ToUpper(kind))
			if expected == "" || current == expected || actualParent != expected {
				t.Fatal("refusing unverified namespace isolation")
			}
		}
		wireMust(t, unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""))
		wireMust(t, unix.Mount("tmpfs", "/var/lib", "tmpfs", unix.MS_NODEV|unix.MS_NOSUID, "size=64m,mode=0700"))
		wireMust(t, os.MkdirAll("/var/lib/sing-box", 0700))
		rwExercise(t)
		return
	case "":
		values := map[string]string{"RAZDACHA_RW_ROLE": "isolated", "RAZDACHA_RW_PARENT_PID": strconv.Itoa(os.Getpid())}
		identity, e := rwIdentity(os.Getpid())
		wireMust(t, e)
		values["RAZDACHA_RW_PARENT_START"] = identity
		for _, key := range []string{"SINGBOX", "CLI"} {
			name := "RAZDACHA_TEST_" + key
			bin, e := filepath.Abs(os.Getenv(name))
			wireMust(t, e)
			info, e := os.Stat(bin)
			wireMust(t, e)
			if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
				t.Fatal("explicit fixture executable required")
			}
			values["RAZDACHA_RW_"+key] = bin
		}
		for _, kind := range []string{"net", "mnt"} {
			v, e := os.Readlink("/proc/self/ns/" + kind)
			wireMust(t, e)
			values["RAZDACHA_RW_HOST_"+strings.ToUpper(kind)] = v
		}
		cmd := rwCommand("isolated")
		cmd.Env = rwEnv(values)
		cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWNET | unix.CLONE_NEWNS}
		t.Log(rwRun(t, cmd))
		return
	default:
		t.Fatal("unknown fixture role")
	}
}

func rwVerifyContained(t *testing.T) {
	t.Helper()
	for _, kind := range []string{"net", "mnt"} {
		actual, e := os.Readlink("/proc/self/ns/" + kind)
		wireMust(t, e)
		original := os.Getenv("RAZDACHA_RW_HOST_" + strings.ToUpper(kind))
		if original == "" || original == actual {
			t.Fatal("child refuses unverified isolation")
		}
	}
}

type rwKeys struct{ Server, Client wgtypes.Key }
type rwStatus struct {
	Handshake             [2]bool
	Decrypted             [2]int
	PlainSelected, Direct int
}

func rwSink(t *testing.T) {
	if _, e := os.Stat("/sys/module/wireguard"); e != nil {
		t.Fatal("WireGuard must already be loaded; fixture refuses global module loading")
	}
	var keys [2]rwKeys
	wireMust(t, json.NewDecoder(os.Stdin).Decode(&keys))
	client, e := wgctrl.New()
	wireMust(t, e)
	defer client.Close()
	for i := range keys {
		name := fmt.Sprintf("rws%d", i)
		wireMust(t, netlink.LinkAdd(&netlink.GenericLink{LinkAttrs: netlink.LinkAttrs{Name: name}, LinkType: "wireguard"}))
		l, e := netlink.LinkByName(name)
		wireMust(t, e)
		addr, e := netlink.ParseAddr(fmt.Sprintf("10.77.%d.1/24", i))
		wireMust(t, e)
		wireMust(t, netlink.AddrAdd(l, addr))
		_, allowed, e := net.ParseCIDR(fmt.Sprintf("10.77.%d.2/32", i))
		wireMust(t, e)
		port := 51820 + i
		wireMust(t, client.ConfigureDevice(name, wgtypes.Config{PrivateKey: &keys[i].Server, ListenPort: &port, ReplacePeers: true, Peers: []wgtypes.PeerConfig{{PublicKey: keys[i].Client.PublicKey(), ReplaceAllowedIPs: true, AllowedIPs: []net.IPNet{*allowed}}}}))
		wireMust(t, netlink.LinkSetUp(l))
	}
	var mu sync.Mutex
	var counts rwStatus
	listener, e := net.Listen("tcp4", ":12080")
	wireMust(t, e)
	server := &http.Server{ReadHeaderTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			mu.Lock()
			status := counts
			mu.Unlock()
			for i := range keys {
				dev, e := client.Device(fmt.Sprintf("rws%d", i))
				if e != nil || len(dev.Peers) != 1 {
					http.Error(w, "observer unavailable", 500)
					return
				}
				status.Handshake[i] = !dev.Peers[0].LastHandshakeTime.IsZero()
			}
			_ = json.NewEncoder(w).Encode(status)
			return
		}
		source, _, _ := net.SplitHostPort(r.RemoteAddr)
		mu.Lock()
		if r.URL.Path == "/direct" {
			counts.Direct++
		} else if r.Header.Get("X-Fixture-Marker") == rwMarker {
			switch source {
			case "10.77.0.2":
				counts.Decrypted[0]++
			case "10.77.1.2":
				counts.Decrypted[1]++
			default:
				counts.PlainSelected++
			}
		}
		mu.Unlock()
		_, _ = io.WriteString(w, rwMarker)
	})}
	defer server.Close()
	fmt.Println("RESTORE-WIRE-SINK-READY")
	wireMust(t, server.Serve(listener))
}
func rwProbe(t *testing.T) {
	if name := os.Getenv("RAZDACHA_RW_DNS"); name != "" {
		q := new(dns.Msg)
		q.SetQuestion(name, dns.TypeA)
		client := &dns.Client{Timeout: 500 * time.Millisecond, Dialer: &net.Dialer{LocalAddr: &net.UDPAddr{IP: net.ParseIP(os.Getenv("RAZDACHA_RW_SOURCE"))}}}
		answer, _, e := client.Exchange(q, "10.8.0.1:53")
		wireMust(t, e)
		if os.Getenv("RAZDACHA_RW_DNS_ALLOW") == "1" {
			if answer == nil || answer.Rcode != dns.RcodeSuccess || len(answer.Answer) == 0 {
				t.Fatal("chosen domain positive control failed")
			}
		} else if answer == nil || answer.Rcode != dns.RcodeRefused {
			t.Fatal("scoped domain rejection missing")
		}
		return
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 350 * time.Millisecond, LocalAddr: &net.TCPAddr{IP: net.ParseIP(os.Getenv("RAZDACHA_RW_SOURCE"))}}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 600 * time.Millisecond}
	req, e := http.NewRequest("GET", os.Getenv("RAZDACHA_RW_URL"), nil)
	wireMust(t, e)
	req.Header.Set("X-Fixture-Marker", rwMarker)
	res, e := client.Do(req)
	ok := false
	if e == nil {
		b, err := io.ReadAll(io.LimitReader(res.Body, 1024))
		_ = res.Body.Close()
		ok = err == nil && res.StatusCode == 200 && string(b) == rwMarker
	}
	want := os.Getenv("RAZDACHA_RW_WANT") == "1"
	if ok != want {
		t.Fatalf("HTTP marker got=%t want=%t error=%v", ok, want, e)
	}
}

// AF_PACKET sees attempts, including unauthenticated handshake initiation. Sensor
// controls, serialized barriers and kernel drop checks make absence falsifiable.
type rwCapture struct {
	fd       int
	mu       sync.Mutex
	attempts [2]int
	err      error
	ack      chan struct{}
	done     chan struct{}
	stop     chan struct{}
}

func rwNewCapture(t *testing.T) *rwCapture {
	protocol := uint16(unix.ETH_P_IP)
	protocol = protocol<<8 | protocol>>8
	fd, e := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(protocol))
	wireMust(t, e)
	iface, e := net.InterfaceByName("wan0")
	wireMust(t, e)
	wireMust(t, unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: protocol, Ifindex: iface.Index}))
	wireMust(t, unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Usec: 100000}))
	c := &rwCapture{fd: fd, ack: make(chan struct{}, 1), done: make(chan struct{}), stop: make(chan struct{})}
	go func() {
		defer close(c.done)
		buf := make([]byte, 65536)
		for {
			select {
			case <-c.stop:
				return
			default:
			}
			n, _, err := unix.Recvfrom(fd, buf, 0)
			if err == unix.EAGAIN || err == unix.EINTR {
				continue
			}
			if err != nil {
				c.mu.Lock()
				c.err = err
				c.mu.Unlock()
				return
			}
			if n < 42 || binary.BigEndian.Uint16(buf[12:14]) != unix.ETH_P_IP {
				continue
			}
			ip := buf[14:n]
			ihl := int(ip[0]&15) * 4
			if ihl < 20 || len(ip) < ihl+8 || ip[9] != unix.IPPROTO_UDP || !net.IP(ip[16:20]).Equal(net.ParseIP("192.0.2.2")) {
				continue
			}
			port := int(binary.BigEndian.Uint16(ip[ihl+2 : ihl+4]))
			c.mu.Lock()
			if port >= 51820 && port <= 51821 {
				c.attempts[port-51820]++
			}
			c.mu.Unlock()
			if port == 51822 {
				select {
				case c.ack <- struct{}{}:
				default:
				}
			}
		}
	}()
	t.Cleanup(func() {
		close(c.stop)
		select {
		case <-c.done:
		case <-time.After(time.Second):
			t.Error("capture cleanup timed out")
		}
		_ = unix.Close(fd)
	})
	return c
}
func (c *rwCapture) snapshot(t *testing.T) [2]int {
	t.Helper()
	socket, e := net.DialTimeout("udp4", "192.0.2.2:51822", time.Second)
	wireMust(t, e)
	_, e = socket.Write([]byte("barrier"))
	wireMust(t, e)
	wireMust(t, socket.Close())
	select {
	case <-c.ack:
	case <-c.done:
		t.Fatal("capture stopped")
	case <-time.After(time.Second):
		t.Fatal("capture barrier timed out")
	}
	stats, e := unix.GetsockoptTpacketStats(c.fd, unix.SOL_PACKET, unix.PACKET_STATISTICS)
	wireMust(t, e)
	if stats.Drops != 0 {
		t.Fatal("capture dropped frames; evidence inconclusive")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	wireMust(t, c.err)
	return c.attempts
}

// The checker executes the real runtime but shares the fixture's bounded child
// ownership instead of production's ordinary external-command lifecycle.
type rwChecker struct{ bin string }

func (c rwChecker) Check(ctx context.Context, path string) error {
	cmd := exec.Command(c.bin, "check", "-c", path)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	p, err := rwStart(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = p.stop() }()
	select {
	case <-p.done:
		if p.err != nil {
			return fmt.Errorf("synthetic runtime check failed: %w", p.err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return errors.New("synthetic runtime check timed out")
	}
}

type rwRuntime struct {
	bin, path string
	process   *rwProcess
	before    func() error
}

func (r *rwRuntime) stop() error {
	if r.process != nil {
		if e := r.process.stop(); e != nil {
			return e
		}
		r.process = nil
	}
	return nil
}
func (r *rwRuntime) Reload(ctx context.Context) error {
	if r.before != nil {
		if e := r.before(); e != nil {
			return e
		}
	}
	if e := r.stop(); e != nil {
		return e
	}
	cmd := exec.Command(r.bin, "run", "-c", r.path)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	p, e := rwStart(cmd)
	if e != nil {
		return e
	}
	r.process = p
	client := &http.Client{Timeout: 150 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return fmt.Errorf("runtime exited before readiness: %w", p.err)
		default:
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:9090/version", nil)
		res, e := client.Do(req)
		if e == nil {
			_ = res.Body.Close()
			if res.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return errors.New("contained runtime readiness timed out")
}

type rwHarness struct {
	ctx     context.Context
	client  netns.NsHandle
	capture *rwCapture
	initial [2]int
	keys    [2]rwKeys
	root    string
	st      *store.Store
	gate    *applyGate
	applier *singbox.Applier
	service *rwRuntime
	http    *http.Client
}

func (h *rwHarness) probe(t *testing.T, source, url string, want bool) {
	t.Helper()
	v := "0"
	if want {
		v = "1"
	}
	rwRun(t, rwInNS(t, h.client, "probe", map[string]string{"RAZDACHA_RW_SOURCE": source, "RAZDACHA_RW_URL": url, "RAZDACHA_RW_WANT": v}))
}
func (h *rwHarness) status(t *testing.T) rwStatus {
	t.Helper()
	res, e := h.http.Get("http://192.0.2.2:12080/status")
	wireMust(t, e)
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("WG status observer failed")
	}
	var s rwStatus
	wireMust(t, json.NewDecoder(res.Body).Decode(&s))
	return s
}
func (h *rwHarness) absent(t *testing.T, phase string) {
	t.Helper()
	time.Sleep(1200 * time.Millisecond)
	now, s := h.capture.snapshot(t), h.status(t)
	if now != h.initial || s.Handshake != [2]bool{} || s.Decrypted != [2]int{} || s.PlainSelected != 0 {
		t.Fatalf("forbidden activation phase=%s attempts=%v initial=%v status=%+v", phase, now, h.initial, s)
	}
	t.Logf("RESTORE-WIRE-ABSENT phase=%s udp_attempts=0 handshakes=0 decrypted=0", phase)
}
func (h *rwHarness) apply(t *testing.T) {
	t.Helper()
	snap, e := h.st.Snapshot(h.ctx)
	wireMust(t, e)
	_, e = h.gate.Apply(h.ctx, snap)
	wireMust(t, e)
}
func (h *rwHarness) rejection(t *testing.T, phase string) {
	t.Helper()
	for _, addr := range []string{"203.0.113.10", "203.0.113.30"} {
		h.probe(t, "10.8.0.2", "http://"+addr+":12080/selected", false)
	}
	for _, name := range []string{"chosen.example.", "other.example."} {
		rwRun(t, rwInNS(t, h.client, "probe", map[string]string{"RAZDACHA_RW_SOURCE": "10.8.0.2", "RAZDACHA_RW_DNS": name}))
	}
	t.Logf("RESTORE-WIRE-SCOPED-REJECTION phase=%s", phase)
	h.probe(t, "10.8.0.2", "http://203.0.113.99:12080/direct", true)
	h.probe(t, "10.8.0.3", "http://203.0.113.10:12080/direct", true)
}
func rwExercise(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	clientNS, sinkNS := wireTopology(t)
	ch, e := netlink.NewHandleAt(clientNS)
	wireMust(t, e)
	l, e := ch.LinkByName("client0")
	wireMust(t, e)
	addr, e := netlink.ParseAddr("10.8.0.3/24")
	wireMust(t, e)
	wireMust(t, ch.AddrAdd(l, addr))
	ch.Close()
	root, e := os.MkdirTemp("/var/lib", "restore-wire-")
	wireMust(t, e)
	// The tmpfs lifetime owns all files; no recursive host cleanup is necessary.
	h := &rwHarness{ctx: ctx, client: clientNS, root: root, http: &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}}
	defer h.http.CloseIdleConnections()
	for i := range h.keys {
		h.keys[i].Server, e = wgtypes.GeneratePrivateKey()
		wireMust(t, e)
		h.keys[i].Client, e = wgtypes.GeneratePrivateKey()
		wireMust(t, e)
	}
	cmd := rwInNS(t, sinkNS, "sink", map[string]string{})
	data, e := json.Marshal(h.keys)
	wireMust(t, e)
	cmd.Stdin = bytes.NewReader(data)
	pipe, e := cmd.StdoutPipe()
	wireMust(t, e)
	cmd.Stderr = io.Discard
	sink, e := rwStart(cmd)
	wireMust(t, e)
	t.Cleanup(func() { wireMust(t, sink.stop()) })
	ready := make(chan error, 1)
	go func() {
		b := make([]byte, len("RESTORE-WIRE-SINK-READY\n"))
		_, e := io.ReadFull(pipe, b)
		if e == nil && string(b) != "RESTORE-WIRE-SINK-READY\n" {
			e = errors.New("invalid readiness")
		}
		ready <- e
		_, _ = io.Copy(io.Discard, pipe)
	}()
	select {
	case e := <-ready:
		wireMust(t, e)
	case <-sink.done:
		t.Fatal("sink exited before ready")
	case <-time.After(3 * time.Second):
		t.Fatal("sink readiness timeout")
	}
	h.capture = rwNewCapture(t)
	for i := range h.keys {
		c, e := net.Dial("udp4", fmt.Sprintf("192.0.2.2:%d", 51820+i))
		wireMust(t, e)
		_, e = c.Write([]byte{1})
		wireMust(t, e)
		wireMust(t, c.Close())
	}
	h.initial = h.capture.snapshot(t)
	if h.initial[0] == 0 || h.initial[1] == 0 {
		t.Fatal("attempted-packet positive controls failed")
	}
	h.probe(t, "10.8.0.2", "http://203.0.113.99:12080/direct", true)
	if h.status(t).Direct == 0 {
		t.Fatal("direct observer positive control failed")
	}
	packet, e := net.ListenPacket("udp4", "127.0.0.1:15353")
	wireMust(t, e)
	dnsServer := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		a := new(dns.Msg)
		a.SetReply(q)
		ip := "203.0.113.10"
		if q.Question[0].Name == "other.example." {
			ip = "203.0.113.30"
		}
		rr, _ := dns.NewRR(q.Question[0].Name + " 0 IN A " + ip)
		a.Answer = []dns.RR{rr}
		_ = w.WriteMsg(a)
	})}
	dnsDone := make(chan error, 1)
	go func() { dnsDone <- dnsServer.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = dnsServer.Shutdown()
		select {
		case e := <-dnsDone:
			wireMust(t, e)
		case <-time.After(time.Second):
			t.Error("DNS cleanup timeout")
		}
	})
	q := new(dns.Msg)
	q.SetQuestion("control.example.", dns.TypeA)
	answer, _, e := (&dns.Client{Timeout: time.Second}).Exchange(q, "127.0.0.1:15353")
	wireMust(t, e)
	if answer == nil || len(answer.Answer) != 1 {
		t.Fatal("DNS positive control failed")
	}
	path := filepath.Join(root, "archive.db")
	h.st, e = store.Open(ctx, path)
	wireMust(t, e)
	defer func() { _ = h.st.Close() }()
	settings := store.DefaultSettings()
	settings.DNSType = "udp"
	settings.DNSUpstream = "127.0.0.1:15353"
	wireMust(t, h.st.SaveSettings(ctx, settings))
	for i := range h.keys {
		raw := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.77.%d.2/32\n[Peer]\nPublicKey = %s\nEndpoint = 192.0.2.2:%d\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 1\n", h.keys[i].Client.String(), i, h.keys[i].Server.PublicKey().String(), 51820+i)
		parsed, e := singbox.Parse(raw)
		wireMust(t, e)
		id := fmt.Sprintf("synthetic-wg-%d", i)
		_, e = h.st.CreateTunnel(ctx, store.Tunnel{ID: id, Name: id, Type: parsed.Type, Source: parsed.Source, Raw: raw, Parsed: parsed.Parsed, Enabled: true})
		wireMust(t, e)
		domain, cidr := "chosen.example", "203.0.113.10/32"
		if i == 1 {
			domain, cidr = "other.example", "203.0.113.30/32"
		}
		_, e = h.st.CreateRule(ctx, store.Rule{Name: id, Enabled: true, Action: store.ActionTunnel, TunnelID: id, Domains: []string{domain}, Subnets: []string{cidr}, PeerScope: store.ScopeSelected, PeerIDs: []string{"selected"}})
		wireMust(t, e)
	}
	for i, id := range []string{"selected", "unselected"} {
		key, e := wgtypes.GeneratePrivateKey()
		wireMust(t, e)
		_, e = h.st.CreatePeer(ctx, store.Peer{ID: id, Name: id, Address: fmt.Sprintf("10.8.0.%d", i+2), PublicKey: key.PublicKey().String(), PrivateKey: key.String(), Enabled: true})
		wireMust(t, e)
	}
	h.service = &rwRuntime{bin: os.Getenv("RAZDACHA_RW_SINGBOX"), path: filepath.Join(root, "etc/sing-box/config.json")}
	t.Cleanup(func() { wireMust(t, h.service.stop()) })
	h.applier = &singbox.Applier{ConfigPath: h.service.path, Checker: rwChecker{bin: h.service.bin}, Reloader: h.service, Log: quietSlog()}
	n := &networkPublication{path: filepath.Join(root, "generation.json"), uncertain: true, guard: netstack.SetClientGuard, wg: func(_ context.Context, s store.Settings) error { return wireConfigureWG(s) }, route: func(context.Context) error { return netstack.NewRoute().Apply() }, nft: func(_ context.Context, s store.Snapshot, content map[string]lists.List) (int, error) {
		nf, e := netstack.NewNft()
		if e != nil {
			return 0, e
		}
		sub := generationSubnets(s, content)
		_, e = nf.Apply(netstack.NftConfig{WANInterface: "wan0", ResolverAddr: s.Settings.WGServerAddress, Subnets: sub})
		return len(sub), e
	}}
	h.gate = newApplyGate(h.applier, n, quietSlog())
	// Install an endpoint-bearing prior runtime candidate WITHOUT starting it.
	// This models an inactive destination before restore and subsequent reboot.
	snap, e := h.st.Snapshot(ctx)
	wireMust(t, e)
	opts, e := singbox.Generate(snap, nil)
	wireMust(t, e)
	config, e := singbox.Marshal(opts)
	wireMust(t, e)
	// A loopback-only observer inlet makes decrypted runtime traffic observable
	// even while client ingress is guarded. Both revisions use the same template
	// with fresh independently generated keys per invocation. Explicit Apply
	// regenerates the entire production config and
	// removes this fixture-only inlet; it never alters production source.
	var prior map[string]any
	wireMust(t, json.Unmarshal(config, &prior))
	prior["inbounds"] = append(prior["inbounds"].([]any), map[string]any{"type": "direct", "tag": "restore-marker-probe", "listen": "127.0.0.1", "listen_port": 18081, "override_address": "203.0.113.10", "override_port": 12080})
	route := prior["route"].(map[string]any)
	route["rules"] = append([]any{map[string]any{"inbound": []string{"restore-marker-probe"}, "action": "route", "outbound": singbox.TunnelTag("synthetic-wg-0")}}, route["rules"].([]any)...)
	config, e = json.Marshal(prior)
	wireMust(t, e)
	wireMust(t, os.MkdirAll(filepath.Dir(h.service.path), 0700))
	wireMust(t, os.WriteFile(h.service.path, config, 0600))
	wireMust(t, h.applier.Checker.Check(ctx, h.service.path))
	wireMust(t, h.st.Close())
	archive, readErr := os.ReadFile(path)
	wireMust(t, readErr)
	t.Logf("RESTORE-WIRE-INPUT prior_config_sha256=%x archive_sha256=%x", sha256.Sum256(config), sha256.Sum256(archive))
	h.absent(t, "before-actual-restore")
	cli := exec.Command(os.Getenv("RAZDACHA_RW_CLI"), "-test.run=^TestRestoreWireCLI$", "-test.timeout=20s", "-test.v")
	cli.Env = append(os.Environ(), "RAZDACHA_RESTORE_WIRE_CLI=1", "RAZDACHA_RW_ROOT="+root)
	rwRun(t, cli)
	h.st, e = store.Open(ctx, filepath.Join(root, "state.db"))
	wireMust(t, e)
	rwRestored(t, h)
}

// BEGIN FIXED RESTORE ADAPTER
// Baseline overlays replace ONLY this section and the restorestate import.
// The CLI overlay separately calls the genuine pinned old restore() transaction.
func rwRestored(t *testing.T, h *rwHarness) {
	ctl := &restorestate.Controller{Dir: h.root, StopRuntime: h.service.stop}
	state, e := ctl.Status()
	wireMust(t, e)
	if _, err := os.Stat(h.service.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("actual restore left endpoint config loadable")
	}
	if state == nil || !state.Held || !state.RuntimeInhibited {
		t.Fatal("actual restore did not install trusted hold")
	}
	h.gate.restore = ctl
	h.applier.BeforeReload = func(context.Context) error { return ctl.BeginPublishing(h.service.path) }
	h.service.before = func() error { return restorestate.RuntimeStartAllowed(h.root, h.service.path) }
	if e = h.service.Reload(h.ctx); e == nil {
		t.Fatal("independent runtime boot admitted held credentials")
	}
	snap, e := h.st.Snapshot(h.ctx)
	wireMust(t, e)
	for _, tunnel := range snap.Tunnels {
		if tunnel.Enabled {
			t.Fatal("actual restore left WG enabled")
		}
	}
	if _, e = h.gate.Apply(h.ctx, snap); !errors.Is(e, restorestate.ErrHeld) {
		t.Fatal("held Apply admitted")
	}
	wireMust(t, h.gate.recoverStartup(h.ctx, filepath.Join(h.root, "bootstrap.json")))
	h.absent(t, "actual-restore-held-boot-denied")
	wireMust(t, ctl.Resume())
	wireMust(t, ctl.Resume())
	h.absent(t, "resumed-without-apply")
	h.service.before = func() error {
		h.probe(t, "10.8.0.2", "http://203.0.113.10:12080/selected", false)
		return restorestate.RuntimeStartAllowed(h.root, h.service.path)
	}
	h.apply(t)
	installed, readErr := os.ReadFile(h.service.path)
	wireMust(t, readErr)
	if bytes.Contains(installed, []byte("restore-marker-probe")) {
		t.Fatal("production Apply retained fixture observer inlet")
	}
	h.rejection(t, "paused-apply")
	h.absent(t, "paused-apply")
	chosen, e := h.st.Tunnel(h.ctx, "synthetic-wg-0")
	wireMust(t, e)
	chosen.Enabled = true
	wireMust(t, h.st.UpdateTunnel(h.ctx, chosen))
	snap, e = h.st.Snapshot(h.ctx)
	wireMust(t, e)
	_, e = h.gate.applyPools(h.ctx, snap)
	wireMust(t, e)
	_, e = h.gate.refreshLists(h.ctx)
	wireMust(t, e)
	wireMust(t, h.gate.recoverStartup(h.ctx, filepath.Join(h.root, "bootstrap.json")))
	h.rejection(t, "pending-enable")
	h.absent(t, "pending-enable-without-apply")
	h.apply(t)
	h.probe(t, "10.8.0.2", "http://203.0.113.10:12080/selected", true)
	h.probe(t, "10.8.0.2", "http://203.0.113.30:12080/selected", false)
	rwRun(t, rwInNS(t, h.client, "probe", map[string]string{"RAZDACHA_RW_SOURCE": "10.8.0.2", "RAZDACHA_RW_DNS": "other.example."}))
	rwRun(t, rwInNS(t, h.client, "probe", map[string]string{"RAZDACHA_RW_SOURCE": "10.8.0.2", "RAZDACHA_RW_DNS": "chosen.example.", "RAZDACHA_RW_DNS_ALLOW": "1"}))
	h.probe(t, "10.8.0.3", "http://203.0.113.30:12080/direct", true)
	h.probe(t, "10.8.0.2", "http://203.0.113.99:12080/direct", true)
	time.Sleep(1200 * time.Millisecond)
	attempts, status := h.capture.snapshot(t), h.status(t)
	if attempts[0] <= h.initial[0] || !status.Handshake[0] || status.Decrypted[0] == 0 {
		t.Fatalf("chosen encrypted positive control failed attempts=%v status=%+v", attempts, status)
	}
	if attempts[1] != h.initial[1] || status.Handshake[1] || status.Decrypted[1] != 0 || status.PlainSelected != 0 {
		t.Fatalf("unchosen credential or plaintext fallback activated attempts=%v status=%+v", attempts, status)
	}
	t.Log("RESTORE-WIRE-POSITIVE chosen_udp=observed chosen_handshake=authenticated chosen_marker=decrypted unchosen_udp=0 unchosen_handshake=0 unchosen_marker=0")
}

// END FIXED RESTORE ADAPTER

// The following fixture-only baseline adapter is extracted from this comment
// into the fixed-adapter section when building pinned main 0591236. Remove only
// the restorestate import. All non-fixture baseline files must remain byte exact.
// The actual CLI restore runs first; no pause is simulated or omitted here.
// BEGIN BASELINE RESTORE ADAPTER
/*
func rwRestored(t *testing.T, h *rwHarness) {
 snap, err := h.st.Snapshot(h.ctx)
 wireMust(t,err)
 if len(snap.Tunnels)!=2 || !snap.Tunnels[0].Enabled || !snap.Tunnels[1].Enabled {t.Fatal("old actual restore did not preserve enabled credentials")}
 if _,err=os.Stat(h.service.path);err!=nil {t.Fatal("old actual restore did not retain prior endpoint config")}
 // Independent managed-runtime start adapter after actual old restore. This
 // models the next service start, not spontaneous boot or a real systemd unit.
 // The client guard remains installed throughout. The identical synthetic
 // prior-config loopback inlet supplies only a fixed authenticated marker.
 wireMust(t,h.service.Reload(h.ctx))
 deadline:=time.Now().Add(4*time.Second)
 for {
  request,e:=http.NewRequestWithContext(h.ctx,"GET","http://127.0.0.1:18081/selected",nil);wireMust(t,e)
  request.Header.Set("X-Fixture-Marker",rwMarker)
  response,e:=h.http.Do(request)
  if e==nil {body,readErr:=io.ReadAll(io.LimitReader(response.Body,1024));_ = response.Body.Close();if readErr==nil && string(body)==rwMarker {break}}
  if time.Now().After(deadline){t.Fatal("baseline retained-runtime marker timed out")}
  time.Sleep(50*time.Millisecond)
 }
 attempts,status:=h.capture.snapshot(t),h.status(t)
 if attempts[0]<=h.initial[0] || !status.Handshake[0] || status.Decrypted[0]==0 || status.PlainSelected!=0 {t.Fatalf("baseline forbidden activation not reproduced attempts=%v status=%+v",attempts,status)}
 t.Log("RESTORE-WIRE-BASELINE-FORBIDDEN actual_restore=old retained_config=loadable independent_runtime_start=performed udp_attempts=observed handshake=authenticated marker=decrypted client_guard=retained")
}
*/
// END BASELINE RESTORE ADAPTER
