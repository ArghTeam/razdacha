package api

import (
	"context"

	"github.com/ArghTeam/razdacha/internal/store"
)

// RealityInventoryEntry carries no secret material. Revision is checked against
// the inventory peer so a concurrent mutation cannot annotate an older identity.
type RealityInventoryEntry struct {
	Status       string
	Downloadable bool
	Revision     int
}

// RealityInventoryPublisher batches request-local eligibility under one gate.
// Legacy publishers retain their per-peer compatibility path.
type RealityInventoryPublisher interface {
	RealityInventory(context.Context, *store.Store, []store.Peer) map[string]RealityInventoryEntry
}
