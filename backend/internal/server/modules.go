package server

import (
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/modules"
)

func (s *Server) moduleManifest() []map[string]interface{} {
	cfg := s.configSnapshot()
	applied, known := s.runtimeConfig()
	if s.managedRuntime == nil {
		known = false
	}
	result := []map[string]interface{}{}
	seen := map[string]bool{}
	for _, d := range modules.All() {
		seen[d.ID] = true
		available := d.Runtime && slices.Contains(s.modules, d.ID)
		item := map[string]interface{}{"id": d.ID, "title": d.Title, "version": d.Version, "ownership": d.Ownership, "capabilities": d.Capabilities, "configKeys": d.ConfigKeys, "configSchema": d.ConfigSchema, "enabled": cfg.Plugins[d.ID].Enable, "available": available, "runtimeSupported": available && s.managedRuntime != nil, "nav": map[string]interface{}{"group": "扩展", "path": d.Path}, "status": "not-applied"}
		if known {
			item["appliedEnabled"] = applied.Plugins[d.ID].Enable
			item["appliedRevision"] = applied.Revision
			item["status"] = "disabled"
			if applied.Plugins[d.ID].Enable {
				item["status"] = "unknown"
				// Query only the fixed owned service. No command arguments come from a plugin.
				if out, err := s.runner.Output("systemctl", "is-active", "nixos-router-dnsmasq.service"); err == nil && strings.TrimSpace(out) == "active" {
					item["status"] = "service-active"
				}
			}
		} else if s.managedRuntime != nil {
			item["status"] = "unknown"
		}
		if !available {
			item["status"] = "unavailable"
		}
		result = append(result, item)
	}
	for id, pc := range cfg.Plugins {
		if seen[id] {
			continue
		}
		result = append(result, map[string]interface{}{"id": id, "title": id, "ownership": "unknown", "enabled": pc.Enable, "available": false, "runtimeSupported": false, "status": "unavailable", "nav": map[string]interface{}{}})
	}
	sort.Slice(result, func(i, j int) bool { return result[i]["id"].(string) < result[j]["id"].(string) })
	return result
}

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/plugins")
	if id != "" {
		id = strings.TrimPrefix(id, "/")
		if id == "" || strings.Contains(id, "/") {
			apiHTTPError(w, "module not found", 404)
			return
		}
	}
	if r.Method == http.MethodGet {
		items := s.moduleManifest()
		if id != "" {
			for _, item := range items {
				if item["id"] == id {
					writeJSON(w, 200, map[string]interface{}{"plugin": item})
					return
				}
			}
			apiHTTPError(w, "module not found", 404)
			return
		}
		writeJSON(w, 200, map[string]interface{}{"plugins": items})
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		apiHTTPError(w, "method not allowed", 405)
		return
	}
	if id == "" {
		apiHTTPError(w, "module ID required", 400)
		return
	}
	var body struct {
		Enabled *bool                  `json:"enabled"`
		Config  map[string]interface{} `json:"config"`
	}
	data, err := io.ReadAll(r.Body)
	if err != nil || config.DecodeStrictJSON(data, &body) != nil {
		apiHTTPError(w, "invalid module configuration", 400)
		return
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if !s.checkConfigRevisionLocked(w, r) {
		return
	}
	cfg := cloneConfig(s.cfg)
	if _, registered := modules.Lookup(id); !registered {
		if _, historical := cfg.Plugins[id]; !historical {
			apiHTTPError(w, "module not found", 404)
			return
		}
	}
	cur := cfg.Plugins[id]
	if body.Config != nil {
		resolved, e := config.ResolvePluginSecrets(s.cfg, map[string]config.PluginConfig{id: {Config: body.Config}})
		if e != nil {
			writeConfigError(w, e)
			return
		}
		cur.Config = mergePreserveSecretsMap(cur.Config, resolved[id].Config)
	}
	if body.Enabled != nil {
		cur.Enable = *body.Enabled
	}
	if cfg.Plugins == nil {
		cfg.Plugins = map[string]config.PluginConfig{}
	}
	cfg.Plugins[id] = cur
	if err = s.saveConfigLocked(w, &cfg); err != nil {
		writeConfigSaveError(w, err)
		return
	}
	s.cfg = cfg
	s.db.AddAudit("plugins", "update", id)
	writeJSON(w, 200, map[string]interface{}{"ok": true, "plugin": map[string]interface{}{"id": id, "enabled": cur.Enable}})
}
