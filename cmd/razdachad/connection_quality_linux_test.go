//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func wireQualityEnabled() bool { return os.Getenv("RAZDACHA_WIRE_QUALITY") == "1" }
func wireTestTimeout() string {
	if wireQualityEnabled() {
		return "-test.timeout=180s"
	}
	return "-test.timeout=80s"
}

func wireIngressName() string {
	if wireQualityEnabled() {
		return "under0"
	}
	return "wg0"
}

func wireClientUnderlayName() string {
	if wireQualityEnabled() {
		return "under1"
	}
	return "client0"
}

func wireGatewayAddress() string {
	if wireQualityEnabled() {
		return "198.51.100.1/30"
	}
	return "10.8.0.1/24"
}

func wireClientAddress() string {
	if wireQualityEnabled() {
		return "198.51.100.2/30"
	}
	return "10.8.0.2/24"
}

// All quality setters run only after this cloned-namespace gate. Child namespace
// handles are created by wireNewNS; no handle or interface is imported from a host.
func qualityRequireIsolated(t *testing.T) {
	t.Helper()
	for _, pair := range []struct{ kind, key string }{{"net", "RAZDACHA_WIRE_PARENT"}, {"mnt", "RAZDACHA_WIRE_PARENT_MOUNT"}} {
		current, err := os.Readlink("/proc/self/ns/" + pair.kind)
		wireMust(t, err)
		if os.Getenv(pair.key) == "" || current == os.Getenv(pair.key) {
			t.Fatal("refusing non-isolated quality namespace")
		}
	}
}

func qualityRequireFresh(t *testing.T) {
	t.Helper()
	qualityRequireIsolated(t)
	links, err := netlink.LinkList()
	wireMust(t, err)
	if len(links) != 1 || links[0].Attrs().Name != "lo" {
		t.Fatal("quality fixture requires an empty fresh network namespace")
	}
	routes, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
	wireMust(t, err)
	if len(routes) != 0 {
		t.Fatal("quality fixture refuses pre-existing routes")
	}
}

func qualityWireGuard(t *testing.T, gateway, client *netlink.Handle, ns netns.NsHandle) {
	t.Helper()
	qualityRequireIsolated(t)
	serverKey, err := wgtypes.GeneratePrivateKey()
	wireMust(t, err)
	clientKey, err := wgtypes.GeneratePrivateKey()
	wireMust(t, err)
	for _, spec := range []struct {
		h          *netlink.Handle
		name, addr string
	}{{gateway, "wg0", "10.8.0.1/24"}, {client, "client0", "10.8.0.2/24"}} {
		qualityRequireIsolated(t)
		wireMust(t, spec.h.LinkAdd(&netlink.Wireguard{LinkAttrs: netlink.LinkAttrs{Name: spec.name, MTU: 1280}}))
		wireLink(t, spec.h, spec.name, spec.addr)
	}
	_, allowed, err := net.ParseCIDR("10.8.0.2/31")
	wireMust(t, err)
	serverPort, clientPort := 51821, 51822
	configure := func(name string, cfg wgtypes.Config) {
		qualityRequireIsolated(t)
		control, e := wgctrl.New()
		wireMust(t, e)
		defer control.Close()
		device, e := control.Device(name)
		wireMust(t, e)
		if device.Type != wgtypes.LinuxKernel {
			t.Fatal("refusing non-kernel WireGuard configuration")
		}
		wireMust(t, control.ConfigureDevice(name, cfg))
	}
	configure("wg0", wgtypes.Config{PrivateKey: &serverKey, ListenPort: &serverPort, ReplacePeers: true, Peers: []wgtypes.PeerConfig{{PublicKey: clientKey.PublicKey(), ReplaceAllowedIPs: true, AllowedIPs: []net.IPNet{*allowed}, Endpoint: &net.UDPAddr{IP: net.ParseIP("198.51.100.2"), Port: clientPort}}}})
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	previous, err := netns.Get()
	wireMust(t, err)
	defer previous.Close()
	wireMust(t, netns.Set(ns))
	defer func() { wireMust(t, netns.Set(previous)) }()
	_, all, err := net.ParseCIDR("0.0.0.0/0")
	wireMust(t, err)
	configure("client0", wgtypes.Config{PrivateKey: &clientKey, ListenPort: &clientPort, ReplacePeers: true, Peers: []wgtypes.PeerConfig{{PublicKey: serverKey.PublicKey(), ReplaceAllowedIPs: true, AllowedIPs: []net.IPNet{*all}, Endpoint: &net.UDPAddr{IP: net.ParseIP("198.51.100.1"), Port: serverPort}}}})
}

