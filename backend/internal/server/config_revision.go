package server

import (
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// Caller holds cfgMu through comparison, secret merge and durable publication.
func (s *Server) checkConfigRevisionLocked(w http.ResponseWriter, r *http.Request) bool {
	validator := r.Header.Get("If-Match")
	if validator == "" {
		writeJSON(w, http.StatusPreconditionRequired, map[string]interface{}{"code": "config_revision_required", "message": "请先重新加载配置，再保存修改。", "fieldErrors": map[string]string{}})
		return false
	}
	if validator != strconv.Quote(s.cfg.Revision) {
		writeJSON(w, http.StatusConflict, map[string]interface{}{"code": "config_conflict", "message": "配置已被其他会话修改。当前输入已保留，请重新加载后核对并保存。", "fieldErrors": map[string]string{}})
		return false
	}
	return true
}

func (s *Server) saveConfigLocked(w http.ResponseWriter, cfg *config.Config) error {
	if err := cfg.ValidateModuleEdits(s.cfg); err != nil {
		return err
	}
	for id, pc := range cfg.Plugins {
		if pc.Enable && !slices.Contains(s.modules, id) {
			return &config.ValidationError{Path: "plugins." + id + ".enable", Code: "module_unavailable", Message: "module is not allowed by deployment"}
		}
	}
	next, err := config.WithNewRevision(*cfg)
	if err != nil {
		return err
	}
	if err := config.SaveToFile(s.cfgPath, next); err != nil {
		return err
	}
	*cfg = next
	w.Header().Set("ETag", strconv.Quote(next.Revision))
	return nil
}

func writeConfigSaveError(w http.ResponseWriter, err error) {
	var field *config.ValidationError
	if errors.As(err, &field) {
		writeConfigError(w, err)
		return
	}
	apiHTTPError(w, "failed to save config", http.StatusInternalServerError)
}
