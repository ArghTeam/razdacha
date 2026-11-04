package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ArghTeam/razdacha/internal/store"
)

// RealityPublisher serializes authentication changes and derives delivery from
// committed policy plus live credentials. Status never contains bearer material.
type RealityPublisher interface {
	PatchPeer(context.Context, *store.Store, string, store.PeerPatch) (store.Peer, error)
	DeletePeer(context.Context, *store.Store, string) (store.PeerDeletion, error)
	RotateReality(context.Context, *store.Store, string, int) (store.Peer, error)
	SaveSettings(context.Context, *store.Store, store.Settings) error
	RealityStatus(context.Context, *store.Store, store.Peer) string
	RealityConfig(context.Context, *store.Store, string) (string, error)
	RealityClientConfig(context.Context, *store.Store, string) ([]byte, error)
}

func realityDefaultStatus(p store.Peer) string {
	if !p.Enabled || !p.RealityEnabled {
		return "disabled"
	}
	return "pending"
}

func (s *Server) peerResponse(ctx context.Context, p store.Peer) peerResponse {
	out := newPeerResponse(p)
	if pub, ok := s.applier.(RealityPublisher); ok {
		out.RealityProfileStatus = pub.RealityStatus(ctx, s.store, p)
	}
	return out
}

func (s *Server) patchPeer(ctx context.Context, id string, p store.PeerPatch) (store.Peer, error) {
	if pub, ok := s.applier.(RealityPublisher); ok {
		return pub.PatchPeer(ctx, s.store, id, p)
	}
	// A server without the coherent publisher cannot enable public authentication.
	if p.RealityEnabled != nil && *p.RealityEnabled {
		return store.Peer{}, fmt.Errorf("%w: public ingress publisher unavailable", ErrNetworkGuarded)
	}
	return s.store.PatchPeer(ctx, id, p)
}

func (s *Server) handleRealityConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	pub, ok := s.applier.(RealityPublisher)
	if !ok {
		s.storeError(w, ErrNetworkGuarded, "Profile unavailable")
		return
	}
	profile, err := pub.RealityConfig(r.Context(), s.store, id)
	if err != nil {
		s.storeError(w, err, "Profile unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="reality-client.txt"`)
	_, _ = w.Write([]byte(profile + "\n"))
}

func (s *Server) handleRotateReality(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	var req struct {
		ExpectedRevision *int `json:"expected_revision"`
	}
	if !s.decodeBody(w, r, &req) {
		return
	}
	if req.ExpectedRevision == nil || *req.ExpectedRevision < 1 {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "expected_revision must be a positive integer")
		return
	}
	pub, ok := s.applier.(RealityPublisher)
	if !ok {
		s.storeError(w, ErrNetworkGuarded, "Profile unavailable")
		return
	}
	p, err := pub.RotateReality(r.Context(), s.store, id, *req.ExpectedRevision)
	if err != nil {
		s.storeError(w, err, "Client not found")
		return
	}
	writeJSON(w, s.log, http.StatusOK, s.peerResponse(r.Context(), p))
}

func (s *Server) handleRealityClientConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	pub, ok := s.applier.(RealityPublisher)
	if !ok {
		s.storeError(w, ErrNetworkGuarded, "Profile unavailable")
		return
	}
	profile, err := pub.RealityClientConfig(r.Context(), s.store, id)
	if err != nil {
		s.storeError(w, err, "Profile unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="razdacha-reality.json"`)
	_, _ = w.Write(profile)
}
