package networkstate

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestPeerRoutingRequiresVersionThreeForBothAuthorities(t *testing.T) {
	target := "fixture-exit"
	for _, kind := range []string{CommittedKind, BootstrapKind} {
		for _, version := range []int{1, 2, 3, 4} {
			t.Run(fmt.Sprintf("%s-v%d", kind, version), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "record.json")
				gen := Generation{Kind: kind, Version: version, Policy: store.Snapshot{Peers: []store.Peer{{EgressTunnelID: &target}}}, Lists: map[string]lists.List{}}
				if err := Write(path, gen); err != nil {
					t.Fatal(err)
				}
				_, err := Read(path, kind)
				if (err == nil) != (version == 3 || version == 4) {
					t.Fatalf("routing envelope admission v%d: %v", version, err)
				}
				gen.Policy.Peers[0].EgressTunnelID = nil
				if err := Write(path, gen); err != nil {
					t.Fatal(err)
				}
				_, err = Read(path, kind)
				if (err == nil) != (version <= 4) {
					t.Fatalf("legacy envelope admission v%d: %v", version, err)
				}
			})
		}
	}
}
