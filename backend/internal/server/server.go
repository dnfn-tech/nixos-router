package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/netip"
	"path/filepath"
	"os"
	"strconv"
	"strings"
	"time"
	"sync"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName         = "nrx_session"
	defaultSessionTTL  = 24 * time.Hour
	maxFailedPerWindow = 5
	failedWindow       = 5 * time.Minute
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
	requireAuth bool
	loginFails map[string][]time.Time
	applyReload bool
	applyMu     sync.Mutex
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
	ApplyReload bool
}

func New(opts Options) *Server {
	s := &Server{
		cfg:       opts.Config,
		cfgPath:   opts.ConfigPath,
		stateDir:  opts.StateDir,
		db:        opts.DB,
		devMode:   opts.DevMode,
		startedAt: time.Now(),
		version:    opts.Version,
		embeddedFS: opts.WebFS,
		webDir:     strings.TrimSpace(opts.WebDir),
		requireAuth: !opts.DevMode,
		loginFails:  make(map[string][]time.Time),
		applyReload: opts.ApplyReload,
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
	mux.HandleFunc("/api/v1/clients", s.handleClients)
	mux.HandleFunc("/api/v1/capabilities/wifi", s.handleWifiCaps)
	mux.HandleFunc("/api/v1/session", s.handleSession)
	mux.HandleFunc("/api/v1/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/v1/apply", s.handleApply)
	mux.HandleFunc("/api/v1/jobs/", s.handleJobByID)
	mux.HandleFunc("/api/v1/jobs", s.handleJobs)
	// Static UI for non-/api paths
	mux.Handle("/", http.HandlerFunc(s.handleSPA))
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	h := s.wrapAuth(mux)
	return s.wrapCORS(h)
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

func (s *Server) wrapAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requireAuth {
			next.ServeHTTP(w, r)
			return
		}
		// Allow: health, POST /session, and static UI (non-/api paths)
		if r.URL.Path == "/api/v1/health" ||
			(r.URL.Path == "/api/v1/session" && r.Method == http.MethodPost) ||
			!strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if s.isSessionValid(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
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
	switch r.Method {
	case http.MethodGet:
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
	case http.MethodPut:
		// accept either full config object or { "config": { ... } }
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var wrapper struct {
			Config *config.Config `json:"config"`
		}
		var newCfg config.Config
		if err := json.Unmarshal(body, &wrapper); err == nil && wrapper.Config != nil {
			newCfg = *wrapper.Config
		} else {
			if err := json.Unmarshal(body, &newCfg); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
		}
		// Preserve secrets if placeholders or empty are provided
		newCfg = s.mergeSecrets(s.cfg, newCfg)
		if err := newCfg.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"errors": []string{err.Error()},
			})
			return
		}
		// persist
		if err := config.SaveToFile(s.cfgPath, newCfg); err != nil {
			http.Error(w, "failed to save config", http.StatusInternalServerError)
			return
		}
		// set new in-memory copy
		s.cfg = newCfg
		user, _, ok := s.getSession(r)
		if !ok {
			user = "unknown"
		}
		s.db.AddAudit(user, "config_save", "saved-only")
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"path":    s.cfgPath,
			"applied": false,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	// Stubbed status derived from config
	lanCIDR := s.cfg.LAN.IPv4CIDR
	resp := map[string]interface{}{
		"system": map[string]interface{}{
			"hostname": s.cfg.System.Hostname,
			"timezone": s.cfg.System.Timezone,
		},
		"wan": map[string]interface{}{
			"iface": s.cfg.WAN.Interface,
			"mode":  s.cfg.WAN.Mode,
			"static": s.cfg.WAN.Static,
			"pppoeUser": func() string {
				if s.cfg.WAN.PPPoE != nil {
					return s.cfg.WAN.PPPoE.Username
				}
				return ""
			}(),
		},
		"lan": map[string]interface{}{
			"bridge": s.cfg.LAN.BridgeName,
			"cidr":   lanCIDR,
			"dhcp":   s.cfg.LAN.DHCP,
			"ports":  s.cfg.LAN.Ports,
		},
		"wifi": map[string]interface{}{
			"enabled": s.cfg.WiFi.Enable,
			"aps":     len(s.cfg.WiFi.APs),
			"guestCount": func() int {
				c := 0
				for _, ap := range s.cfg.WiFi.APs {
					if ap.Guest {
						c++
					}
				}
				return c
			}(),
		},
		"firewall": map[string]interface{}{
			"enabled": s.cfg.Firewall.Enable,
			"nat":     s.cfg.Firewall.NATEnabled,
			"portForwards": s.cfg.Firewall.PortForwards,
		},
		"ssh": map[string]interface{}{
			"enabled": s.cfg.SSH.Enable,
			"port":    s.cfg.SSH.Port,
			"passwordAuth": s.cfg.SSH.PasswordAuth,
		},
		"ddns": map[string]interface{}{
			"enabled":  s.cfg.DDNS.Enable,
			"provider": s.cfg.DDNS.Provider,
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

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	// Prefer a known leases file; else stub from static leases; else empty
	type Client struct {
		IP       string `json:"ip"`
		MAC      string `json:"mac,omitempty"`
		Hostname string `json:"hostname,omitempty"`
		Source   string `json:"source"`
	}
	var clients []Client
	// From static leases
	for _, sl := range s.cfg.LAN.StaticLeases {
		clients = append(clients, Client{IP: sl.IP, MAC: sl.MAC, Hostname: sl.Hostname, Source: "static"})
	}
	// TODO: parse dnsmasq leases when available; mark as stub otherwise
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"clients": clients,
		"source":  "stub",
	})
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
		"bands":  []string{"2g", "5g", "6g"},
		"driver": "stub",
	})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// forward to /session create
	s.handleSession(w, r)
}

