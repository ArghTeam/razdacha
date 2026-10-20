package netstack

import (
	"net/netip"
	"testing"
)

// This evaluator tests compiled rule behavior over independent packet examples;
// Linux integration separately checks that expression translation reaches the kernel.
func TestPeerEgressCaptureAndResidualForwarding(t *testing.T) {
	cfg := testConfig("203.0.113.0/24", "100.64.0.1/32")
	cfg.PeerEgress = []string{"100.64.0.2"}
	cfg.ResolverAddr = "100.64.0.1"
	rs := buildOK(t, cfg)
	match := func(r NftRule, src, dst, proto string, port uint16) bool {
		if r.IIfNeq {
			return false
		}
		if r.NFProto != "" && r.NFProto != "ipv4" {
			return false
		}
		if r.L4Proto != "" && r.L4Proto != proto {
			return false
		}
		if r.DstPort != 0 && r.DstPort != port {
			return false
		}
		if r.DstPrefix.IsValid() && !r.DstPrefix.Contains(netip.MustParseAddr(dst)) {
			return false
		}
		contains := func(name, ip string) bool {
			for _, v := range setByName(t, rs, name).Elements {
				if v.Contains(netip.MustParseAddr(ip)) {
					return true
				}
			}
			return false
		}
		if r.SrcSet != "" && !contains(r.SrcSet, src) {
			return false
		}
		if r.DstSet != "" && !contains(r.DstSet, dst) {
			return false
		}
		return true
	}
	for _, tc := range []struct {
		name, src, dst, proto string
		port                  uint16
		mark, deny            bool
	}{
		{"selected TCP", "100.64.0.2", "192.0.2.10", "tcp", 443, true, false},
		{"selected UDP", "100.64.0.2", "192.0.2.10", "udp", 443, true, false},
		{"shared direct", "100.64.0.3", "192.0.2.10", "tcp", 443, false, false},
		{"shared list", "100.64.0.3", "203.0.113.10", "tcp", 443, true, false},
		{"selected ICMP list", "100.64.0.2", "203.0.113.10", "icmp", 0, false, true},
		{"selected arbitrary IP", "100.64.0.2", "192.0.2.10", "gre", 0, false, true},
		{"selected private", "100.64.0.2", "10.20.0.1", "tcp", 443, false, true},
		{"gateway administration", "100.64.0.2", "100.64.0.1", "tcp", 443, false, false},
		{"shared gateway list unchanged", "100.64.0.3", "100.64.0.1", "tcp", 443, true, false},
		{"gateway DNS", "100.64.0.2", "100.64.0.1", "udp", 53, false, false},
		{"shared ICMP", "100.64.0.3", "192.0.2.10", "icmp", 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marked := false
			for _, r := range chainByName(t, rs, ChainMangle).Rules {
				if !match(r, tc.src, tc.dst, tc.proto, tc.port) {
					continue
				}
				if r.Action == ActionReturn {
					break
				}
				if r.Action == ActionMark {
					marked = true
				}
			}
			if marked != tc.mark {
				t.Fatalf("capture=%v want %v", marked, tc.mark)
			}
			// TProxy packets enter INPUT. Gateway DNS/admin also enter INPUT. All
			// other unproxied selected packets encounter source-scoped FORWARD reject.
			deny := false
			if !marked && tc.dst != "100.64.0.1" {
				for _, r := range chainByName(t, rs, ChainForward).Rules {
					if match(r, tc.src, tc.dst, tc.proto, tc.port) && r.Action == ActionRejectICMP {
						deny = true
					}
				}
			}
			if deny != tc.deny {
				t.Fatalf("residual forwarding reject=%v want %v", deny, tc.deny)
			}
		})
	}
	for _, bad := range []string{"0.0.0.0/0", "10.8.0.0/24", "::1", "invalid"} {
		cfg.PeerEgress = []string{bad}
		if _, err := BuildNftRuleset(cfg); err == nil {
			t.Fatalf("non-exact IPv4 source accepted: %s", bad)
		}
	}
}
