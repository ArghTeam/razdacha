package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

func onboardingCountry(raw string) (string, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	if value == "" || value == "AUTO" {
		return "AUTO", nil
	}
	if value == "RU" || value == "BY" {
		return "", fmt.Errorf("%w: country is excluded", store.ErrInvalid)
	}
	if len(value) != 2 || value[0] < 'A' || value[0] > 'Z' || value[1] < 'A' || value[1] > 'Z' {
		return "", fmt.Errorf("%w: invalid country", store.ErrInvalid)
	}
	return value, nil
}

func (g *applyGate) ConfigureOnboarding(ctx context.Context, st *store.Store, intent api.OnboardingRequest) (store.OnboardingState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return store.OnboardingState{}, err
	}
	if !g.network.initialized || g.network.uncertain {
		return store.OnboardingState{}, api.ErrNetworkGuarded
	}
	state, err := st.Onboarding(ctx)
	if err != nil {
		return state, err
	}
	country, err := onboardingCountry(intent.Country)
	if err != nil {
		return state, err
	}
	if intent.Mode != "free" && intent.Mode != "skip" {
		return state, fmt.Errorf("%w: invalid mode", store.ErrInvalid)
	}
	if !state.Pending || (state.ReadOnly && intent.Mode == "free") {
		return state, fmt.Errorf("%w: setup is unavailable", store.ErrInvalid)
	}
	if state.Prepared && !state.Applied && (state.Mode != intent.Mode || state.Country != country) {
		return state, store.ErrOnboardingConflict
	}
	snap, err := st.Snapshot(ctx)
	if err != nil {
		return state, err
	}
	current, err := store.PolicyFingerprint(snap)
	if err != nil {
		return state, err
	}
	base, err := store.PolicyFingerprint(g.base)
	if err != nil {
		return state, err
	}
	if current != base && (!state.Prepared || state.Expected != current) {
		return state, store.ErrOnboardingConflict
	}
	var members []store.PoolServer
	catalog := ""
	if intent.Mode == "free" {
		u, _ := url.Parse(lists.APIpoolURL)
		q := url.Values{"protocol": {"vless,ss,trojan,hysteria2"}, "exclude_country": {"RU,BY"}}
		if country != "AUTO" {
			q.Set("country", country)
		}
		u.RawQuery = q.Encode()
		catalog = u.String()
		if _, _, err := lists.PoolDriverFor(catalog); err != nil {
			return state, err
		}
		if g.onboardingCatalog != nil {
			members, err = g.onboardingCatalog(ctx, catalog)
		} else {
			crawl := lists.PoolCatalog{Limit: 100, Timeout: 15 * time.Second, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), CheckKey: func(raw string) error { _, parseErr := singbox.Parse(raw); return parseErr }}
			bounded, cancel := context.WithTimeout(ctx, 40*time.Second)
			members, err = crawl.Servers(bounded, catalog, store.PoolFilterFrom(snap.Settings))
			cancel()
		}
		if ctx.Err() != nil {
			return state, ctx.Err()
		}
		if err != nil {
			members = nil
		} // Preserve selected-resource rejection on catalog failure.
	}
	preset := []string{}
	for _, key := range lists.Preset() {
		if key != "google_ai" {
			preset = append(preset, key)
		}
	}
	return st.ConfigureOnboarding(ctx, current, intent.Mode, country, catalog, members, preset)
}

func (g *applyGate) ApplyOnboarding(ctx context.Context, st *store.Store) (singbox.ApplyResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return singbox.ApplyResult{}, err
	}
	state, err := st.Onboarding(ctx)
	if err != nil {
		return singbox.ApplyResult{}, err
	}
	snap, err := st.Snapshot(ctx)
	if err != nil {
		return singbox.ApplyResult{}, err
	}
	current, err := store.PolicyFingerprint(snap)
	if err != nil {
		return singbox.ApplyResult{}, err
	}
	if !state.Pending || !state.Prepared || state.Expected != current {
		return singbox.ApplyResult{}, store.ErrOnboardingConflict
	}
	if g.network.manager != nil {
		loaded := g.network.manager.Snapshot()
		sources := lists.Sources(snap)
		missing := false
		for _, source := range sources {
			if _, ok := loaded[source.URL]; !ok {
				missing = true
			}
		}
		if missing {
			seen := map[string]bool{}
			for _, source := range sources {
				seen[source.URL] = true
			}
			for _, source := range lists.Sources(g.base) {
				if !seen[source.URL] {
					sources = append(sources, source)
				}
			}
			g.network.manager.SetSources(sources)
			bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
			_ = g.network.manager.Refresh(bounded)
			cancel()
		}
	}
	if err := ctx.Err(); err != nil {
		return singbox.ApplyResult{}, err
	}
	result, err := g.publish(ctx, snap, false, true)
	if err != nil {
		return result, err
	}
	return result, st.AcceptOnboarding(ctx, current, false)
}

func (g *applyGate) FinishOnboarding(ctx context.Context, st *store.Store) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	state, err := st.Onboarding(ctx)
	if err != nil {
		return err
	}
	applied, err := store.PolicyFingerprint(g.base)
	if err != nil {
		return err
	}
	if g.network.uncertain || !g.network.initialized || state.Expected != applied {
		return store.ErrOnboardingConflict
	}
	return st.AcceptOnboarding(ctx, applied, true)
}

func (g *applyGate) ReopenOnboarding(ctx context.Context, st *store.Store, peerID string) (store.OnboardingState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return st.ReopenOnboarding(ctx, peerID)
}
