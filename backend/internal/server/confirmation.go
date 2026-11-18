package server

import (
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
	"net/http"
)

func (s *Server) handleConfirmation(w http.ResponseWriter, r *http.Request) {
	control, ok := s.managedRuntime.(managed.ConfirmationControl)
	if !ok {
		apiHTTPError(w, "managed confirmation unavailable", 503)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/apply/candidate" {
		status, err := control.Candidate(r.Context())
		if err != nil {
			apiHTTPError(w, "candidate state unavailable", 503)
			return
		}
		writeJSON(w, 200, map[string]any{"confirmation": status})
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/apply/confirm" {
		apiHTTPError(w, "method not allowed", 405)
		return
	}
	id, err := managed.DecodeConfirmationID(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		apiHTTPError(w, "invalid confirmation request", 400)
		return
	}
	status, err := control.Confirm(r.Context(), id)
	if err != nil {
		writeJSON(w, 409, map[string]any{"error": "confirmation_rejected", "confirmation": status})
		return
	}
	user, _, _ := s.getSession(r)
	s.db.AddAudit(user, "confirm", id)
	writeJSON(w, 200, map[string]any{"confirmation": status})
}