func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func isRedactedOrEmpty(s string) bool {
	return s == "" || s == "****"
}

func (s *Server) mergeSecrets(oldCfg config.Config, in config.Config) config.Config {
	out := in
	// PPPoE password
	if in.WAN.Mode == "pppoe" && in.WAN.PPPoE != nil {
		if isRedactedOrEmpty(in.WAN.PPPoE.Password) && oldCfg.WAN.PPPoE != nil {
			pp := *in.WAN.PPPoE
			pp.Password = oldCfg.WAN.PPPoE.Password
			out.WAN.PPPoE = &pp
		}
	}
	// WiFi AP PSKs matched by SSID (fallback: same index)
	if len(in.WiFi.APs) > 0 && len(oldCfg.WiFi.APs) > 0 {
		bySSID := map[string]config.WiFiAP{}
		for _, ap := range oldCfg.WiFi.APs {
			if ap.SSID != "" {
				bySSID[ap.SSID] = ap
			}
		}
		aps := make([]config.WiFiAP, len(in.WiFi.APs))
		copy(aps, in.WiFi.APs)
		for i := range aps {
			if isRedactedOrEmpty(aps[i].PSK) {
				if apOld, ok := bySSID[aps[i].SSID]; ok && apOld.PSK != "" {
					aps[i].PSK = apOld.PSK
				} else if i < len(oldCfg.WiFi.APs) && oldCfg.WiFi.APs[i].SSID == aps[i].SSID && oldCfg.WiFi.APs[i].PSK != "" {
					aps[i].PSK = oldCfg.WiFi.APs[i].PSK
				}
			}
		}
		out.WiFi.APs = aps
	}
	// DDNS tokens/secrets
	if in.DDNS.Provider == oldCfg.DDNS.Provider {
		switch in.DDNS.Provider {
		case "cloudflare":
			if in.DDNS.Cloudflare != nil && oldCfg.DDNS.Cloudflare != nil && isRedactedOrEmpty(in.DDNS.Cloudflare.APIToken) {
				cc := *in.DDNS.Cloudflare
				cc.APIToken = oldCfg.DDNS.Cloudflare.APIToken
				out.DDNS.Cloudflare = &cc
			}
		case "duckdns":
			if in.DDNS.DuckDNS != nil && oldCfg.DDNS.DuckDNS != nil && isRedactedOrEmpty(in.DDNS.DuckDNS.Token) {
				dd := *in.DDNS.DuckDNS
				dd.Token = oldCfg.DDNS.DuckDNS.Token
				out.DDNS.DuckDNS = &dd
			}
		case "aliyun":
			if in.DDNS.Aliyun != nil && oldCfg.DDNS.Aliyun != nil {
				al := *in.DDNS.Aliyun
				if isRedactedOrEmpty(al.AccessKey) {
					al.AccessKey = oldCfg.DDNS.Aliyun.AccessKey
				}
				if isRedactedOrEmpty(al.SecretKey) {
					al.SecretKey = oldCfg.DDNS.Aliyun.SecretKey
				}
				out.DDNS.Aliyun = &al
			}
		case "custom":
			if in.DDNS.Custom != nil && oldCfg.DDNS.Custom != nil && isRedactedOrEmpty(in.DDNS.Custom.Token) {
				cu := *in.DDNS.Custom
				cu.Token = oldCfg.DDNS.Custom.Token
				out.DDNS.Custom = &cu
			}
		}
	}
	return out
}

