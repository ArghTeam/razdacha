//go:build linux

package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/miekg/dns"
)

// This opt-in exercise reuses the isolated reciprocal kernel WG topology. Its
// profile-derived REALITY wrapper uses only the direct synthetic underlay.
func realityCoexistExercise(t *testing.T) {
	qualityRequireIsolated(t)
	if !wireQualityEnabled() {
		t.Fatal("REALITY coexistence requires the encrypted WG quality opt-in")
	}
	wireMust(t, os.Setenv("RAZDACHA_WIRE_PEER_CASE", "grant"))
	p := newPeerWire(t)
	// testing.TempDir's numbered child uses 0777 subject to umask. The live
	// manifest requires an explicitly private parent, as the installed DB has.
	wireMust(t, os.Chmod(p.dir, 0o700))
	p.controls(t, "coexist-before")
	before := qualityPeerStats(t)
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	target.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	target.StartTLS()
	t.Cleanup(target.Close)
	settings, err := p.st.Settings(p.h.ctx)
	wireMust(t, err)
	settings.RealityEnabled = true
	settings.RealityListenPort = coexistPort(t)
	settings.RealityServerName = "coexist.fixture.example"
	settings.RealityHandshakeServer = "127.0.0.1"
	settings.RealityHandshakePort = target.Listener.Addr().(*net.TCPAddr).Port
	settings.EndpointHost = "198.51.100.1"
	wireMust(t, p.st.SaveSettings(p.h.ctx, settings))
	enabled := true
	p.a, err = p.st.PatchPeer(p.h.ctx, p.a.ID, store.PeerPatch{RealityEnabled: &enabled})
	wireMust(t, err)
	p.gate.store = p.st
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	profile, err := p.gate.RealityClientConfig(p.h.ctx, p.st, p.a.ID)
	wireMust(t, err)
	var client map[string]any
	wireMust(t, json.Unmarshal(profile, &client))
	outbounds, ok := client["outbounds"].([]any)
	if !ok || len(outbounds) != 1 {
		t.Fatal("delivered client profile lacks its single proxy outbound")
	}
	proxy, ok := outbounds[0].(map[string]any)
	if !ok || proxy["type"] != "vless" || proxy["server"] != settings.EndpointHost {
		t.Fatal("delivered client profile has an unexpected proxy endpoint")
	}
	// The existing client default route is WG. This fixture-only binding selects
	// its physical documentation-address underlay, independently of WG ingress.
	proxy["bind_interface"] = "under1"
	route, ok := client["route"].(map[string]any)
	if !ok {
		t.Fatal("delivered client profile lacks routing options")
	}
	route["auto_detect_interface"] = false
	client["inbounds"] = []any{
		map[string]any{"type": "direct", "tag": "coexist-data", "listen": "127.0.0.1", "listen_port": 18080, "override_address": "203.0.113.20", "override_port": 12080},
		map[string]any{"type": "direct", "tag": "coexist-dns", "listen": "127.0.0.1", "listen_port": 18053, "override_address": "10.8.0.1", "override_port": 53},
	}
	encoded, err := json.Marshal(client)
	wireMust(t, err)
	path := filepath.Join(p.dir, "coexist-client.json")
	wireMust(t, os.WriteFile(path, encoded, 0o600))
	command := wireExec(t, p.h.client, "reality-coexist-client", path)
	input, err := command.StdinPipe()
	wireMust(t, err)
	output, err := command.StdoutPipe()
	wireMust(t, err)
	command.Stderr = os.Stderr
	wireMust(t, command.Start())
	done := make(chan error, 1)
	t.Cleanup(func() { _ = input.Close(); _ = command.Process.Kill() })
	ready := make(chan struct{})
	result := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			line := scanner.Text()
			_, _ = fmt.Fprintln(os.Stderr, line)
			if line == "297-REALITY-COEXIST-READY" {
				close(ready)
			}
			if strings.HasPrefix(line, "297-REALITY-COEXIST-CLIENT-PASS ") {
				result <- line
			}
		}
		done <- command.Wait()
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal("REALITY client exited before successful protocol controls", err)
	case <-time.After(15 * time.Second):
		t.Fatal("REALITY client controls timed out")
	}
	p.qualityExercise(t, "selective")
	after := qualityPeerStats(t)
	if after.PublicKey != before.PublicKey || after.ReceiveBytes <= before.ReceiveBytes || after.TransmitBytes <= before.TransmitBytes {
		t.Fatal("concurrent traffic did not preserve and advance the encrypted WG peer")
	}
	_, err = io.WriteString(input, "STOP\n")
	wireMust(t, err)
	wireMust(t, input.Close())
	select {
	case err := <-done:
		wireMust(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("REALITY client did not finish after concurrent WG traffic")
	}
	select {
	case line := <-result:
		t.Log(line)
	default:
		t.Fatal("REALITY client lacks repeated successful protocol evidence")
	}
	p.h.assertNoForbidden(t)
	t.Logf("297-WG-REALITY-COEXIST-PASS encrypted-rx-delta=%d encrypted-tx-delta=%d", after.ReceiveBytes-before.ReceiveBytes, after.TransmitBytes-before.TransmitBytes)
}

func coexistPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	wireMust(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	wireMust(t, listener.Close())
	return port
}

func realityCoexistClient(t *testing.T) {
	qualityRequireIsolated(t)
	args := strings.Split(os.Getenv("RAZDACHA_WIRE_ARGS"), "|")
	if !wireQualityEnabled() || len(args) != 1 || !filepath.IsAbs(args[0]) {
		t.Fatal("invalid isolated coexistence client invocation")
	}
	underlay, err := net.InterfaceByName("under1")
	wireMust(t, err)
	addresses, err := underlay.Addrs()
	wireMust(t, err)
	ownedIPv4 := false
	for _, address := range addresses {
		if address.String() == "198.51.100.2/30" {
			ownedIPv4 = true
		}
	}
	if !ownedIPv4 {
		t.Fatal("coexistence client requires the owned direct underlay")
	}
	path := args[0]
	log, err := os.OpenFile(filepath.Join(filepath.Dir(path), "coexist-client.log"), os.O_CREATE|os.O_WRONLY, 0o600)
	wireMust(t, err)
	defer log.Close()
	command := exec.Command(os.Getenv("RAZDACHA_WIRE_BIN"), "run", "-D", filepath.Dir(path), "-c", path)
	command.Stdout, command.Stderr = log, log
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	wireMust(t, command.Start())
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
	deadline := time.Now().Add(5 * time.Second)
	for {
		connection, e := net.DialTimeout("tcp4", "127.0.0.1:18080", 50*time.Millisecond)
		if e == nil {
			_ = connection.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated REALITY wrapper did not listen")
		}
		select {
		case <-done:
			t.Fatal("isolated REALITY wrapper exited", runtimeErr)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(os.Stdin).ReadString('\n'); stop <- line }()
	limit := time.NewTimer(30 * time.Second)
	defer limit.Stop()
	for batches := 1; ; batches++ {
		coexistMatrix(t, batches)
		if batches == 1 {
			fmt.Println("297-REALITY-COEXIST-READY")
		}
		select {
		case line := <-stop:
			if line != "STOP\n" || batches < 3 {
				t.Fatal("coexistence ended without repeated successful protocol controls")
			}
			fmt.Printf("297-REALITY-COEXIST-CLIENT-PASS protocol-batches=%d\n", batches)
			return
		case <-time.After(50 * time.Millisecond):
		case <-limit.C:
			t.Fatal("bounded REALITY coexistence period expired")
		}
	}
}

func coexistMatrix(t *testing.T, batch int) {
	t.Helper()
	for _, network := range []string{"tcp4", "udp4"} {
		connection, err := net.DialTimeout(network, "127.0.0.1:18080", time.Second)
		wireMust(t, err)
		qualityExchange(t, connection, "", []byte("coexist-reality-"+network+"-"+strconv.Itoa(batch)))
		wireMust(t, connection.Close())
	}
	for _, network := range []string{"tcp", "udp"} {
		question := new(dns.Msg)
		question.SetQuestion(fmt.Sprintf("coexist-%d.fixture.example.", batch), dns.TypeA)
		answer, _, err := (&dns.Client{Net: network, Timeout: time.Second}).Exchange(question, "127.0.0.1:18053")
		wireMust(t, err)
		if answer.Rcode != dns.RcodeSuccess || len(answer.Answer) != 1 || !strings.Contains(answer.Answer[0].String(), "203.0.113.20") {
			t.Fatal("REALITY DNS did not return the owned gateway resolver answer")
		}
	}
}
