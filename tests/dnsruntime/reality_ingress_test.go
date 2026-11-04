package dnsruntime

import (
	"bufio"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/miekg/dns"
)

// This capability fixture deliberately does not use the production generator.
// It proves the pinned engine's hooks before a new ingress can be integrated.
func TestRealityIngressIdentity(t *testing.T) {
	if os.Getenv("RAZDACHA_REALITY_TEST") != "1" {
		t.Skip("set RAZDACHA_REALITY_TEST=1 and RAZDACHA_TEST_SINGBOX for the isolated proof")
	}
	realityNamespace(t)
	binary := os.Getenv("RAZDACHA_TEST_SINGBOX")
	if !filepath.IsAbs(binary) {
		t.Fatal("RAZDACHA_TEST_SINGBOX must be absolute")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	version, err := exec.CommandContext(ctx, binary, "version").Output()
	cancel()
	if err != nil || !strings.Contains(string(version), "sing-box version 1.12.25\n") {
		t.Fatal("expected the pinned sing-box 1.12.25 executable")
	}
	target := realityHandshakeTarget(t)
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	realityMust(t, err)
	shortID := make([]byte, 8)
	_, err = rand.Read(shortID)
	realityMust(t, err)
	first, second, unnamed := uuid.NewString(), uuid.NewString(), uuid.NewString()
	serverPort := realityPort(t)
	sinks := []*realitySink{realityNewSink(t, "first"), realityNewSink(t, "second"), realityNewSink(t, "fallback")}
	resolvers := []*realityResolver{realityDNS(t, "192.0.2.11"), realityDNS(t, "192.0.2.22"), realityDNS(t, "192.0.2.99")}
	// Prove the fallback observers are responsive before asserting their counts.
	canary, err := realityExchange("tcp", sinks[2].port, []byte("canary"))
	realityMust(t, err)
	if string(canary) != "fallback:canary" {
		t.Fatal("fallback sink positive control failed")
	}
	question := new(dns.Msg)
	question.SetQuestion("canary.fixture.example.", dns.TypeA)
	answer, _, err := (&dns.Client{Timeout: time.Second}).Exchange(question, fmt.Sprintf("127.0.0.1:%d", resolvers[2].port))
	realityMust(t, err)
	if len(answer.Answer) != 1 || answer.Answer[0].(*dns.A).A.String() != resolvers[2].answer {
		t.Fatal("fallback resolver positive control failed")
	}
	server := map[string]any{
		"log": map[string]any{"disabled": true},
		"inbounds": []any{map[string]any{
			"type": "vless", "tag": "alternative", "listen": "127.0.0.1", "listen_port": serverPort,
			"users": []any{map[string]any{"name": "client-first", "uuid": first}, map[string]any{"name": "client-second", "uuid": second}, map[string]any{"name": "", "uuid": unnamed}},
			"tls": map[string]any{"enabled": true, "server_name": "fixture.example", "reality": map[string]any{
				"enabled": true, "private_key": base64.RawURLEncoding.EncodeToString(key.Bytes()), "short_id": []string{hex.EncodeToString(shortID)},
				"handshake": map[string]any{"server": "127.0.0.1", "server_port": target},
			}},
		}},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route": map[string]any{"final": "direct", "rules": []any{
			map[string]any{"inbound": []string{"alternative"}, "port": []int{53}, "action": "hijack-dns"},
			realityRoute("client-first", sinks[0].port), realityRoute("client-second", sinks[1].port),
			map[string]any{"inbound": []string{"alternative"}, "action": "reject"},
		}},
		"dns": map[string]any{"strategy": "ipv4_only", "disable_cache": true, "final": "fallback", "servers": []any{
			realityDNSServer("first", resolvers[0].port), realityDNSServer("second", resolvers[1].port), realityDNSServer("fallback", resolvers[2].port),
		}, "rules": []any{
			map[string]any{"inbound": []string{"alternative"}, "auth_user": []string{"client-first"}, "server": "first"},
			map[string]any{"inbound": []string{"alternative"}, "auth_user": []string{"client-second"}, "server": "second"},
			map[string]any{"inbound": []string{"alternative"}, "action": "reject"},
		}},
	}
	serverProcess := realityStart(t, binary, server, serverPort)
	publicKey := base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	clients := make([][2]int, 2)
	for index, credential := range []string{first, second} {
		clients[index] = realityClient(t, binary, serverPort, sinks[2].port, credential, publicKey, hex.EncodeToString(shortID), true)
	}
	for index, ports := range clients {
		for _, network := range []string{"tcp", "udp"} {
			t.Run(fmt.Sprintf("client-%d/%s-route", index+1, network), func(t *testing.T) {
				payload := []byte(uuid.NewString())
				got, err := realityExchange(network, ports[0], payload)
				realityMust(t, err)
				if string(got) != sinks[index].name+":"+string(payload) {
					t.Fatal("authenticated user reached the wrong sink")
				}
			})
			t.Run(fmt.Sprintf("client-%d/%s-dns", index+1, network), func(t *testing.T) {
				query := new(dns.Msg)
				query.SetQuestion(uuid.NewString()+".fixture.example.", dns.TypeA)
				response, _, err := (&dns.Client{Net: network, Timeout: 3 * time.Second}).Exchange(query, fmt.Sprintf("127.0.0.1:%d", ports[1]))
				realityMust(t, err)
				if response.Rcode != dns.RcodeSuccess || len(response.Answer) != 1 {
					t.Fatal("expected a successful authenticated DNS response")
				}
				answer, ok := response.Answer[0].(*dns.A)
				if !ok || answer.A.String() != resolvers[index].answer {
					t.Fatal("DNS lost authenticated user scope")
				}
			})
		}
	}
	wrongKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	realityMust(t, err)
	for _, negative := range []struct {
		name, credential, key string
		reality               bool
	}{
		{"unknown-credential", uuid.NewString(), publicKey, true},
		{"missing-user-name", unnamed, publicKey, true},
		{"plain-vless", first, publicKey, false},
		{"wrong-reality-key", first, base64.RawURLEncoding.EncodeToString(wrongKey.PublicKey().Bytes()), true},
	} {
		t.Run(negative.name, func(t *testing.T) {
			ports := realityClient(t, binary, serverPort, sinks[2].port, negative.credential, negative.key, hex.EncodeToString(shortID), negative.reality)
			for _, network := range []string{"tcp", "udp"} {
				payload := []byte(uuid.NewString())
				got, err := realityExchange(network, ports[0], payload)
				if err == nil || len(got) != 0 {
					t.Fatalf("%s unexpectedly accepted unauthorized traffic", network)
				}
				for _, sink := range sinks {
					if sink.seen(string(payload)) {
						t.Fatalf("%s reached a sink despite client-side failure", network)
					}
				}
				question := new(dns.Msg)
				question.SetQuestion(uuid.NewString()+".fixture.example.", dns.TypeA)
				response, _, err := (&dns.Client{Net: network, Timeout: time.Second}).Exchange(question, fmt.Sprintf("127.0.0.1:%d", ports[1]))
				if err == nil && response.Rcode == dns.RcodeSuccess {
					t.Fatalf("%s accepted unauthorized DNS", network)
				}
			}
		})
	}
	if sinks[2].count() != 1 || resolvers[2].requests.Load() != 1 {
		t.Fatal("unexpected default-route or default-resolver fallback")
	}
	// Prove shutdown closes an established proxied TCP flow, without claiming
	// that the product already implements credential revocation or guards.
	connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", clients[0][0]), 2*time.Second)
	realityMust(t, err)
	defer connection.Close()
	realityMust(t, connection.SetDeadline(time.Now().Add(3*time.Second)))
	_, err = connection.Write([]byte("established\n"))
	realityMust(t, err)
	buffer := make([]byte, len("first:established\n"))
	_, err = io.ReadFull(connection, buffer)
	realityMust(t, err)
	if string(buffer) != "first:established\n" {
		t.Fatal("established flow has no positive sink proof")
	}
	serverProcess.stop(t)
	realityMust(t, connection.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, err = connection.Read(buffer)
	if err == nil {
		t.Fatal("server shutdown left the established flow readable")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("server shutdown did not close the established flow within deadline")
	}
}

