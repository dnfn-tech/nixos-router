package server

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
	embeddedFS  fs.FS   // compiled-in assets
	webDir      string  // when set, serve from local dir (dev override)
}

type Options struct {
	Config     config.Config
	ConfigPath string
	StateDir   string
	DB         *db.DB
	DevMode    bool
	Version    string
	WebFS      fs.FS
	WebDir     string
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
		embeddedFS: opts.WebFS,
		webDir:     strings.TrimSpace(opts.WebDir),
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
	// Static UI for non-/api paths
	mux.Handle("/", http.HandlerFunc(s.handleSPA))
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

// handleSPA serves the embedded (or local) UI with SPA fallback to index.html.
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	// Preflight handled in wrapCORS; if still here and it's OPTIONS, return 204
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Only static for non-API paths
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	fsys := s.uiFS()
	// Try to open requested file
	f, err := fsys.Open(path)
	if err != nil {
		// Fallback to index.html (SPA routing)
		s.serveFileFromFS(w, r, fsys, "index.html")
		return
	}
	defer f.Close()
	s.serveOpenedFile(w, r, path, f)
}

func (s *Server) uiFS() fs.FS {
	if s.webDir != "" {
		return os.DirFS(s.webDir)
	}
	if s.embeddedFS != nil {
		return s.embeddedFS
	}
	// Empty FS: always 404
	return emptyFS{}
}

func (s *Server) serveFileFromFS(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	f, err := fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	s.serveOpenedFile(w, r, name, f)
}

func (s *Server) serveOpenedFile(w http.ResponseWriter, r *http.Request, name string, f fs.File) {
	// Set content type based on extension
	if ct := contentTypeByExt(name); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// Read and write
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "failed to read asset", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) { return nil, fs.ErrNotExist }

func contentTypeByExt(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".json"):
		return "application/json; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(name, ".gif"):
		return "image/gif"
	default:
		return "application/octet-stream"
	}
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

