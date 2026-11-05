package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/ingressguard"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// RealityActivityAuthority binds a controller snapshot to checked runtime bytes
// and current live admission. No credential material is returned to HTTP clients.
func (g *applyGate) RealityActivityAuthority(ctx context.Context, st *store.Store) (api.RealityActivityAuthority, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out api.RealityActivityAuthority
	if g.network == nil || !g.network.initialized || g.network.uncertain || !g.base.Settings.RealityEnabled {
		return out, api.ErrNetworkGuarded
	}
	manifest, err := ingressguard.Read(g.manifestPath())
	if err != nil {
		return out, fmt.Errorf("read activity authority: %w", err)
	}
	if manifest.Guarded || len(manifest.ConfigHashes) != 1 {
		return out, api.ErrNetworkGuarded
	}
	config, err := ingressguard.ReadConfig(g.configPath())
	if err != nil {
		return out, fmt.Errorf("read activity configuration: %w", err)
	}
	hash := ingressguard.HashConfig(config)
	if hash != manifest.ConfigHashes[0] {
		return out, api.ErrNetworkGuarded
	}
	live, err := st.RealityRuntime(ctx, g.base)
	if err != nil {
		return out, fmt.Errorf("read activity admission: %w", err)
	}
	out.Users = make(map[string]int)
	for _, u := range live.Users {
		for _, a := range g.base.RealityAdmissions {
			if a.PeerID == u.PeerID {
				out.Users[u.Name] = a.Revision
			}
		}
	}
	if !activityFallbacksPresent(config, out.Users) {
		return api.RealityActivityAuthority{}, fmt.Errorf("runtime does not support authenticated flow observations")
	}
	data, err := json.Marshal(struct {
		Epoch      uint64
		Config     string
		Users      map[string]int
		Admissions []store.RealityAdmission
	}{g.activityEpoch, hash, out.Users, g.base.RealityAdmissions})
	if err != nil {
		return api.RealityActivityAuthority{}, fmt.Errorf("encode activity authority: %w", err)
	}
	sum := sha256.Sum256(data)
	out.Epoch = hex.EncodeToString(sum[:])
	return out, nil
}

func activityFallbacksPresent(config []byte, users map[string]int) bool {
	var doc struct {
		Route struct {
			Rules []map[string]json.RawMessage `json:"rules"`
		} `json:"route"`
	}
	if json.Unmarshal(config, &doc) != nil {
		return false
	}
	found := make(map[string]bool)
	for _, r := range doc.Route.Rules {
		if len(r) != 4 {
			continue
		}
		var inbound, auth []string
		var action, outbound string
		if json.Unmarshal(r["inbound"], &inbound) != nil || json.Unmarshal(r["auth_user"], &auth) != nil || json.Unmarshal(r["action"], &action) != nil || json.Unmarshal(r["outbound"], &outbound) != nil {
			continue
		}
		if len(inbound) == 1 && inbound[0] == singbox.TagRealityIn && len(auth) == 1 && action == "route" && outbound == singbox.TagDirect {
			if _, ok := users[auth[0]]; ok {
				found[auth[0]] = true
			}
		}
	}
	return len(users) > 0 && len(found) == len(users)
}
