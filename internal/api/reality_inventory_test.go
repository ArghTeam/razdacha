package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

type inventoryCountingPublisher struct {
	Applier
	RealityPublisher
	batches, peers int
}

func (p *inventoryCountingPublisher) RealityInventory(_ context.Context, _ *store.Store, peers []store.Peer) map[string]RealityInventoryEntry {
	p.batches++
	p.peers += len(peers)
	out := make(map[string]RealityInventoryEntry, len(peers))
	for _, peer := range peers {
		out[peer.ID] = RealityInventoryEntry{Status: "pending", Downloadable: true, Revision: peer.RealityRevision}
	}
	return out
}

func (*inventoryCountingPublisher) RealityStatus(context.Context, *store.Store, store.Peer) string {
	panic("unexpected per-peer admission reconstruction")
}

func (*inventoryCountingPublisher) RealityDownloadable(context.Context, *store.Store, store.Peer) bool {
	panic("unexpected per-peer delivery check")
}

func (*inventoryCountingPublisher) RealityProfile(context.Context, *store.Store, string) (RealityProfile, error) {
	panic("inventory serialized a secret profile")
}

func TestRealityInventoryCallsOneBatchAndNoSecretExportsForHundredClients(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	for i := 0; i < 100; i++ {
		if _, err := CreatePeer(context.Background(), ts.st, fmt.Sprintf("Client %d", i), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	p := &inventoryCountingPublisher{}
	ts.applier = p
	response := ts.auth(t, cookie, http.MethodGet, "/api/peers", "")
	requireCode(t, response, http.StatusOK)
	var peers []peerResponse
	decodeJSONBody(t, response, &peers)
	if p.batches != 1 || p.peers != 100 || len(peers) != 100 {
		t.Fatal("inventory did not use exactly one complete batch", p.batches, p.peers, len(peers))
	}
	for _, peer := range peers {
		if peer.RealityProfileStatus != "pending" || !peer.RealityProfileDownloadable {
			t.Fatal("batch result omitted")
		}
	}
}
