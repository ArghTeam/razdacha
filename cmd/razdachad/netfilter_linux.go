//go:build linux

package main

import (
	"context"
	"fmt"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/store"
)

// startNetfilter establishes the persistent client guard before WG startup.
// The gate receives publishers which consume only immutable generations.
func startNetfilter(ctx context.Context) (netfilter, error) {
	// Guard first, including before WAN discovery and WireGuard startup.
	if err := netstack.SetClientGuard(true); err != nil {
		return netfilter{}, err
	}
	route := netstack.NewRoute()
	wan, err := route.DetectWAN()
	if err != nil {
		return netfilter{}, fmt.Errorf("detect external interface: %w", err)
	}
	publish := func(ctx context.Context, snap store.Snapshot, contents map[string]lists.List) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		subnets := generationSubnets(snap, contents)
		// Fresh batch per attempt; failed transactions cannot retain queued mutations.
		nft, err := netstack.NewNft()
		if err != nil {
			return 0, err
		}
		rs, err := nft.Apply(netstack.NftConfig{WANInterface: wan, ResolverAddr: snap.Settings.WGServerAddress, Subnets: subnets, PeerEgress: netstack.PeerEgressSources(snap.Peers)})
		if err != nil {
			return 0, err
		}
		if rs.SkippedSubnets != 0 {
			return 0, fmt.Errorf("candidate contains %d invalid subnets", rs.SkippedSubnets)
		}
		return len(subnets), nil
	}
	return netfilter{stop: func() {}, nftState: netstack.DiagNft, routeState: netstack.DiagRoute, guard: netstack.SetClientGuard, ensureRoute: func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return route.Apply()
	}, publish: publish}, nil
}

// resetNetfilter снимает всё, что демон добавил в сеть: таблицу, правило
// маршрутизации и таблицу 105. Интерфейс wg0 не трогается — он переживает
// сброс, иначе клиенты теряют связь из-за диагностической команды.
func resetNetfilter() error {
	nft, err := netstack.NewNft()
	if err != nil {
		return fmt.Errorf("подключение к nftables: %w", err)
	}
	if err := nft.Clear(); err != nil {
		return fmt.Errorf("снятие правил: %w", err)
	}
	if err := netstack.NewRoute().Clear(); err != nil {
		return fmt.Errorf("снятие маршрутизации: %w", err)
	}
	return netstack.SetClientGuard(false)
}
