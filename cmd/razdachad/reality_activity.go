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
	revisions := make(map[string]int, len(g.base.RealityAdmissions))
	for _, a := range g.base.RealityAdmissions {
		revisions[a.PeerID] = a.Revision
	}
	for _, u := range live.Users {
		if revision := revisions[u.PeerID]; revision > 0 {
			out.Users[u.Name] = revision
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
		if len(r) != 3 && len(r) != 4 {
			continue
		}
		exact := true
		for key := range r {
			if key != "inbound" && key != "auth_user" && key != "outbound" && key != "action" {
				exact = false
				break
			}
		}
		if !exact {
			continue
		}
		var inbound, auth []string
		action := "route"
		var outbound string
		if !activityStringList(r["inbound"], &inbound) || !activityStringList(r["auth_user"], &auth) || json.Unmarshal(r["outbound"], &outbound) != nil {
			continue
		}
		if raw, ok := r["action"]; ok && json.Unmarshal(raw, &action) != nil {
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

func activityStringList(data json.RawMessage, out *[]string) bool {
	if json.Unmarshal(data, out) == nil {
		return true
	}
	var single string
	if json.Unmarshal(data, &single) != nil {
		return false
	}
	*out = []string{single}
	return true
}