type creds struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// --- Jobs & Apply (generate-only) ---

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	jobs, err := s.db.ListJobs(limit)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	items := make([]map[string]interface{}, 0, len(jobs))
	for _, j := range jobs {
		items = append(items, map[string]interface{}{
			"id":        j.ID,
			"kind":      j.Kind,
			"status":    j.Status,
			"createdAt": j.CreatedAt.UTC().Format(time.RFC3339),
			"updatedAt": j.UpdatedAt.UTC().Format(time.RFC3339),
			"payload":   j.Payload,
			"error":     j.Error,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"jobs": items})
}

func (s *Server) handleJobByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	j, ok, err := s.db.GetJob(id)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":        j.ID,
		"kind":      j.Kind,
		"status":    j.Status,
		"createdAt": j.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt": j.UpdatedAt.UTC().Format(time.RFC3339),
		"payload":   j.Payload,
		"error":     j.Error,
	})
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// ensure serial apply
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	user, _, ok := s.getSession(r)
	if !ok && s.requireAuth {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	// Load current saved config.json for apply
	cfg, err := config.LoadFromFile(s.cfgPath)
	if err != nil {
		// create failed job
		jid := newSessionID()
		_ = s.db.CreateJob(jid, "apply", `{"mode":"generate-only"}`)
		_ = s.db.UpdateJobStatus(jid, "failed", "failed to load config: "+err.Error())
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_config", "jobId": jid})
		return
	}
	// create running job
	jid := newSessionID()
	if err := s.db.CreateJob(jid, "apply", `{"mode":"generate-only"}`); err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	genDir := filepath.Join(s.stateDir, "generated")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		_ = s.db.UpdateJobStatus(jid, "failed", "mkdir generated: "+err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"jobId": jid})
		return
	}
	// generate fragments
	if err := s.generateFragments(cfg, genDir); err != nil {
		_ = s.db.UpdateJobStatus(jid, "failed", "generate: "+err.Error())
		s.db.AddAudit(user, "apply", "generate-only failed: "+err.Error())
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "generate_failed", "jobId": jid})
		return
	}
	// (Optional) reload stubs
	if s.applyReload {
		// Future: systemctl reload units
	}
	_ = s.db.UpdateJobStatus(jid, "success", "")
	s.db.AddAudit(user, "apply", "generate-only success")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobId":          jid,
		"appliedRuntime": false,
		"mode":           "generate-only",
	})
}