func qualityPeerStats(t *testing.T) wgtypes.Peer {
	t.Helper()
	qualityRequireIsolated(t)
	control, err := wgctrl.New()
	wireMust(t, err)
	defer control.Close()
	device, err := control.Device("wg0")
	wireMust(t, err)
	if device.Type != wgtypes.LinuxKernel || len(device.Peers) != 1 {
		t.Fatal("quality ingress is not a reciprocal kernel WireGuard peer")
	}
	peer := device.Peers[0]
	if peer.LastHandshakeTime.IsZero() || time.Since(peer.LastHandshakeTime) > 3*time.Minute || peer.ReceiveBytes == 0 || peer.TransmitBytes == 0 {
		t.Fatal("missing encrypted handshake or bidirectional WireGuard traffic")
	}
	return peer
}

func (p *peerWire) qualityRun(t *testing.T, source, target, prefix, mode string) {
	t.Helper()
	cmd := wireExec(t, p.h.client, "quality-probe", source, target, prefix, mode)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("quality %s: %v\n%s", mode, err, output)
	}
	if !strings.Contains(string(output), "266-QUALITY-PROBE-PASS "+mode) {
		t.Fatalf("quality probe lacks completion evidence: %s", output)
	}
	t.Logf("%s", output)
	p.h.assertNoForbidden(t)
}

func (p *peerWire) qualityExercise(t *testing.T, stage string) {
	t.Helper()
	before := qualityPeerStats(t)
	target, prefix := "203.0.113.20:12080", ""
	if stage == "selective" {
		target = "203.0.113.10:12080"
		prefix = "private:"
	}
	if stage == "full-egress" {
		prefix = "private:"
	}
	p.qualityRun(t, p.a.Address, target, prefix, "matrix")
	p.qualityRun(t, p.a.Address, target, prefix, "persistent")
	// A second source remains a direct control in every policy mode.
	p.qualityRun(t, p.b.Address, "203.0.113.20:12080", "", "matrix")
	after := qualityPeerStats(t)
	if after.ReceiveBytes <= before.ReceiveBytes || after.TransmitBytes <= before.TransmitBytes {
		t.Fatal("quality probes did not advance encrypted WireGuard counters")
	}
	t.Logf("266-QUALITY-STAGE-PASS mode=%s encrypted-rx-delta=%d encrypted-tx-delta=%d", stage, after.ReceiveBytes-before.ReceiveBytes, after.TransmitBytes-before.TransmitBytes)
}

func (p *peerWire) qualityUnavailable(t *testing.T) {
	t.Helper()
	for _, proto := range []string{"tcp4", "udp4"} {
		p.probe(t, p.a.Address, proto, "203.0.113.20:12080", "deny", "quality-unavailable")
	}
	p.controls(t, "quality-unavailable-controls")
	p.h.assertNoForbidden(t)
}

func (p *peerWire) qualityLossRecovery(t *testing.T) {
	t.Helper()
	qualityRequireIsolated(t)
	link, err := netlink.LinkByName("under0")
	wireMust(t, err)
	// Loss affects only encrypted packets leaving the disposable gateway underlay.
	// Deterministic 100% loss avoids probabilistic success thresholds.
	q := netlink.NewNetem(netlink.QdiscAttrs{LinkIndex: link.Attrs().Index, Handle: netlink.MakeHandle(1, 0), Parent: netlink.HANDLE_ROOT}, netlink.NetemQdiscAttrs{Loss: 100, Limit: 64})
	wireMust(t, netlink.QdiscAdd(q))
	removed := false
	defer func() {
		if !removed {
			qualityRequireIsolated(t)
			wireMust(t, netlink.QdiscDel(q))
		}
	}()
	p.qualityRun(t, p.a.Address, "203.0.113.20:12080", "", "loss")
	qdiscs, err := netlink.QdiscList(link)
	wireMust(t, err)
	var drops uint32
	for _, installed := range qdiscs {
		if installed.Attrs().Handle == q.Handle && installed.Type() == "netem" {
			stats := installed.Attrs().Statistics
			if stats != nil && stats.Queue != nil {
				drops = stats.Queue.Drops
			}
		}
	}
	if drops == 0 {
		t.Fatal("loss timeout has no corresponding underlay qdisc drop evidence")
	}
	t.Logf("266-QUALITY-IMPAIRMENT drops=%d", drops)
	qualityRequireIsolated(t)
	wireMust(t, netlink.QdiscDel(q))
	removed = true
	p.qualityRun(t, p.a.Address, "203.0.113.20:12080", "", "persistent")
	p.controls(t, "quality-reconnected")
	t.Log("266-QUALITY-LOSS-RECOVERY-PASS deterministic-underlay-loss=100% recovered-TCP-UDP")
}

