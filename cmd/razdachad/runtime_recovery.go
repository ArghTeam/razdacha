package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/ingressguard"
)

// recoverRuntimeRestart accepts only managed startup intent while the daemon
// still owns a fully committed generation. Publication failures remain uncertain.
func (g *applyGate) recoverRuntimeRestart(ctx context.Context) (repaired bool, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := g.network
	if g.store == nil || !n.initialized || n.uncertain {
		return false, nil
	}
	m, err := ingressguard.Read(g.manifestPath())
	if err != nil {
		return false, err
	}
	if !m.Guarded || !m.RuntimeRestart {
		return false, nil
	}
	defer func() {
		if err != nil {
			n.uncertain = true
		}
	}()
	config, err := ingressguard.ReadConfig(g.configPath())
	if err != nil {
		return false, err
	}
	if err = ingressguard.ValidateConfig(config, m); err != nil {
		return false, err
	}
	if len(m.ConfigHashes) != 1 || m.ConfigHashes[0] != ingressguard.HashConfig(config) {
		return false, fmt.Errorf("runtime restart has ambiguous configuration authority")
	}
	endpoints, err := ingressguard.ConfigEndpoints(config)
	if err != nil {
		return false, err
	}
	if len(endpoints) == 0 || !reflect.DeepEqual(endpoints, m.Endpoints) {
		return false, fmt.Errorf("runtime recovery endpoint authority changed")
	}
	committed, err := readGeneration(n.path)
	if err != nil {
		return false, fmt.Errorf("verify runtime recovery authority: %w", err)
	}
	expected := networkGeneration{Kind: committed.Kind, Version: committed.Version, Policy: g.base, Lists: n.contents}
	disk, err := json.Marshal(committed)
	if err != nil {
		return false, err
	}
	memory, err := json.Marshal(expected)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(disk, memory) {
		return false, fmt.Errorf("runtime recovery authority changed")
	}
	if _, err = g.publishChecked(ctx, g.base, true, false, n.checkRuntime); err != nil {
		return false, err
	}
	return true, nil
}

func watchRuntimeRestarts(ctx context.Context, g *applyGate) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			attempt, cancel := context.WithTimeout(ctx, time.Minute)
			repaired, err := g.recoverRuntimeRestart(attempt)
			cancel()
			if err != nil && ctx.Err() == nil {
				g.log.Error("Recover independently restarted runtime", "error", err)
			}
			if repaired {
				g.log.Info("Recovered independently restarted runtime")
			}
		}
	}
}

// A managed restart stops the old process before this controller check begins.
// Missing controller readiness leaves both ingress barriers closed.
func waitRuntimeController(ctx context.Context) error {
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := clash.New(clash.Options{})
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := client.Version(readyCtx); err == nil {
			return nil
		}
		select {
		case <-readyCtx.Done():
			return readyCtx.Err()
		case <-ticker.C:
		}
	}
}
