package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
)

type Server struct {
	cfg         config.Config
	cfgPath     string
	stateDir    string
	db          *db.DB
	devMode     bool
	startedAt   time.Time
	version     string
	allowedCORS string // when set (dev) send Access-Control-Allow-Origin
}

type Options struct {
	Config     config.Config
	ConfigPath string
	StateDir   string
	DB         *db.DB
	DevMode    bool
	Version    string
}

func New(opts Options) *Server {
	s := &Server{
		cfg:       opts.Config,
		cfgPath:   opts.ConfigPath,
		stateDir:  opts.StateDir,
		db:        opts.DB,
		devMode:   opts.DevMode,
		startedAt: time.Now(),
		version:   opts.Version,
	}
	if s.devMode {
		// allow all for local-dev unless overridden
		s.allowedCORS = "*"
		if v := os.Getenv("NIXOS_ROUTER_CORS_ORIGIN"); v != "" {
			s.allowedCORS = v
		}
	}
	return s
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/config", s.handleConfig)
	mux.HandleFunc("/api/v1/status", s.handleStatus)
	mux.HandleFunc("/api/v1/ui/nav", s.handleUINav)
	mux.HandleFunc("/api/v1/plugins", s.handlePlugins)
	mux.HandleFunc("/api/v1/capabilities/wifi", s.handleWifiCaps)
	mux.HandleFunc("/api/v1/auth/login", s.handleAuthLogin)
	// simple CORS preflight handler in dev
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			s.applyCORS(w, r)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	return s.wrapCORS(mux)
}

func (s *Server) wrapCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.allowedCORS != "" {
			s.applyCORS(w, r)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) applyCORS(w http.ResponseWriter, r *http.Request) {
	if s.allowedCORS == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", s.allowedCORS)
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Vary", "Origin")
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"ok":         true,
		"version":    s.version,
		"startedAt":  s.startedAt.UTC().Format(time.RFC3339),
		"uptimeSecs": int(time.Since(s.startedAt).Seconds()),
		"dev":        s.devMode,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	// redact by default; redact=0 to return full config (dev convenience)
	redact := true
	if v := r.URL.Query().Get("redact"); v != "" {
		if v == "0" || strings.EqualFold(v, "false") {
			redact = false
		}
	}
	cfg := s.cfg
	if redact {
		cfg = s.cfg.RedactedCopy()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":   s.cfgPath,
		"config": cfg,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	// Stubbed status derived from config
	lanCIDR := s.cfg.LAN.IPv4CIDR
	resp := map[string]interface{}{
		"system": map[string]interface{}{
			"hostname": s.cfg.System.Hostname,
			"timezone": s.cfg.System.Timezone,
		},
		"interfaces": []map[string]interface{}{
			{"name": s.cfg.WAN.Interface, "role": "wan", "mode": s.cfg.WAN.Mode, "up": false},
			{"name": s.cfg.LAN.BridgeName, "role": "lan", "cidr": lanCIDR, "up": true},
		},
		"wifi": map[string]interface{}{
			"enabled": s.cfg.WiFi.Enable,
			"aps":     len(s.cfg.WiFi.APs),
		},
		"firewall": map[string]interface{}{
			"enabled": s.cfg.Firewall.Enable,
			"nat":     s.cfg.Firewall.NATEnabled,
		},
		"ssh": map[string]interface{}{
			"enabled": s.cfg.SSH.Enable,
			"port":    s.cfg.SSH.Port,
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUINav(w http.ResponseWriter, r *http.Request) {
	nav := []map[string]interface{}{
		{"id": "dashboard", "title": "Dashboard", "path": "/"},
		{"id": "network", "title": "Network", "children": []map[string]string{
			{"id": "wan", "title": "WAN", "path": "/wan"},
			{"id": "lan", "title": "LAN", "path": "/lan"},
			{"id": "wifi", "title": "WiFi", "path": "/wifi"},
			{"id": "dns", "title": "DNS", "path": "/dns"},
			{"id": "firewall", "title": "Firewall", "path": "/firewall"},
		}},
		{"id": "system", "title": "System", "path": "/system"},
	}
	// Enabled plugins appended
	for name, p := range s.cfg.Plugins {
		if p.Enable {
			nav = append(nav, map[string]interface{}{
				"id":    "plugin:" + name,
				"title": fmt.Sprintf("Plugin: %s", name),
				"path":  "/plugins/" + name,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"nav": nav})
}

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	list := []map[string]interface{}{}
	for name, p := range s.cfg.Plugins {
		item := map[string]interface{}{
			"name":    name,
			"enabled": p.Enable,
		}
		if p.Config != nil {
			item["configKeys"] = keysOf(p.Config)
		}
		list = append(list, item)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"plugins": list})
}

func (s *Server) handleWifiCaps(w http.ResponseWriter, r *http.Request) {
	maxAP := 2
	if v := os.Getenv("NIXOS_ROUTER_WIFI_MAX_APS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxAP = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"maxAP":  maxAP,
		"bands":  []string{"2g", "5g"}, // stub
		"driver": "stub",
	})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotImplemented, map[string]interface{}{
		"error":  "not_implemented",
		"detail": "Auth will be added in a later milestone",
	})
}

func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

