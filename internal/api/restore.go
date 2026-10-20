package api

import (
	"net/http"

	"github.com/ArghTeam/razdacha/internal/restorestate"
)

var restoreChecklist = []string{
	"Review incoming endpoint/public hostname and client endpoint updates.",
	"Review destination WAN interface, routes, firewall, UDP port and WAN/NAT mapping.",
	"Review panel exposure/bindings and TLS certificate SANs/trust.",
	"Audit saved list/catalog URLs, tunnel credentials, notification recipients and scheduled backup delivery before resuming external activity.",
}

func (s *Server) handleRestoreStatus(w http.ResponseWriter, r *http.Request) {
	status, e := s.restore.Status()
	if e != nil {
		writeError(w, s.log, http.StatusServiceUnavailable, "restore_state_invalid", "Trusted restore state is invalid; local recovery is required")
		return
	}
	held, inhibited := false, false
	if status != nil {
		held = status.Held
		inhibited = status.RuntimeInhibited
	}
	writeJSON(w, s.log, http.StatusOK, map[string]any{"restored": status != nil, "held": held, "runtime_inhibited": inhibited, "checklist": restoreChecklist, "recovery_activated": status != nil && status.RecoveryActivated})
}

func (s *Server) handleRestoreResume(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DestinationReviewed          bool `json:"destination_reviewed"`
		ExternalActivityAcknowledged bool `json:"external_activity_acknowledged"`
	}
	if !s.decodeBody(w, r, &request) {
		return
	}
	if !request.DestinationReviewed || !request.ExternalActivityAcknowledged {
		writeError(w, s.log, http.StatusBadRequest, "restore_acknowledgment_required", "Review the destination checklist and acknowledge all saved external callbacks before resuming")
		return
	}
	if e := s.restore.Resume(); e != nil {
		writeError(w, s.log, http.StatusConflict, "restore_resume_failed", "Restore resume failed; safety state is retained")
		return
	}
	s.handleRestoreStatus(w, r)
}

// Only explicit enumerated local routes remain usable while held. Authentication is
// checked first; unknown routes cannot accidentally become external bypasses.
func (s *Server) restoreGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e := s.restore.Allowed(); e == nil {
			next.ServeHTTP(w, r)
			return
		}
		local := false
		switch r.Pattern {
		case "POST /api/logout", "GET /api/session", "GET /api/restore", "POST /api/restore/resume",
			"GET /api/peers", "GET /api/peers/{id}/config", "PATCH /api/peers/{id}", "DELETE /api/peers/{id}",
			"GET /api/tunnels", "GET /api/tunnels/{id}/raw", "GET /api/tunnels/{id}/pool", "POST /api/tunnels", "PATCH /api/tunnels/{id}", "POST /api/tunnels/parse",
			"GET /api/rules", "POST /api/rules", "PUT /api/rules/order", "PATCH /api/rules/{id}", "DELETE /api/rules/{id}",
			"GET /api/lists/community", "GET /api/settings", "PATCH /api/settings", "GET /api/notify", "PUT /api/notify",
			"GET /api/backup", "PUT /api/backup", "GET /api/backup/download", "GET /api/version", "GET /api/diag", "POST /api/diag/run":
			local = true
		case "DELETE /api/tunnels/{id}":
			_, registered, e := s.store.WARPRegistration(r.Context(), r.PathValue("id"))
			local = e == nil && !registered
		}
		// Corrupt state permits only sanitized status; never continue local writes against
		// an uncertain/incomplete database simply because a route is normally local.
		status, e := s.restore.Status()
		if e != nil || status != nil && status.Phase != "complete" {
			local = r.Pattern == "GET /api/restore"
		}
		if !local {
			writeError(w, s.log, http.StatusConflict, "restore_held", restorestate.ErrHeld.Error())
			return
		}
		next.ServeHTTP(w, r)
	})
}
