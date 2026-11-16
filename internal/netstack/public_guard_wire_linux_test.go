//go:build linux

package netstack

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/ingressguard"
	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// TestPublicClientGuardWire is opt-in and self-executes into new network/mount
// namespaces before any mutation. It never uses host interfaces, routes or keys.
func TestPublicClientGuardWire(t *testing.T) {
	if os.Getenv("RAZDACHA_PUBLIC_GUARD_TEST") != "1" {
		t.Skip("set RAZDACHA_PUBLIC_GUARD_TEST=1 for the isolated Linux guard fixture")
	}
	if os.Getenv("RAZDACHA_PUBLIC_GUARD_CHILD") != "1" {
		if os.Geteuid() != 0 {
			t.Fatal("isolated guard fixture requires root")
		}
		parent, err := os.Readlink("/proc/self/ns/net")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPublicClientGuardWire$", "-test.v", "-test.timeout=20s")
		command.Env = append(os.Environ(), "RAZDACHA_PUBLIC_GUARD_CHILD=1", "RAZDACHA_PUBLIC_GUARD_PARENT="+parent)
		command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWNET | unix.CLONE_NEWNS}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated guard fixture failed: %v\n%s", err, output)
		}
		if !strings.Contains(string(output), "--- PASS: TestPublicClientGuardWire") {
			t.Fatal("isolated fixture did not report its passing test")
		}
		t.Log(string(output))
		return
	}
	parent := os.Getenv("RAZDACHA_PUBLIC_GUARD_PARENT")
	current, err := os.Readlink("/proc/self/ns/net")
	if err != nil || parent == "" || current == parent {
		t.Fatal("refusing guard mutation outside a fresh network namespace")
	}
	links, err := netlink.LinkList()
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Attrs().Name != "lo" {
		t.Fatal("guard fixture namespace must contain only loopback")
	}
	if err := netlink.LinkSetUp(links[0]); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := SetPublicClientGuard(nil, false); err != nil {
			t.Error(err)
		}
	}()

	old, unrelated, next := publicEcho(t, "tcp4", "127.0.0.1:0"), publicEcho(t, "tcp4", "127.0.0.1:0"), publicEcho(t, "tcp4", "127.0.0.1:0")
	probePublic(t, old, true)
	probePublic(t, unrelated, true)
	established, err := net.DialTimeout("tcp4", old, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer established.Close()
	if err := established.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := established.Write([]byte("initial\n")); err != nil {
		t.Fatal(err)
	}
	if got, err := bufio.NewReader(established).ReadString('\n'); err != nil || got != "initial\n" {
		t.Fatal("established connection lacked positive proof", err)
	}
	oldEndpoint, nextEndpoint := publicEndpoint(t, old), publicEndpoint(t, next)
	if err := SetPublicClientGuard([]ingressguard.Endpoint{oldEndpoint}, true); err != nil {
		t.Fatal(err)
	}
	probePublic(t, old, false)
	probePublic(t, unrelated, true)
	if err := established.SetDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, _ = established.Write([]byte("blocked\n"))
	buffer := make([]byte, 32)
	if _, err := established.Read(buffer); err == nil {
		t.Fatal("established traffic bypassed public guard")
	}
	if err := SetPublicClientGuard([]ingressguard.Endpoint{oldEndpoint, nextEndpoint}, true); err != nil {
		t.Fatal(err)
	}
	probePublic(t, old, false)
	probePublic(t, next, false)
	probePublic(t, unrelated, true)
	if err := SetPublicClientGuard([]ingressguard.Endpoint{nextEndpoint}, true); err != nil {
		t.Fatal(err)
	}
	probePublic(t, old, true)
	probePublic(t, next, false)
	if err := SetPublicClientGuard([]ingressguard.Endpoint{{Address: "bad", Port: 1}}, false); err == nil {
		t.Fatal("invalid release plan accepted")
	}
	probePublic(t, next, false)
	if err := SetPublicClientGuard(nil, false); err != nil {
		t.Fatal(err)
	}
	probePublic(t, next, true)
	if err := SetPublicClientGuard([]ingressguard.Endpoint{{Address: "0.0.0.0", Port: oldEndpoint.Port}}, true); err != nil {
		t.Fatal(err)
	}
	probePublic(t, old, false)
	probePublic(t, unrelated, true)
	if err := SetPublicClientGuard([]ingressguard.Endpoint{{Address: "127.0.0.2", Port: oldEndpoint.Port}}, true); err != nil {
		t.Fatal(err)
	}
	probePublic(t, old, true)

	connection, err := nftables.New()
	if err != nil {
		t.Fatal(err)
	}
	chains, err := connection.ListChains()
	if err != nil {
		t.Fatal(err)
	}
	for _, chain := range chains {
		if chain.Table.Name == PublicClientGuardTable && (chain.Hooknum == nil || *chain.Hooknum != *nftables.ChainHookInput || chain.Priority == nil || *chain.Priority != -310) {
			t.Fatal("public barrier is not local early INPUT")
		}
	}
	t.Run("ipv6", func(t *testing.T) {
		listener, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			t.Skip("IPv6 loopback unavailable in this disposable namespace")
		}
		address := listener.Addr().String()
		listener.Close()
		address = publicEcho(t, "tcp6", address)
		endpoint := publicEndpoint(t, address)
		probePublic(t, address, true)
		if err := SetPublicClientGuard([]ingressguard.Endpoint{endpoint}, true); err != nil {
			t.Fatal(err)
		}
		probePublic(t, address, false)
		probePublic(t, unrelated, true)
		if err := SetPublicClientGuard(nil, false); err != nil {
			t.Fatal(err)
		}
		probePublic(t, address, true)
	})
}

func publicEndpoint(t *testing.T, address string) ingressguard.Endpoint {
	t.Helper()
	resolved, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	return ingressguard.Endpoint{Address: resolved.IP.String(), Port: uint16(resolved.Port)}
}

func publicEcho(t *testing.T, network, address string) string {
	t.Helper()
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	var mutex sync.Mutex
	var connections []net.Conn
	group.Add(1)
	go func() {
		defer group.Done()
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			mutex.Lock()
			connections = append(connections, connection)
			mutex.Unlock()
			group.Add(1)
			go func() {
				defer group.Done()
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		mutex.Lock()
		for _, connection := range connections {
			connection.Close()
		}
		mutex.Unlock()
		group.Wait()
	})
	return listener.Addr().String()
}

func probePublic(t *testing.T, address string, allowed bool) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
	if !allowed {
		if err == nil {
			connection.Close()
			t.Fatal("closed public listener accepted a new connection")
		}
		return
	}
	if err != nil {
		t.Fatal("unrelated or released listener unavailable", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("probe\n")); err != nil {
		t.Fatal(err)
	}
	if got, err := bufio.NewReader(connection).ReadString('\n'); err != nil || got != "probe\n" {
		t.Fatal("allowed listener did not exchange data", err)
	}
}
