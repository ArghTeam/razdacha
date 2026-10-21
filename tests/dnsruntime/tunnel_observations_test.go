package dnsruntime

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/experimental/clashapi"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

type observationCertificates struct {
	adapter.CertificateStore
	pool *x509.CertPool
}

func (s observationCertificates) Pool() *x509.CertPool { return s.pool }

// This controlled dialer is not a WireGuard endpoint. It proves pinned URL-test
// behavior without contacting public DNS, providers, or installed networking.
type observationDialer struct {
	adapter.Outbound
	address  string
	failure  error
	domains  atomic.Int32
	literals atomic.Int32
}

func (*observationDialer) Tag() string { return "controlled-observation" }

func (d *observationDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if destination.IsFqdn() {
		d.domains.Add(1)
		return nil, errors.New("controlled diagnostic hostname DNS rejection")
	}
	d.literals.Add(1)
	if d.failure != nil {
		return nil, d.failure
	}
	if network != "tcp" || destination.String() != d.address {
		return nil, fmt.Errorf("unexpected controlled destination: %s %s", network, destination)
	}
	return (&net.Dialer{}).DialContext(ctx, network, d.address)
}

var _ N.Dialer = (*observationDialer)(nil)

func observationTLS(t *testing.T, handler http.Handler) (*httptest.Server, context.Context) {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = stdlog.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	return server, service.ContextWith[adapter.CertificateStore](context.Background(), observationCertificates{pool: pool})
}

func TestPinnedTunnelObservationURLTest(t *testing.T) {
	t.Run("literal TLS HEAD does not require diagnostic hostname or follow redirects", func(t *testing.T) {
		var heads, redirected atomic.Int32
		server, ctx := observationTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead {
				t.Errorf("method = %s, want HEAD", r.Method)
			}
			if r.URL.Path != "/probe" {
				redirected.Add(1)
			}
			heads.Add(1)
			w.Header().Set("Location", "https://diagnostic.invalid/redirect")
			w.WriteHeader(http.StatusMovedPermanently)
		}))
		dialer := &observationDialer{address: server.Listener.Addr().String()}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if _, err := urltest.URLTest(ctx, "https://diagnostic.invalid/probe", dialer); err == nil {
			t.Fatal("controlled hostname DNS rejection unexpectedly succeeded")
		}
		if _, err := urltest.URLTest(ctx, server.URL+"/probe", dialer); err != nil {
			t.Fatalf("literal-IP verified HTTPS must survive diagnostic DNS rejection: %v", err)
		}
		if dialer.domains.Load() != 1 || dialer.literals.Load() != 1 || heads.Load() != 1 || redirected.Load() != 0 {
			t.Fatalf("unexpected DNS/dial/HEAD/redirect counts: %d/%d/%d/%d", dialer.domains.Load(), dialer.literals.Load(), heads.Load(), redirected.Load())
		}
	})

	t.Run("untrusted TLS certificate fails", func(t *testing.T) {
		server, _ := observationTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("untrusted TLS reached HTTP handler")
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := urltest.URLTest(ctx, server.URL, &observationDialer{address: server.Listener.Addr().String()})
		var unknown x509.UnknownAuthorityError
		if !errors.As(err, &unknown) {
			t.Fatalf("want certificate trust failure, got %v", err)
		}
	})

	t.Run("twelve second valid response fits fifteen second budget", func(t *testing.T) {
		server, ctx := observationTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			timer := time.NewTimer(12 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				w.WriteHeader(http.StatusNoContent)
			case <-r.Context().Done():
			}
		}))
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		delay, err := urltest.URLTest(ctx, server.URL, &observationDialer{address: server.Listener.Addr().String()})
		if err != nil || delay < 12000 || delay >= 15000 {
			t.Fatalf("12s response under 15s budget: delay=%d error=%v", delay, err)
		}
	})
}

type observationOutbounds struct {
	adapter.OutboundManager
	outbound adapter.Outbound
}

func (m observationOutbounds) Outbound(tag string) (adapter.Outbound, bool) {
	return m.outbound, tag == "controlled-observation"
}

// The actual pinned controller deliberately collapses unrelated failures to the
// same 503 response. That response cannot establish WireGuard transport failure.
func TestPinnedTunnelObservationOpaqueClashFailure(t *testing.T) {
	tlsServer, _ := observationTLS(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("untrusted TLS reached HTTP handler")
	}))
	var commonBody string
	for _, test := range []struct {
		name, target string
		failure      error
	}{
		{name: "hostname DNS rejection", target: "https://diagnostic.invalid/"},
		{name: "TCP dial failure", target: tlsServer.URL, failure: errors.New("controlled TCP connection failure")},
		{name: "TLS trust failure", target: tlsServer.URL},
	} {
		t.Run(test.name, func(t *testing.T) {
			dialer := &observationDialer{address: tlsServer.Listener.Addr().String(), failure: test.failure}
			ctx := service.ContextWith[adapter.OutboundManager](context.Background(), observationOutbounds{outbound: dialer})
			// Reserve then release an ephemeral loopback port; a collision fails
			// Start rather than reusing or changing any existing listener.
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			server, err := clashapi.NewServer(ctx, log.NewNOPFactory(), option.ClashAPIOptions{ExternalController: address, Secret: "disposable-observation-secret"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close() })
			if err := server.Start(adapter.StartStateStarted); err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequest(http.MethodGet, "http://"+address+"/proxies/controlled-observation/delay?timeout=15000&url="+url.QueryEscape(test.target), nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer disposable-observation-secret")
			client := &http.Client{Timeout: 3 * time.Second}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "An error occurred in the delay test") {
				t.Fatalf("unexpected controller failure: status=%d body=%s", response.StatusCode, body)
			}
			if commonBody == "" {
				commonBody = string(body)
			} else if string(body) != commonBody {
				t.Fatalf("controller leaked a distinct failure classification: %s", body)
			}
		})
	}
}
