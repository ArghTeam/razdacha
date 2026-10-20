package networkstate

import (
	"github.com/ArghTeam/razdacha/internal/lists"
	"path/filepath"
	"testing"
)

func TestTransportRequiresVersionFour(t *testing.T) {
	for _, kind := range []string{CommittedKind, BootstrapKind} {
		for version := 1; version <= 5; version++ {
			path := filepath.Join(t.TempDir(), "generation.json")
			gen := Generation{Version: version, Kind: kind, Lists: map[string]lists.List{"fixture": {Native: &lists.NativeArtifact{Version: 2}, TransportSubnets: []string{"203.0.113.0/24"}}}}
			if err := Write(path, gen); err != nil {
				t.Fatal(err)
			}
			_, err := Read(path, kind)
			if (err == nil) != (version == 4) {
				t.Fatalf("version %d admission: %v", version, err)
			}
		}
	}
}
