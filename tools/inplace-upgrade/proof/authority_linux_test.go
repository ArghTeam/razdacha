//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestAuthorityProofRetainsExactPrivateEvidence(t *testing.T) {
	dir := t.TempDir()
	gen := networkstate.Generation{
		Kind: networkstate.CommittedKind, Version: 4,
		Lists: map[string]lists.List{}, Policy: store.Snapshot{Settings: store.DefaultSettings(), Tunnels: []store.Tunnel{{ID: "synthetic-pool", Source: store.SourcePool}}},
	}
	path := filepath.Join(dir, networkstate.CommittedFile)
	if err := networkstate.Write(path, gen); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Unknown future fields and formatting are retained as evidence, even though
	// the existing typed canonical digest does not represent them.
	raw := strings.TrimSuffix(string(original), "}") + `, "future_field": "synthetic-evidence" }` + "\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	proof, err := authorityProof(dir)
	if err != nil {
		t.Fatal(err)
	}
	if proof["generation_raw"] != raw {
		t.Fatal("exact original evidence was not retained")
	}
	digest := sha256.Sum256([]byte(raw))
	if proof["raw_sha256"] != hex.EncodeToString(digest[:]) {
		t.Fatal("raw evidence checksum mismatch")
	}
	if proof["version"] != 4 || proof["native_sources"] != 0 {
		t.Fatalf("unexpected proof: %+v", proof)
	}
}

func TestAuthorityProofRevalidatesCurrentNativeArtifactEveryTime(t *testing.T) {
	dir := t.TempDir()
	nativeDir := filepath.Join(dir, "lists", "native")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"version":3,"rules":[{"ip_cidr":["198.51.100.0/24"]}]}`)
	projection, err := lists.ProjectNative(body, lists.FormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	hash := hex.EncodeToString(digest[:])
	path := filepath.Join(nativeDir, hash+".json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	gen := networkstate.Generation{Kind: networkstate.CommittedKind, Version: 4, Policy: store.Snapshot{Settings: store.DefaultSettings()}, Lists: map[string]lists.List{
		"synthetic-source": {
			Native:  &lists.NativeArtifact{Version: lists.ProjectionVersion, Format: lists.FormatJSON, Hash: hash, Path: path},
			Subnets: projection.Subnets, TransportSubnets: projection.TransportSubnets,
		},
	}}
	if err := networkstate.Write(filepath.Join(dir, networkstate.CommittedFile), gen); err != nil {
		t.Fatal(err)
	}
	if _, err := authorityProof(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt synthetic artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authorityProof(dir); err == nil {
		t.Fatal("current native artifact corruption accepted after previous successful proof")
	}
}

func TestAuthorityGeneratedConfigTracksEnabledPoolExactly(t *testing.T) {
	dir := t.TempDir()
	gen := networkstate.Generation{
		Kind: networkstate.CommittedKind, Version: 4,
		Lists: map[string]lists.List{}, Policy: store.Snapshot{Settings: store.DefaultSettings(), Tunnels: []store.Tunnel{{
			ID: "synthetic-pool", Name: "Synthetic pool", Source: store.SourcePool, Type: store.TunnelVLESS, Enabled: true,
			Pool: []store.PoolServer{{URL: "vless://00000000-0000-0000-0000-000000000000@192.0.2.1:443?security=tls&sni=synthetic.example"}},
		}}},
	}
	path := filepath.Join(dir, networkstate.CommittedFile)
	if err := networkstate.Write(path, gen); err != nil {
		t.Fatal(err)
	}
	first, err := authorityProof(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen.Policy.Tunnels[0].Pool[0].URL = "vless://00000000-0000-0000-0000-000000000000@192.0.2.2:443?security=tls&sni=synthetic.example"
	if err := networkstate.Write(path, gen); err != nil {
		t.Fatal(err)
	}
	second, err := authorityProof(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first["generated_config_sha256"] == second["generated_config_sha256"] {
		t.Fatal("enabled pool catalog change did not change exact generated config proof")
	}
	config, err := singbox.Generate(gen.Policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := singbox.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	if second["generated_config_sha256"] != hex.EncodeToString(digest[:]) {
		t.Fatal("generated config proof differs from exact generation marshal")
	}
}
