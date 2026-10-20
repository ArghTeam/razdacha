package netstack

import "testing"

func TestNativeTransportMarksOnlyTCPUDP(t *testing.T) {
	rs, err := BuildNftRuleset(NftConfig{WANInterface: "eth0", TransportSubnets: []string{"203.0.113.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, chain := range rs.Chains {
		for _, rule := range chain.Rules {
			if rule.DstSet == SetTransportSubnets {
				count++
				if rule.Action != ActionMark || (rule.L4Proto != "tcp" && rule.L4Proto != "udp") {
					t.Fatalf("unrestricted transport mark: %+v", rule)
				}
			}
		}
	}
	if count != 2 {
		t.Fatalf("transport marks=%d", count)
	}
}