func (s *Server) generateFragments(cfg config.Config, dir string) error {
	// dnsmasq
	var dhcpRange string
	if cfg.LAN.DHCP.Enable {
		if cfg.LAN.DHCP.LeaseMins <= 0 {
			cfg.LAN.DHCP.LeaseMins = 1440
		}
		dhcpRange = fmt.Sprintf("dhcp-range=%s,%s,%dm", cfg.LAN.DHCP.RangeStart, cfg.LAN.DHCP.RangeEnd, cfg.LAN.DHCP.LeaseMins)
	}
	dns := "# Generated by routerd (generate-only)\n"
	dns += "domain-needed\nbogus-priv\nno-resolv\n"
	dns += fmt.Sprintf("interface=%s\n", cfg.LAN.BridgeName)
	if cfg.DNS.Domain != "" {
		dns += fmt.Sprintf("domain=%s\n", cfg.DNS.Domain)
	}
	if dhcpRange != "" {
		dns += dhcpRange + "\n"
	}
	for _, up := range cfg.DNS.Upstreams {
		dns += fmt.Sprintf("server=%s\n", up)
	}
	for _, sl := range cfg.LAN.StaticLeases {
		if sl.MAC != "" && sl.IP != "" {
			if sl.Hostname != "" {
				dns += fmt.Sprintf("dhcp-host=%s,%s,%s\n", sl.MAC, sl.IP, sl.Hostname)
			} else {
				dns += fmt.Sprintf("dhcp-host=%s,%s\n", sl.MAC, sl.IP)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "dnsmasq.conf.fragment"), []byte(dns), 0o644); err != nil {
		return err
	}
	// nftables
	nft := "# Generated by routerd (generate-only)\n"
	nft += "table inet filter {\n  chain input {\n    type filter hook input priority 0; policy drop;\n    ct state established,related accept\n    iifname \"lo\" accept\n    iifname \"" + cfg.LAN.BridgeName + "\" accept\n  }\n}\n"
	nft += "table ip nat {\n  chain postrouting {\n    type nat hook postrouting priority 100; policy accept;\n"
	if cfg.Firewall.NATEnabled {
		ifName := cfg.WAN.Interface
		if ifName == "" {
			ifName = "wan0"
		}
		nft += fmt.Sprintf("    oifname \"%s\" masquerade\n", ifName)
	} else {
		nft += "    # NAT disabled\n"
	}
	nft += "  }\n  chain prerouting {\n    type nat hook prerouting priority -100; policy accept;\n"
	for _, pf := range cfg.Firewall.PortForwards {
		if pf.ExternalPortEnd != 0 && pf.ExternalPortEnd != pf.ExternalPort {
			nft += fmt.Sprintf("    %s dport %d-%d dnat to %s:%d\n", pf.Protocol, pf.ExternalPort, pf.ExternalPortEnd, pf.DestIP, pf.DestPort)
		} else {
			nft += fmt.Sprintf("    %s dport %d dnat to %s:%d\n", pf.Protocol, pf.ExternalPort, pf.DestIP, pf.DestPort)
		}
	}
	nft += "  }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "nftables.nft.fragment"), []byte(nft), 0o644); err != nil {
		return err
	}
	// hostapd
	host := "# Generated by routerd (generate-only)\n"
	if cfg.WiFi.Enable && len(cfg.WiFi.APs) > 0 {
		for i, ap := range cfg.WiFi.APs {
			if !ap.Enable {
				continue
			}
			iface := fmt.Sprintf("wlan%d", i)
			host += fmt.Sprintf("interface=%s\nssid=%s\n", iface, ap.SSID)
			if ap.PSK != "" {
				host += "wpa=2\nwpa_key_mgmt=WPA-PSK\nwpa_passphrase=<redacted>\n"
			} else {
				host += "# open network (not recommended)\n"
			}
			if ap.Guest {
				host += "# guest network\n"
				if ap.Isolate {
					host += "# AP isolation enabled (isolate stations)\n"
				}
			}
			host += "\n"
		}
	} else {
		host += "# wifi disabled\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "hostapd.conf.fragment"), []byte(host), 0o644); err != nil {
		return err
	}
	return nil
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		// Rate-limit failures per client ip
		if !s.checkLoginAllowance(r) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too_many_attempts"})
			return
		}
		var c creds
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(c.Username) == "" || c.Password == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		hash, err := s.db.GetUserPasswordHash(c.Username)
		if err != nil || hash == "" {
			s.onLoginFail(r, c.Username)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(c.Password)) != nil {
			s.onLoginFail(r, c.Username)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
			return
		}
		// Success
		s.resetLoginFail(r)
		sid := newSessionID()
		if err := s.db.CreateSession(sid, c.Username, defaultSessionTTL); err != nil {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		exp := time.Now().Add(defaultSessionTTL)
		s.setSessionCookie(w, sid, exp)
		s.db.AddAudit(c.Username, "login", "success")
		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"user":    c.Username,
			"expires": exp.UTC().Format(time.RFC3339),
		})
	case http.MethodGet:
		user, exp, ok := s.getSession(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"user":    user,
			"expires": exp.UTC().Format(time.RFC3339),
		})
	case http.MethodDelete:
		sid, _ := readSessionCookie(r)
		if sid != "" {
			_ = s.db.DeleteSession(sid)
		}
		// Clear cookie
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		s.db.AddAudit("session", "logout", "by cookie")
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) isSessionValid(r *http.Request) bool {
	_, _, ok := s.getSession(r)
	return ok
}

func (s *Server) getSession(r *http.Request) (string, time.Time, bool) {
	sid, ok := readSessionCookie(r)
	if !ok || sid == "" {
		return "", time.Time{}, false
	}
	user, exp, ok, err := s.db.GetSession(sid)
	if err != nil || !ok {
		return "", time.Time{}, false
	}
	return user, exp, true
}

func readSessionCookie(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	return c.Value, true
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sid string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    sid,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func newSessionID() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func (s *Server) clientKey(r *http.Request) string {
	host, _, _ := strings.Cut(r.RemoteAddr, ":")
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String()
	}
	return host
}

func (s *Server) checkLoginAllowance(r *http.Request) bool {
	now := time.Now()
	key := s.clientKey(r)
	wins, ok := s.loginFails[key]
	if !ok {
		return true
	}
	kept := wins[:0]
	for _, t := range wins {
		if now.Sub(t) <= failedWindow {
			kept = append(kept, t)
		}
	}
	s.loginFails[key] = kept
	return len(kept) < maxFailedPerWindow
}

func (s *Server) onLoginFail(r *http.Request, user string) {
	key := s.clientKey(r)
	s.loginFails[key] = append(s.loginFails[key], time.Now())
	s.db.AddAudit(user, "login", "failed")
}

func (s *Server) resetLoginFail(r *http.Request) {
	key := s.clientKey(r)
	delete(s.loginFails, key)
}

