package networkstate

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestPublicAuthenticationRequiresVersionFive(t *testing.T) {
	for _, kind := range []string{CommittedKind, BootstrapKind} {
		for _, version := range []int{1, 2, 3, 4, 5, 6} {
			t.Run(fmt.Sprintf("%s-v%d", kind, version), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "generation.json")
				for _, policy := range []store.Snapshot{
					{Settings: store.Settings{RealityEnabled: true}},
					{RealityIdentityID: "identity"},
					{RealityAdmissions: []store.RealityAdmission{{PeerID: "client", CredentialID: "opaque", Revision: 1}}},
					{Peers: []store.Peer{{WireGuardDisabled: true}}},
					{Peers: []store.Peer{{RealityEnabled: true, RealityRevision: 1, RealityCredentialID: "opaque"}}},
				} {
					if err := Write(path, Generation{Kind: kind, Version: version, Policy: policy, Lists: map[string]lists.List{}}); err != nil {
						t.Fatal(err)
					}
					_, err := Read(path, kind)
					if (err == nil) != (version == 5) {
						t.Fatalf("transport authority v%d admitted=%v", version, err == nil)
					}
				}
			})
		}
	}
}
