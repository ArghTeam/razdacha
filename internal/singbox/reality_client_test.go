package singbox

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

func TestRealityClientProfileCapturesDNSAndIPv6WithoutDirectFallback(t *testing.T) {
	settings := store.DefaultSettings()
	settings.EndpointHost = "gateway.fixture.example"
	settings.RealityListenPort = 8443
	settings.RealityServerName = "fixture.example"
	data, err := RealityClientProfile(settings, "00000000-0000-4000-8000-000000000001", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "0011223344556677")
	if err != nil {
		t.Fatal(err)
	}
	var opts struct {
		Inbounds []struct {
			Type         string
			Address      []string
			RouteAddress []string `json:"route_address"`
			AutoRoute    bool     `json:"auto_route"`
			StrictRoute  bool     `json:"strict_route"`
			MTU          int
		}
		Outbounds []struct {
			Type           string
			DomainResolver struct{ Server string } `json:"domain_resolver"`
		}
		DNS struct {
			Final   string
			Servers []struct{ Tag, Server, Detour string }
			Rules   []map[string]any
		}
		Route struct {
			Final string
			Rules []map[string]any
		}
	}
	if err := json.Unmarshal(data, &opts); err != nil {
		t.Fatal(err)
	}
	if len(opts.Inbounds) != 1 || !opts.Inbounds[0].AutoRoute || !opts.Inbounds[0].StrictRoute || opts.Inbounds[0].MTU != 1280 {
		t.Fatal("missing complete client capture")
	}
	if strings.Join(opts.Inbounds[0].RouteAddress, ",") != "0.0.0.0/0,::/0" {
		t.Fatal("IPv6 could escape capture")
	}
	if len(opts.Outbounds) != 1 || opts.Outbounds[0].Type != "vless" || opts.Route.Final != "proxy" {
		t.Fatal("non-proxy fallback present")
	}
	if opts.Outbounds[0].DomainResolver.Server != "endpoint-bootstrap" || opts.DNS.Final != "gateway" {
		t.Fatal("endpoint bootstrap widened to application DNS")
	}
	for _, server := range opts.DNS.Servers {
		if server.Tag == "gateway" && (server.Server != "10.8.0.1" || server.Detour != "proxy") {
			t.Fatal("gateway DNS bypasses authenticated proxy")
		}
	}
	if !strings.Contains(string(data), `"AAAA"`) || !strings.Contains(string(data), `"ip_version": 6`) {
		t.Fatal("IPv6 is not rejected locally")
	}
}
