package netstack

import (
	"net/netip"
	"testing"

	"github.com/ArghTeam/razdacha/internal/ingressguard"
)

func TestPublicGuardPlanScope(t *testing.T) {
	rules, err := publicGuardPlan([]ingressguard.Endpoint{{Address: "::", Port: 8443}, {Address: "127.0.0.1", Port: 9443}, {Address: "2001:db8::1", Port: 9443}, {Address: "0.0.0.0", Port: 10443}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 5 {
		t.Fatalf("expected both wildcard families and address-specific rules, got %d", len(rules))
	}
	var wildcard4, wildcard6, specific4, specific6 bool
	for _, rule := range rules {
		if rule.port == 8443 && !rule.address.IsValid() {
			if rule.family == 4 {
				wildcard4 = true
			}
			if rule.family == 6 {
				wildcard6 = true
			}
		}
		if rule.port == 9443 && rule.address == netip.MustParseAddr("127.0.0.1") {
			specific4 = rule.family == 4
		}
		if rule.port == 9443 && rule.address == netip.MustParseAddr("2001:db8::1") {
			specific6 = rule.family == 6
		}
		if rule.port == 10443 && rule.family != 4 {
			t.Fatal("IPv4 wildcard widened to IPv6")
		}
	}
	if !wildcard4 || !wildcard6 || !specific4 || !specific6 {
		t.Fatal("endpoint address/family scope lost")
	}
	if _, err := publicGuardPlan([]ingressguard.Endpoint{{Address: "invalid", Port: 8443}}); err == nil {
		t.Fatal("invalid plan accepted")
	}
}