func realityRoute(user string, port int) map[string]any {
	return map[string]any{"inbound": []string{"alternative"}, "auth_user": []string{user}, "action": "route", "outbound": "direct", "override_address": "127.0.0.1", "override_port": port}
}

func realityDNSServer(tag string, port int) map[string]any {
	return map[string]any{"type": "udp", "tag": tag, "server": "127.0.0.1", "server_port": port}
}

func realityClient(t *testing.T, binary string, serverPort, fallbackPort int, credential, key, shortID string, enabled bool) [2]int {
	t.Helper()
	ports := [2]int{realityPort(t), realityPort(t)}
	outbound := map[string]any{"type": "vless", "tag": "proxy", "server": "127.0.0.1", "server_port": serverPort, "uuid": credential}
	if enabled {
		outbound["tls"] = map[string]any{"enabled": true, "server_name": "fixture.example", "utls": map[string]any{"enabled": true, "fingerprint": "chrome"}, "reality": map[string]any{"enabled": true, "public_key": key, "short_id": shortID}}
	}
	config := map[string]any{"log": map[string]any{"disabled": true}, "inbounds": []any{
		map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": ports[0], "override_address": "127.0.0.1", "override_port": fallbackPort},
		map[string]any{"type": "direct", "listen": "127.0.0.1", "listen_port": ports[1], "override_address": "198.51.100.53", "override_port": 53},
	}, "outbounds": []any{outbound}, "route": map[string]any{"final": "proxy"}}
	realityStart(t, binary, config, ports[0])
	return ports
}

type realityProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
}

