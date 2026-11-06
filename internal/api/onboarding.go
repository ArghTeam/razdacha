package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

type OnboardingRequest struct {
	Mode    string `json:"mode"`
	Country string `json:"country"`
}

// OnboardingReview exposes safe observations and opaque review revisions.
type OnboardingReview struct {
	Revision    string `json:"revision"`
	Fingerprint string `json:"fingerprint"`
	ClientMTU   int    `json:"client_mtu"`
	Clients     int    `json:"clients"`
	Rules       int    `json:"rules"`
}
type OnboardingReviewRequest struct {
	Revision    string `json:"revision"`
	Fingerprint string `json:"fingerprint"`
	Confirm     bool   `json:"confirm"`
}

// OnboardingApplier extends the same guarded daemon authority, never a second publisher.
type OnboardingApplier interface {
	ReviewOnboarding(context.Context, *store.Store, OnboardingReviewRequest) (OnboardingReview, error)
	ConfigureOnboarding(context.Context, *store.Store, OnboardingRequest) (store.OnboardingState, error)
	ApplyOnboarding(context.Context, *store.Store) (singbox.ApplyResult, error)
	FinishOnboarding(context.Context, *store.Store) error
	ReopenOnboarding(context.Context, *store.Store, string) (store.OnboardingState, error)
}

func (s *Server) onboardingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrOnboardingConflict):
		writeError(w, s.log, http.StatusConflict, codeConflict, "Setup conflicts with pending changes. Review them before continuing.")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "This setup change is unavailable for existing policy. Keep current configuration or manage it in the panel.")
	default:
		writeError(w, s.log, http.StatusServiceUnavailable, codeNotReady, "Setup was not accepted. Sources may still be loading; retry Apply. Existing policy remains protected.")
	}
}

func (s *Server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	state, err := s.store.Onboarding(r.Context())
	if err != nil {
		s.onboardingError(w, err)
		return
	}
	state.Expected = ""
	writeJSON(w, s.log, http.StatusOK, state)
}

func (s *Server) handleOnboardingAction(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	gate, ok := s.applier.(OnboardingApplier)
	if !ok {
		s.onboardingError(w, errors.New("setup publication unavailable"))
		return
	}
	switch r.PathValue("action") {
	case "reopen":
		var body struct {
			PeerID string `json:"peer_id"`
		}
		if !s.decodeInto(w, r, &body) {
			return
		}
		state, err := gate.ReopenOnboarding(r.Context(), s.store, body.PeerID)
		if err != nil {
			s.onboardingError(w, err)
			return
		}
		state.Expected = ""
		writeJSON(w, s.log, http.StatusOK, state)
	case "review":
		var body OnboardingReviewRequest
		if !s.decodeInto(w, r, &body) {
			return
		}
		review, err := gate.ReviewOnboarding(r.Context(), s.store, body)
		if err != nil {
			s.onboardingError(w, err)
			return
		}
		writeJSON(w, s.log, http.StatusOK, review)
	case "configure":
		var body OnboardingRequest
		if !s.decodeInto(w, r, &body) {
			return
		}
		state, err := gate.ConfigureOnboarding(r.Context(), s.store, body)
		if err != nil {
			s.onboardingError(w, err)
			return
		}
		state.Expected = ""
		writeJSON(w, s.log, http.StatusOK, state)
	case "apply":
		if _, err := gate.ApplyOnboarding(r.Context(), s.store); err != nil {
			s.onboardingError(w, err)
			return
		}
		writeJSON(w, s.log, http.StatusOK, map[string]string{"configuration": "accepted", "connectivity": "not_verified"})
	case "finish":
		if err := gate.FinishOnboarding(r.Context(), s.store); err != nil {
			s.onboardingError(w, err)
			return
		}
		writeJSON(w, s.log, http.StatusOK, map[string]bool{"completed": true})
	default:
		http.NotFound(w, r)
	}
}
