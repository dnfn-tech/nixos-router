package server

import (
	"context"
	"encoding/json"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
	"log"
	"net/http"
	"time"
)

// ManagedRuntime owns activation and last-good. The HTTP server only owns
// editable intent and the observable job lifecycle.
type ManagedRuntime interface {
	Apply(context.Context, config.Config) (managed.Result, error)
}

func (s *Server) handleManagedApply(w http.ResponseWriter, r *http.Request) {
	if !s.applyMu.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]interface{}{"error": "apply_busy", "message": "Another configuration is being applied", "appliedRuntime": false})
		return
	}
	s.cfgMu.RLock()
	cfg, err := config.LoadFromFile(s.cfgPath)
	s.cfgMu.RUnlock()
	if err != nil {
		s.applyMu.Unlock()
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_config", "message": err.Error()})
		return
	}
	id := newSessionID()
	user, _, _ := s.getSession(r)
	if err = s.db.CreateJob(id, "apply", `{"mode":"managed-runtime","appliedRuntime":false,"stage":"queued"}`); err != nil {
		s.applyMu.Unlock()
		http.Error(w, "cannot create apply job", 500)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"jobId": id, "mode": "managed-runtime", "status": "running", "appliedRuntime": false})
	// Deliver the job identifier before activation can change the LAN address.
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	go func() {
		defer s.applyMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		result, runErr := s.managedRuntime.Apply(ctx, cfg)
		status := "success"
		message := ""
		if runErr != nil {
			status = "failed"
			message = runErr.Error()
		}
		payload := map[string]interface{}{"jobId": id, "mode": "managed-runtime", "status": status, "stage": result.Stage, "appliedRuntime": runErr == nil, "rollbackStatus": result.RollbackStatus, "warnings": result.Warnings}
		if runErr != nil {
			payload["error"] = message
		}
		body, _ := json.Marshal(payload)
		if err := s.db.FinishJob(id, status, string(body), message); err != nil {
			log.Printf("cannot persist terminal apply job %s: %v", id, err)
		}
		s.db.AddAudit(user, "apply", status+" stage="+result.Stage+" rollback="+result.RollbackStatus)
	}()
}

func (s *Server) runtimeConfig() (config.Config, bool) {
	if runtime, ok := s.managedRuntime.(interface{ AppliedConfig() (config.Config, bool) }); ok {
		return runtime.AppliedConfig()
	}
	return s.configSnapshot(), s.managedRuntime == nil
}