func realityStart(t *testing.T, binary string, config map[string]any, port int) *realityProcess {
	t.Helper()
	directory := t.TempDir()
	realityMust(t, os.Chmod(directory, 0o700))
	config["log"] = map[string]any{"level": "error"}
	content, err := json.Marshal(config)
	realityMust(t, err)
	path := filepath.Join(directory, "config.json")
	realityMust(t, os.WriteFile(path, content, 0o600))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	check := exec.CommandContext(ctx, binary, "check", "-D", directory, "-c", path)
	err = check.Run()
	cancel()
	if err != nil {
		t.Fatal("pinned runtime rejected the private fixture configuration")
	}
	process := &realityProcess{cmd: exec.Command(binary, "run", "-D", directory, "-c", path), done: make(chan struct{})}
	logPath := filepath.Join(directory, "runtime.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0o600)
	realityMust(t, err)
	process.cmd.Stdout, process.cmd.Stderr = logFile, logFile
	t.Cleanup(func() {
		_ = logFile.Close()
		if t.Failed() {
			content, _ := os.ReadFile(logPath)
			redacted := regexp.MustCompile(`[a-fA-F0-9]{8}-[a-fA-F0-9-]{27,}|[A-Za-z0-9_-]{43}`).ReplaceAllString(string(content), "[redacted]")
			t.Log("fixture runtime diagnostics:", redacted)
		}
	})
	realityMust(t, process.cmd.Start())
	go func() { _ = process.cmd.Wait(); close(process.done) }()
	t.Cleanup(func() { process.stop(t) })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return process
		}
		select {
		case <-process.done:
			t.Fatal("fixture runtime exited before becoming ready")
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture runtime listener did not become ready")
	return nil
}

func (p *realityProcess) stop(t *testing.T) {
	t.Helper()
	p.once.Do(func() {
		_ = p.cmd.Process.Signal(os.Interrupt)
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
			t.Error("fixture runtime required forced shutdown")
		}
	})
}

func realityMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func realityPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	realityMust(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	realityMust(t, listener.Close())
	return port
}

func realityExchange(network string, port int, payload []byte) ([]byte, error) {
	connection, err := net.DialTimeout(network+"4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	if err = connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, err
	}
	request := payload
	if network == "tcp" {
		request = append(append([]byte(nil), payload...), '\n')
	}
	if _, err = connection.Write(request); err != nil {
		return nil, err
	}
	if network == "tcp" {
		response, err := bufio.NewReader(connection).ReadBytes('\n')
		return []byte(strings.TrimSuffix(string(response), "\n")), err
	}
	buffer := make([]byte, 4096)
	count, err := connection.Read(buffer)
	return []byte(strings.TrimSuffix(string(buffer[:count]), "\n")), err
}

type realitySink struct {
	name     string
	port     int
	mu       sync.Mutex
	payloads map[string]bool
}

func (s *realitySink) seen(payload string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.payloads[payload]
}
func (s *realitySink) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.payloads) }
func (s *realitySink) response(payload []byte) []byte {
	s.mu.Lock()
	s.payloads[string(payload)] = true
	s.mu.Unlock()
	return append(append([]byte(s.name+":"), payload...), '\n')
}

func realityNewSink(t *testing.T, name string) *realitySink {
	t.Helper()
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	realityMust(t, err)
	sink := &realitySink{name: name, port: tcp.Addr().(*net.TCPAddr).Port, payloads: make(map[string]bool)}
	udp, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", sink.port))
	realityMust(t, err)
	t.Cleanup(func() { _ = tcp.Close(); _ = udp.Close() })
	go func() {
		for {
			connection, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(30 * time.Second))
				reader := bufio.NewReader(connection)
				for {
					payload, err := reader.ReadSlice('\n')
					if err != nil {
						return
					}
					if _, err := connection.Write(sink.response(payload[:len(payload)-1])); err != nil {
						return
					}
				}
			}()
		}
	}()
	go func() {
		buffer := make([]byte, 4096)
		for {
			count, address, err := udp.ReadFrom(buffer)
			if err != nil {
				return
			}
			_, _ = udp.WriteTo(sink.response(buffer[:count]), address)
		}
	}()
	return sink
}

type realityResolver struct {
	port     int
	answer   string
	requests atomic.Int32
}

func realityDNS(t *testing.T, answer string) *realityResolver {
	t.Helper()
	listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
	realityMust(t, err)
	resolver := &realityResolver{port: listener.LocalAddr().(*net.UDPAddr).Port, answer: answer}
	server := &dns.Server{PacketConn: listener, Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, query *dns.Msg) {
		resolver.requests.Add(1)
		response := new(dns.Msg)
		response.SetReply(query)
		response.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: query.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}, A: net.ParseIP(answer)}}
		_ = writer.WriteMsg(response)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown(); _ = listener.Close() })
	return resolver
}

func realityHandshakeTarget(t *testing.T) int {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	realityMust(t, err)
	certificate, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"fixture.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}, &x509.Certificate{SerialNumber: big.NewInt(1)}, &private.PublicKey, private)
	realityMust(t, err)
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	realityMust(t, err)
	keyPair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	realityMust(t, err)
	listener, err := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{keyPair}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, CurvePreferences: []tls.CurveID{tls.X25519}, NextProtos: []string{"h2", "http/1.1"}})
	realityMust(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
				_, _ = io.Copy(io.Discard, connection)
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}
