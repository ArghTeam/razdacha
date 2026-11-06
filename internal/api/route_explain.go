package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// RouteExplainer reads committed authority; it cannot publish or probe policy.
type RouteExplainer interface {
	ExplainRoute(context.Context, *store.Store, singbox.ExplainRequest) (singbox.PolicyExplanation, error)
}

func (s *Server) handleRouteExplain(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var req singbox.ExplainRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "Invalid web policy request.")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "Exactly one web policy request is required.")
		return
	}
	domain, err := singbox.NormalizeExplainDomain(req.Domain)
	if err != nil || req.PeerID == "" || len(req.PeerID) > 128 || req.Transport != "wg" && req.Transport != "reality" {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "Choose an existing client, wg or reality ingress and a domain without URL, IP, port, path or CIDR.")
		return
	}
	req.Domain = domain
	select {
	case s.explainSlot <- struct{}{}:
		defer func() { <-s.explainSlot }()
	default:
		writeError(w, s.log, http.StatusTooManyRequests, codeTooMany, "A web policy explanation is already running. Try again shortly.")
		return
	}
	out := singbox.UnknownExplanation(req, "authority_unavailable")
	if s.routeExplainer != nil {
		out, err = s.routeExplainer.ExplainRoute(r.Context(), s.store, req)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, s.log, http.StatusNotFound, codeNotFound, "Client no longer exists.")
			return
		}
		if err != nil {
			out = singbox.UnknownExplanation(req, "authority_read_failed")
		}
	} else {
		if _, err = s.store.Peer(r.Context(), req.PeerID); errors.Is(err, store.ErrNotFound) {
			writeError(w, s.log, http.StatusNotFound, codeNotFound, "Client no longer exists.")
			return
		}
	}
	writeJSON(w, s.log, http.StatusOK, out)
}
