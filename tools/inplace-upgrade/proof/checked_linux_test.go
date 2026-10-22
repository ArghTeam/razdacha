//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

type checkFunc func(context.Context, string) error

func (f checkFunc) Check(ctx context.Context, path string) error { return f(ctx, path) }
func checkedPoolFixture() networkstate.Generation {
	return networkstate.Generation{Kind: networkstate.CommittedKind, Version: 4, Lists: map[string]lists.List{}, Policy: store.Snapshot{
		Settings: store.DefaultSettings(), Tunnels: []store.Tunnel{{
			ID: "synthetic-pool", Name: "Synthetic pool", Type: store.TunnelVLESS, Source: store.SourcePool, Enabled: true,
			Pool: []store.PoolServer{
				{URL: "vless://00000000-0000-0000-0000-000000000000@192.0.2.1:443?security=tls&sni=synthetic.example"},
				{URL: "vless://00000000-0000-0000-0000-000000000000@192.0.2.2:443?security=tls&sni=synthetic.example"},
			},
		}},
	}}
}

func TestCheckedProofUsesOnlyBoundedPoolPruningAndCleansTemporaries(t *testing.T) {
	parent := t.TempDir()
	gen := checkedPoolFixture()
	calls := 0
	checker := checkFunc(func(_ context.Context, path string) error {
		calls++
		if !strings.HasPrefix(path, parent+string(os.PathSeparator)) {
			t.Fatal("checker escaped owned temporary directory")
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), "never-published.json")); !os.IsNotExist(err) {
			t.Fatal("proof published a runtime config")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var config struct {
			Outbounds []struct {
				Server string `json:"server"`
			}
		}
		if err := json.Unmarshal(body, &config); err != nil {
			return err
		}
		for index, outbound := range config.Outbounds {
			if outbound.Server == "192.0.2.1" {
				return fmt.Errorf("%w: initialize outbound[%d]: synthetic rejected pool member", singbox.ErrCheckFailed, index)
			}
		}
		return nil
	})
	raw, checked, err := checkedConfiguration(context.Background(), gen, checker, parent)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || raw == checked {
		t.Fatalf("expected exactly one rejected pool retry and changed checked output: calls=%d", calls)
	}
	gen.Policy.Tunnels[0].Pool = gen.Policy.Tunnels[0].Pool[1:]
	config, err := singbox.Generate(gen.Policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := singbox.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	if checked != hex.EncodeToString(digest[:]) {
		t.Fatal("checked proof does not match the precise pruned candidate")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("owned proof temporaries remain: %v %v", entries, err)
	}
}

func TestCheckedProofRefusesNonPoolValidationAndCleansTemporaries(t *testing.T) {
	parent := t.TempDir()
	calls := 0
	checker := checkFunc(func(context.Context, string) error {
		calls++
		return fmt.Errorf("%w: initialize endpoint[0]: synthetic manual tunnel error", singbox.ErrCheckFailed)
	})
	if _, _, err := checkedConfiguration(context.Background(), checkedPoolFixture(), checker, parent); err == nil {
		t.Fatal("non-pool validation failure was accepted")
	}
	if calls != 1 {
		t.Fatalf("non-pool validation retried: %d", calls)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failure left owned temporary files: %v %v", entries, err)
	}
}

func TestPinnedCheckerRejectsChangedExecutableBeforeExecution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-checker")
	if err := os.WriteFile(path, []byte("not executed"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := runtimeDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	checker := pinnedChecker{binary: path, digest: digest, version: "synthetic"}
	if err := os.WriteFile(path, []byte("changed executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := checker.Check(context.Background(), "unused-private-candidate"); err == nil || !strings.Contains(err.Error(), "changed during proof") {
		t.Fatalf("changed checker was not rejected before execution: %v", err)
	}
}