func qualityProbe(t *testing.T) {
	args := strings.Split(os.Getenv("RAZDACHA_WIRE_ARGS"), "|")
	if len(args) != 4 {
		t.Fatal("invalid quality arguments")
	}
	source, target, prefix, mode := args[0], args[1], args[2], args[3]
	identity := fmt.Sprintf("%s/%s/%d", source, target, time.Now().UnixNano())
	if source != "10.8.0.2" && source != "10.8.0.3" {
		t.Fatal("invalid quality source")
	}
	if target != "203.0.113.20:12080" && target != "203.0.113.10:12080" {
		t.Fatal("invalid quality target")
	}
	dial := func(proto string) net.Conn {
		d := net.Dialer{Timeout: time.Second}
		if proto == "tcp4" {
			d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(source)}
		} else {
			d.LocalAddr = &net.UDPAddr{IP: net.ParseIP(source)}
		}
		c, err := d.Dial(proto, target)
		wireMust(t, err)
		return c
	}
	if mode == "loss" {
		c := dial("udp4")
		defer c.Close()
		wireMust(t, c.SetDeadline(time.Now().Add(350*time.Millisecond)))
		_, err := c.Write([]byte("quality-loss\n"))
		wireMust(t, err)
		buf := make([]byte, 64)
		_, err = c.Read(buf)
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("100%% underlay loss did not produce a bounded receive timeout: %v", err)
		}
		fmt.Println("266-QUALITY-PROBE-PASS loss")
		return
	}
	for _, proto := range []string{"tcp4", "udp4"} {
		c := dial(proto)
		if proto == "udp4" {
			qualitySetDF(t, c, false)
		}
		if mode == "matrix" {
			for _, size := range []int{64, 1200, 1252, 1253, 1400} {
				fmt.Printf("266-QUALITY-MATRIX protocol=%s payload-bytes=%d\n", proto, size)
				qualityExchange(t, c, prefix, qualityPayload(size, identity+proto+"-"+strconv.Itoa(size)))
			}
			if proto == "udp4" {
				qualitySetDF(t, c, true)
				wireMust(t, c.SetDeadline(time.Now().Add(time.Second)))
				_, err := c.Write(qualityPayload(1253, "DF-above-MTU"))
				if !errors.Is(err, unix.EMSGSIZE) {
					t.Fatalf("DF datagram above WG MTU: got %v, want EMSGSIZE", err)
				}
				qualitySetDF(t, c, false)
				qualityExchange(t, c, prefix, qualityPayload(1253, "fragmentation-recovery"))
			}
		} else if mode == "persistent" {
			started := time.Now()
			for i := 0; i < 40; i++ {
				qualityExchange(t, c, prefix, qualityPayload(1200, fmt.Sprintf("%s/persistent-%s-%d", identity, proto, i)))
				time.Sleep(50 * time.Millisecond)
			}
			fmt.Printf("266-QUALITY-PERSISTENT protocol=%s frames=40 elapsed-ms=%d\n", proto, time.Since(started).Milliseconds())
		} else {
			t.Fatal("invalid quality mode")
		}
		wireMust(t, c.Close())
		// A separate socket proves reconnect rather than reuse of a cached connection.
		c = dial(proto)
		if proto == "udp4" {
			qualitySetDF(t, c, false)
		}
		qualityExchange(t, c, prefix, qualityPayload(64, identity+"/reconnect-"+proto))
		wireMust(t, c.Close())
	}
	fmt.Println("266-QUALITY-PROBE-PASS " + mode)
}

func qualitySetDF(t *testing.T, c net.Conn, df bool) {
	t.Helper()
	udp, ok := c.(*net.UDPConn)
	if !ok {
		t.Fatal("DF requires UDP")
	}
	raw, err := udp.SyscallConn()
	wireMust(t, err)
	mode := unix.IP_PMTUDISC_DONT
	if df {
		mode = unix.IP_PMTUDISC_DO
	}
	var socketErr error
	wireMust(t, raw.Control(func(fd uintptr) { socketErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, mode) }))
	wireMust(t, socketErr)
}

func qualityExchange(t *testing.T, c net.Conn, prefix string, payload []byte) {
	t.Helper()
	wireMust(t, c.SetDeadline(time.Now().Add(time.Second)))
	n, err := c.Write(payload)
	wireMust(t, err)
	if n != len(payload) {
		t.Fatal("short quality write")
	}
	expected := append([]byte(prefix), payload...)
	actual := make([]byte, len(expected))
	if _, ok := c.(*net.UDPConn); ok {
		buf := make([]byte, len(expected)+1)
		n, err = c.Read(buf)
		wireMust(t, err)
		actual = buf[:n]
	} else {
		_, err = io.ReadFull(c, actual)
		wireMust(t, err)
	}
	if !qualityExact(actual, expected) {
		t.Fatal("quality payload or checksum mismatch")
	}
}
