package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ArghTeam/razdacha/internal/ingressguard"
)

func TestProductionRestoreIngressAllowsOnlyGenuinelyFreshArtifacts(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "state.db")
	config := filepath.Join(dir, "config.json")
	manifest := filepath.Join(dir, "public-ingress.json")
	wgCalls, publicCalls := 0, 0
	closeIngress := func() error {
		return closeRestoreIngress(db, config, func() error { wgCalls++; return nil }, func([]ingressguard.Endpoint) error { publicCalls++; return nil })
	}
	if err := closeIngress(); err != nil {
		t.Fatal("fresh target restore refused", err)
	}
	if wgCalls != 1 || publicCalls != 0 {
		t.Fatal("fresh restore changed public safety state")
	}
	if err := os.WriteFile(manifest, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := closeIngress(); err == nil {
		t.Fatal("orphan/corrupt authority allowed restore")
	}
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"inbounds":[{"type":"vless","listen":"0.0.0.0","listen_port":8443}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := closeIngress(); err == nil {
		t.Fatal("unapproved saved public listener allowed restore")
	}
	if publicCalls != 0 {
		t.Fatal("unsafe restore reached public installer")
	}
}
