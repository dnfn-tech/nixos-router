package server

import (
	"net/http"
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
