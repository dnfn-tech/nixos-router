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
	"os/exec"
	"strconv"
	"strings"
	"time"
	"sync"
	"net/url"

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
	runner      CommandRunner
	// Live status helpers
	sysClassNet string // base dir for /sys/class/net (overridable by env)
	ifPrev      map[string]ifacePrev // in-memory last counters sample
	// Traffic control apply (default off)
	applyTrafficControl bool
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
	// ApplyTrafficControl: when true and ApplyReload is also true, attempt to run generated qos.sh.
	// Defaults to false for safety.
	ApplyTrafficControl bool
}

// CommandRunner abstracts command lookups and execution to allow tests to inject a mock
type CommandRunner interface {
	LookPath(name string) error
	Run(name string, args ...string) error
	// Output executes a command and returns its stdout as string.
	Output(name string, args ...string) (string, error)
}

type defaultRunner struct{}

func (defaultRunner) LookPath(name string) error { _, err := exec.LookPath(name); return err }
func (defaultRunner) Run(name string, args ...string) error { return execRun(name, args...) }
func (defaultRunner) Output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

type ifacePrev struct {
	rxBytes uint64
	txBytes uint64
	ts      time.Time
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
		runner:      defaultRunner{},
		ifPrev:      make(map[string]ifacePrev),
		applyTrafficControl: opts.ApplyTrafficControl,
	}
	if s.devMode {
		// allow all for local-dev unless overridden
		s.allowedCORS = "*"
		if v := os.Getenv("NIXOS_ROUTER_CORS_ORIGIN"); v != "" {
			s.allowedCORS = v
		}
	}
	// Resolve sysfs base for net interfaces
	if v := os.Getenv("NIXOS_ROUTER_SYS_CLASS_NET"); strings.TrimSpace(v) != "" {
		s.sysClassNet = strings.TrimSpace(v)
	} else {
		s.sysClassNet = "/sys/class/net"
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
	mux.HandleFunc("/api/v1/clients/", s.handleClientItem)
	mux.HandleFunc("/api/v1/capabilities/wifi", s.handleWifiCaps)
	mux.HandleFunc("/api/v1/session", s.handleSession)
	mux.HandleFunc("/api/v1/session/password", s.handlePasswordChange)
	mux.HandleFunc("/api/v1/account/password", s.handlePasswordChange) // alias
	mux.HandleFunc("/api/v1/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/v1/apply", s.handleApply)
	mux.HandleFunc("/api/v1/jobs/", s.handleJobByID)
	mux.HandleFunc("/api/v1/jobs", s.handleJobs)
	mux.HandleFunc("/api/v1/audit", s.handleAudit)
	mux.HandleFunc("/api/v1/backup", s.handleBackup)
	mux.HandleFunc("/api/v1/system/backup", s.handleBackup) // alias
	mux.HandleFunc("/api/v1/backup/restore", s.handleRestore)
	mux.HandleFunc("/api/v1/system/reboot", s.handleReboot)
	// Static UI for non-/api paths
	mux.Handle("/", http.HandlerFunc(s.handleSPA))
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	h := s.wrapAuth(mux)
	h = s.wrapCSRF(h)
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

// wrapCSRF: for non-GET state-changing requests, when Origin is present, require same-origin;
// allow absence of Origin (curl/tools) and preflight OPTIONS.
func (s *Server) wrapCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		host := r.Host
		// basic same-origin check
		if strings.Contains(origin, host) {
			next.ServeHTTP(w, r)
			return
		}
		// Also allow when Sec-Fetch-Site indicates same-origin
		if r.Header.Get("Sec-Fetch-Site") == "same-origin" {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "CSRF check failed: Origin not allowed", http.StatusForbidden)
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

// --- Live status helpers (best-effort; degrade gracefully) ---
func (s *Server) readFileTrim(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s *Server) readIfaceSysfs(name string) (map[string]interface{}, bool) {
	if name == "" {
		return nil, false
	}
	base := filepath.Join(s.sysClassNet, name)
	if fi, err := os.Stat(base); err != nil || !fi.IsDir() {
		return nil, false
	}
	out := map[string]interface{}{
		"name": name,
	}
	source := "sysfs"
	// operstate
	if v, err := s.readFileTrim(filepath.Join(base, "operstate")); err == nil && v != "" {
		out["up"] = strings.EqualFold(v, "up")
	}
	// speed (Mbps)
	if v, err := s.readFileTrim(filepath.Join(base, "speed")); err == nil && v != "" {
		if n, err2 := strconv.Atoi(v); err2 == nil && n > 0 {
			out["speedMbps"] = n
		}
	}
	// duplex
	if v, err := s.readFileTrim(filepath.Join(base, "duplex")); err == nil && v != "" {
		out["duplex"] = v
	}
	// rx/tx bytes and estimate rate from previous sample
	readUint := func(p string) (uint64, bool) {
		if s, err := s.readFileTrim(p); err == nil && s != "" {
			if n, err2 := strconv.ParseUint(s, 10, 64); err2 == nil {
				return n, true
			}
		}
		return 0, false
	}
	rx, okRx := readUint(filepath.Join(base, "statistics", "rx_bytes"))
	tx, okTx := readUint(filepath.Join(base, "statistics", "tx_bytes"))
	now := time.Now()
	if okRx {
		out["rxBytes"] = rx
	}
	if okTx {
		out["txBytes"] = tx
	}
	ifPrev, okPrev := s.ifPrev[name]
	if okPrev && (okRx || okTx) {
		dt := now.Sub(ifPrev.ts).Seconds()
		if dt > 0 {
			if okRx {
				drx := int64(rx) - int64(ifPrev.rxBytes)
				if drx < 0 {
					drx = 0
				}
				out["rxBps"] = int64(float64(drx) / dt)
			}
			if okTx {
				dtx := int64(tx) - int64(ifPrev.txBytes)
				if dtx < 0 {
					dtx = 0
				}
				out["txBps"] = int64(float64(dtx) / dt)
			}
		}
	}
	// Update prev sample
	if okRx || okTx {
		s.ifPrev[name] = ifacePrev{rxBytes: rx, txBytes: tx, ts: now}
	}
	out["source"] = source
	return out, true
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
	clientCount := 0
	if ls := s.readDnsmasqLeases(); len(ls) > 0 {
		clientCount = len(ls)
	}
	// Interfaces: best-effort live via sysfs + config roles
	var interfaces []map[string]interface{}
	// WAN
	wanIf := s.cfg.WAN.Interface
	if strings.TrimSpace(wanIf) == "" {
		wanIf = "wan0"
	}
	wanItem := map[string]interface{}{"name": wanIf, "role": "wan", "mode": s.cfg.WAN.Mode}
	if live, ok := s.readIfaceSysfs(wanIf); ok {
		for k, v := range live {
			wanItem[k] = v
		}
	}
	interfaces = append(interfaces, wanItem)
	// LAN (bridge)
	lanIf := s.cfg.LAN.BridgeName
	if strings.TrimSpace(lanIf) == "" {
		lanIf = "br-lan"
	}
	lanItem := map[string]interface{}{"name": lanIf, "role": "lan", "cidr": lanCIDR}
	if live, ok := s.readIfaceSysfs(lanIf); ok {
		for k, v := range live {
			lanItem[k] = v
		}
	}
	interfaces = append(interfaces, lanItem)
	// Determine sources summary
	srcs := map[string]string{}
	if (wanItem["source"] == "sysfs") || (lanItem["source"] == "sysfs") {
		srcs["interfaces"] = "sysfs"
	} else {
		srcs["interfaces"] = "stub"
	}
	// Clients source for summary (from leases vs static)
	clientsSource := "stub"
	if ls := s.readDnsmasqLeases(); len(ls) > 0 {
		clientsSource = "leases"
	}
	srcs["clients"] = clientsSource
	// WiFi source (stub for now)
	srcs["wifi"] = "stub"
	// Units (core + plugins): best-effort via systemctl
	units, unitsSource := s.collectUnitStatuses()
	if unitsSource != "" {
		srcs["units"] = unitsSource
	}
	// Last apply job summary if db present
	var lastApply map[string]interface{}
	if s.db != nil && s.db.SQL != nil {
		if jobs, err := s.db.ListJobs(50); err == nil {
			for _, j := range jobs {
				if strings.EqualFold(j.Kind, "apply") {
					// try parse mode from payload ({"mode":"..."})
					mode := ""
					if strings.TrimSpace(j.Payload) != "" {
						var p map[string]interface{}
						if json.Unmarshal([]byte(j.Payload), &p) == nil {
							if mv, ok := p["mode"].(string); ok {
								mode = mv
							}
						}
					}
					lastApply = map[string]interface{}{
						"id":        j.ID,
						"status":    j.Status,
						"updatedAt": j.UpdatedAt.UTC().Format(time.RFC3339),
					}
					if mode != "" {
						lastApply["mode"] = mode
					}
					srcs["apply"] = "db"
					break
				}
			}
		}
	}
	if lastApply == nil {
		srcs["apply"] = "stub"
	}
	resp := map[string]interface{}{
		"system": map[string]interface{}{
			"hostname": s.cfg.System.Hostname,
			"timezone": s.cfg.System.Timezone,
		},
		"interfaces": interfaces,
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
			"clientCount": clientCount,
			"guestCount": func() int {
				c := 0
				for _, ap := range s.cfg.WiFi.APs {
					if ap.Guest {
						c++
					}
				}
				return c
			}(),
			"source": "stub",
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
		"units": units,
		"sources": srcs,
	}
	if lastApply != nil {
		resp["lastApply"] = lastApply
	}
	writeJSON(w, http.StatusOK, resp)
}

// collectUnitStatuses queries known core/plugin units through systemctl (when present).
// Returns list and a source marker: "systemctl" or "stub".
func (s *Server) collectUnitStatuses() ([]map[string]string, string) {
	known := []struct {
		id   string
		unit string
	}{
		// Core
		{"dnsmasq", "dnsmasq.service"},
		{"nftables", "nftables.service"},
		{"hostapd", "hostapd.service"},
	}
	// Plugins
	// mihomo (clash/Clash.Meta family commonly packaged as mihomo.service)
	known = append(known,
		struct{ id, unit string }{"mihomo", "mihomo.service"},
		struct{ id, unit string }{"tailscaled", "tailscaled.service"},
		struct{ id, unit string }{"zerotier-one", "zerotier-one.service"},
	)
	// headscale only when relevant (tailscale controlPlane=headscale)
	if pc, ok := s.cfg.Plugins["tailscale"]; ok {
		if cp, _ := pc.Config["controlPlane"].(string); strings.EqualFold(cp, "headscale") {
			known = append(known, struct{ id, unit string }{"headscale", "headscale.service"})
		}
	}
	// If systemctl is absent, return stub states
	if err := s.runner.LookPath("systemctl"); err != nil {
		out := make([]map[string]string, 0, len(known))
		for _, k := range known {
			out = append(out, map[string]string{
				"id":    k.id,
				"unit":  k.unit,
				"state": "unknown",
			})
		}
		return out, "stub"
	}
	// Query is-active for each unit
	mapState := func(stdout string, err error) string {
		if err != nil {
			// When unit missing, many systemd return codes set stderr; treat as missing
			// Prefer to classify as "missing" instead of "inactive" for clarity
			low := strings.ToLower(stdout)
			if strings.Contains(low, "not-found") || strings.Contains(low, "not found") {
				return "missing"
			}
			return "missing"
		}
		s := strings.ToLower(strings.TrimSpace(stdout))
		switch s {
		case "active":
			return "active"
		case "inactive":
			return "inactive"
		case "failed":
			return "failed"
		default:
			// activating/deactivating/unknown etc.
			return "unknown"
		}
	}
	out := make([]map[string]string, 0, len(known))
	for _, k := range known {
		stdout, err := s.runner.Output("systemctl", "is-active", k.unit)
		state := mapState(stdout, err)
		// Special-case: when systemctl is present but unit truly not found, err likely non-nil and stdout empty.
		out = append(out, map[string]string{
			"id":    k.id,
			"unit":  k.unit,
			"state": state,
		})
	}
	return out, "systemctl"
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
			{"id": "ddns", "title": "DDNS", "path": "/ddns"},
			{"id": "ipv6", "title": "IPv6", "path": "/ipv6"},
			{"id": "qos", "title": "QoS", "path": "/qos"},
			{"id": "parental", "title": "Parental", "path": "/parental"},
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
	if r.Method == http.MethodGet {
		builtins := []struct {
			id    string
			title string
			group string
			path  string
		}{
			{"adblock", "AdBlock", "网络服务", "/adblock"},
			{"traffic", "Traffic", "网络服务", "/traffic"},
			{"mihomo", "Mihomo", "网络服务", "/mihomo"},
			{"vlan", "VLAN", "网络服务", "/vlan"},
			{"tailscale", "Tailscale", "网络服务", "/tailscale"},
			{"zerotier", "Zerotier", "网络服务", "/zerotier"},
			{"qos", "QoS", "网络服务", "/qos"},
			{"parental", "家长控制", "网络服务", "/parental"},
			{"ddns", "动态DNS", "网络服务", "/ddns"},
			{"ipv6", "IPv6", "网络服务", "/ipv6"},
		}
		list := []map[string]interface{}{}
		seen := map[string]bool{}
		for _, b := range builtins {
			p := s.cfg.Plugins[b.id]
			item := map[string]interface{}{
				"id":      b.id,
				"title":   b.title,
				"enabled": p.Enable,
				"nav": map[string]interface{}{
					"group": b.group,
					"path":  b.path,
				},
			}
			if p.Config != nil {
				item["configKeys"] = keysOf(p.Config)
			}
			list = append(list, item)
			seen[b.id] = true
		}
		// Include any extra plugin entries not in builtins
		for name, p := range s.cfg.Plugins {
			if seen[name] {
				continue
			}
			list = append(list, map[string]interface{}{
				"id":      name,
				"title":   name,
				"enabled": p.Enable,
				"nav":     map[string]interface{}{},
				"configKeys": func() []string {
					if p.Config != nil {
						return keysOf(p.Config)
					}
					return nil
				}(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"plugins": list})
		return
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPatch {
		// PUT /api/v1/plugins/{id}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/plugins/")
		if id == "" || strings.Contains(id, "/") {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var body struct {
			Enabled *bool                  `json:"enabled"`
			Config  map[string]interface{} `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		cur := s.cfg.Plugins[id]
		// Merge config with secret-preserve
		if body.Config != nil {
			cur.Config = mergePreserveSecretsMap(cur.Config, body.Config)
		}
		if body.Enabled != nil {
			cur.Enable = *body.Enabled
		}
		if s.cfg.Plugins == nil {
			s.cfg.Plugins = map[string]config.PluginConfig{}
		}
		s.cfg.Plugins[id] = cur
		// persist
		if err := config.SaveToFile(s.cfgPath, s.cfg); err != nil {
			http.Error(w, "failed to save config", http.StatusInternalServerError)
			return
		}
		s.db.AddAudit("plugins", "update", fmt.Sprintf("%s enabled=%v", id, cur.Enable))
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":     true,
			"plugin": map[string]interface{}{"id": id, "enabled": cur.Enable, "configKeys": keysOf(cur.Config)},
		})
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Prefer a known leases file; else stub from static leases; else empty
	type Client struct {
		IP       string `json:"ip"`
		MAC      string `json:"mac,omitempty"`
		Hostname string `json:"hostname,omitempty"`
		Source   string `json:"source"`
		Expires  string `json:"expires,omitempty"`
		LastSeen string `json:"lastSeen,omitempty"`
		Blocked  bool   `json:"blocked"`
	}
	var clients []Client
	source := "stub"
	leases := s.readDnsmasqLeases()
	if len(leases) > 0 {
		source = "leases"
		// index static leases for hostnames
		mac2host := map[string]string{}
		ip2host := map[string]string{}
		for _, sl := range s.cfg.LAN.StaticLeases {
			if sl.MAC != "" && sl.Hostname != "" {
				mac2host[strings.ToLower(sl.MAC)] = sl.Hostname
			}
			if sl.IP != "" && sl.Hostname != "" {
				ip2host[sl.IP] = sl.Hostname
			}
		}
		for _, L := range leases {
			h := L.Hostname
			if h == "" && L.MAC != "" {
				if v, ok := mac2host[strings.ToLower(L.MAC)]; ok {
					h = v
					source = "mixed"
				}
			}
			if h == "" {
				if v, ok := ip2host[L.IP]; ok {
					h = v
					source = "mixed"
				}
			}
			expires := ""
			if !L.Expires.IsZero() {
				expires = L.Expires.UTC().Format(time.RFC3339)
			}
			clients = append(clients, Client{
				IP: L.IP, MAC: L.MAC, Hostname: h, Source: source, Expires: expires, LastSeen: expires,
				Blocked: s.isMacBlocked(L.MAC),
			})
		}
	} else {
		// From static leases fallback
		for _, sl := range s.cfg.LAN.StaticLeases {
			clients = append(clients, Client{IP: sl.IP, MAC: sl.MAC, Hostname: sl.Hostname, Source: "static", Blocked: s.isMacBlocked(sl.MAC)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"clients": clients, "source": source})
}

func (s *Server) handleClientItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Identify MAC from path or query
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/clients/")
	if id == "" {
		id = r.URL.Query().Get("mac")
	}
	if id == "" {
		http.Error(w, "missing client id (mac)", http.StatusBadRequest)
		return
	}
	// URL-decoding for path mac
	id, _ = url.QueryUnescape(id)
	mac := strings.ToLower(id)
	var body struct {
		Hostname *string `json:"hostname"`
		Blocked  *bool   `json:"blocked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// update static lease hostname
	found := false
	for i := range s.cfg.LAN.StaticLeases {
		if strings.ToLower(s.cfg.LAN.StaticLeases[i].MAC) == mac {
			if body.Hostname != nil {
				s.cfg.LAN.StaticLeases[i].Hostname = *body.Hostname
			}
			found = true
			break
		}
	}
	if !found {
		// create entry if hostname provided
		sl := config.StaticLease{MAC: strings.ToUpper(mac)}
		if body.Hostname != nil {
			sl.Hostname = *body.Hostname
		}
		s.cfg.LAN.StaticLeases = append(s.cfg.LAN.StaticLeases, sl)
	}
	// update blocked list
	if body.Blocked != nil {
		if *body.Blocked {
			if !s.isMacBlocked(mac) {
				s.cfg.Firewall.BlockedMACs = append(s.cfg.Firewall.BlockedMACs, strings.ToUpper(mac))
			}
		} else {
			// remove if present
			out := s.cfg.Firewall.BlockedMACs[:0]
			for _, m := range s.cfg.Firewall.BlockedMACs {
				if strings.ToLower(m) != mac {
					out = append(out, m)
				}
			}
			s.cfg.Firewall.BlockedMACs = out
		}
	}
	// persist
	if err := s.cfg.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"errors": []string{err.Error()}})
		return
	}
	if err := config.SaveToFile(s.cfgPath, s.cfg); err != nil {
		http.Error(w, "failed to save config", http.StatusInternalServerError)
		return
	}
	s.db.AddAudit("clients", "patch", mac)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
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

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, _, ok := s.getSession(r)
	if !ok && s.requireAuth {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.NewPassword) == "" || len(body.NewPassword) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "新密码太短（至少 8 位）"})
		return
	}
	// verify current
	hash, err := s.db.GetUserPasswordHash(user)
	if err != nil || hash == "" {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.CurrentPassword)) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "当前密码不正确"})
		return
	}
	// update
	newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if err := s.db.UpdateUserPassword(user, string(newHash)); err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	// invalidate other sessions
	if sid, _ := readSessionCookie(r); sid != "" {
		_ = s.db.DeleteSessionsByUserExcept(user, sid)
	}
	s.db.AddAudit(user, "password_change", "success")
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
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

func mergePreserveSecretsMap(old, in map[string]interface{}) map[string]interface{} {
	if old == nil && in == nil {
		return nil
	}
	if old == nil {
		return in
	}
	if in == nil {
		return old
	}
	out := map[string]interface{}{}
	// include all keys from either map
	seen := map[string]bool{}
	for k := range old {
		seen[k] = true
	}
	for k := range in {
		seen[k] = true
	}
	for k := range seen {
		ov, okOld := old[k]
		nv, okNew := in[k]
		if okNew {
			// If redacted string, keep old
			if sv, ok := nv.(string); ok && isRedactedOrEmpty(sv) {
				out[k] = ov
				continue
			}
			// Recurse into maps
			if nm, ok := nv.(map[string]interface{}); ok {
				if om, ok2 := ov.(map[string]interface{}); ok2 {
					out[k] = mergePreserveSecretsMap(om, nm)
					continue
				}
			}
			out[k] = nv
		} else if okOld {
			out[k] = ov
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

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	items, err := s.db.ListAudit(limit)
	if err != nil {
		// If table missing or error, stub empty list with shape
		writeJSON(w, http.StatusOK, map[string]interface{}{"audit": []interface{}{}, "source": "stub"})
		return
	}
	out := make([]map[string]interface{}, 0, len(items))
	for _, a := range items {
		out = append(out, map[string]interface{}{
			"id":     a.ID,
			"at":     a.TS.UTC().Format(time.RFC3339),
			"actor":  a.Actor,
			"action": a.Action,
			"detail": a.Detail,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"audit": out})
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Always return JSON bundle of config for v1
	cfg, err := config.LoadFromFile(s.cfgPath)
	if err != nil {
		http.Error(w, "failed to load config", http.StatusInternalServerError)
		return
	}
	filename := "nixos-router-backup-" + time.Now().UTC().Format("20060102") + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"kind":   "nixos-router-backup",
		"format": "config+json",
		"config": cfg, // include secrets
		"createdAt": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Try multipart first
	ct := r.Header.Get("Content-Type")
	var newCfg config.Config
	var err error
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(10 << 20); err != nil { // 10MB
			http.Error(w, "bad multipart form", http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		var wrapper struct{ Config *config.Config `json:"config"` }
		if json.Unmarshal(data, &wrapper) == nil && wrapper.Config != nil {
			newCfg = *wrapper.Config
		} else if json.Unmarshal(data, &newCfg) != nil {
			http.Error(w, "invalid JSON in backup", http.StatusBadRequest)
			return
		}
	} else {
		// JSON body: either {config:{...}} or raw config object
		body, _ := io.ReadAll(r.Body)
		var wrapper struct{ Config *config.Config `json:"config"` }
		if json.Unmarshal(body, &wrapper) == nil && wrapper.Config != nil {
			newCfg = *wrapper.Config
		} else if json.Unmarshal(body, &newCfg) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	}
	// Secret-aware merge to preserve placeholders if present
	newCfg = s.mergeSecrets(s.cfg, newCfg)
	// Also merge plugin configs preserving redacted
	if newCfg.Plugins != nil {
		for pid, pc := range newCfg.Plugins {
			if old, ok := s.cfg.Plugins[pid]; ok {
				pc.Config = mergePreserveSecretsMap(old.Config, pc.Config)
				newCfg.Plugins[pid] = pc
			}
		}
	}
	if err = newCfg.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"errors": []string{err.Error()}})
		return
	}
	if err := config.SaveToFile(s.cfgPath, newCfg); err != nil {
		http.Error(w, "failed to save config", http.StatusInternalServerError)
		return
	}
	s.cfg = newCfg
	s.db.AddAudit("system", "restore", "config-only saved")
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "applied": false})
}

func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	allow := os.Getenv("NIXOS_ROUTER_ALLOW_REBOOT") == "1"
	if s.devMode || !allow {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "reboot disabled"})
		return
	}
	// schedule reboot asynchronously
	go func() {
		// try systemctl reboot, fallback to /sbin/reboot
		_ = execCommand("systemctl", "reboot")
		_ = execCommand("/sbin/reboot")
	}()
	s.db.AddAudit("system", "reboot", "scheduled")
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "scheduled": true})
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
	jobMode := "generate-only"
	if s.applyReload {
		jobMode = "generate+reload"
	}
	if err := s.db.CreateJob(jid, "apply", fmt.Sprintf(`{"mode":"%s"}`, jobMode)); err != nil {
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
		// core networking units
		okReload, msg := s.reloadCoreUnits()
		// plugin orchestration - non-fatal notes
		notes := s.orchestratePlugins(cfg)
		// Optional: traffic control script execution when explicitly enabled
		if s.applyTrafficControl {
			script := filepath.Join(genDir, "qos.sh")
			if fi, err := os.Stat(script); err == nil && fi.Mode().Perm()&0o111 != 0 {
				// Best-effort run; failures are noted but do not fail the apply
				if err := s.runner.Run(script); err != nil {
					notes = append(notes, "qos: failed to run qos.sh (ignored): "+err.Error())
				} else {
					notes = append(notes, "qos: executed qos.sh")
				}
			} else {
				notes = append(notes, "qos: script not present or not executable; skipped")
			}
		} else {
			notes = append(notes, "qos: applyTrafficControl=false; generation only")
		}
		if !okReload {
			_ = s.db.UpdateJobStatus(jid, "failed", msg)
			s.db.AddAudit(user, "apply", "reload failed: "+msg)
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"jobId":          jid,
				"appliedRuntime": false,
				"mode":           "generate+reload",
				"error":          msg,
				"notes":          notes,
			})
			return
		}
		_ = s.db.UpdateJobStatus(jid, "success", "")
		s.db.AddAudit(user, "apply", "reload success")
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"jobId":          jid,
			"appliedRuntime": true,
			"mode":           "generate+reload",
			"notes":          notes,
		})
		return
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
	// Blocked MACs drop (in filter input chain)
	if len(cfg.Firewall.BlockedMACs) > 0 {
		nft += "add table inet routerd_tmp\n"
		nft += "add chain inet routerd_tmp input { type filter hook input priority 1; policy accept; }\n"
		for _, mac := range cfg.Firewall.BlockedMACs {
			nft += fmt.Sprintf("add rule inet routerd_tmp input ether saddr %s drop\n", strings.ToLower(mac))
		}
	}
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
	// IPv6 notes
	ipv6 := "# IPv6 generate-only\n"
	ipv6 += fmt.Sprintf("# wanMode=%s lanPD=%v lanRA=%v\n", cfg.IPv6.WANMode, cfg.IPv6.LANPD, cfg.IPv6.LANRA)
	_ = os.WriteFile(filepath.Join(dir, "ipv6.nft.fragment"), []byte(ipv6), 0o644)
	// DDNS env notes (redacted)
	ddns := "# DDNS generate-only\n"
	ddns += fmt.Sprintf("# provider=%s enabled=%v\n", cfg.DDNS.Provider, cfg.DDNS.Enable)
	_ = os.WriteFile(filepath.Join(dir, "ddns.env.fragment"), []byte(ddns), 0o644)
	// QoS: when enabled, generate concrete tc script; always also write notes
	{
		qosNotes := "# QoS generate-only (notes)\n"
		qosNotes += fmt.Sprintf("# upMbps=%d downMbps=%d\n", cfg.QoS.UpMbps, cfg.QoS.DownMbps)
		qosNotes += "# example (egress): tc qdisc replace dev <wan-if> root fq_codel\n"
		qosNotes += "# example (ingress): tc qdisc replace dev <lan-bridge> handle ffff: ingress\n"
		_ = os.WriteFile(filepath.Join(dir, "qos.conf.fragment"), []byte(qosNotes), 0o644)
		if cfg.QoS.Enable && (cfg.QoS.UpMbps > 0 || cfg.QoS.DownMbps > 0) {
			wan := s.cfg.WAN.Interface
			if strings.TrimSpace(wan) == "" {
				wan = "wan0"
			}
			lan := s.cfg.LAN.BridgeName
			if strings.TrimSpace(lan) == "" {
				lan = "br-lan"
			}
			var sb strings.Builder
			sb.WriteString("#!/usr/bin/env sh\n")
			sb.WriteString("# Generated by routerd (generate-only); idempotent-ish tc setup\n")
			sb.WriteString("set -eu\n")
			// Egress (WAN) shaping
			if cfg.QoS.UpMbps > 0 {
				up := cfg.QoS.UpMbps
				sb.WriteString(fmt.Sprintf("tc qdisc replace dev %s root handle 1: htb default 30 || true\n", wan))
				sb.WriteString(fmt.Sprintf("tc class replace dev %s parent 1: classid 1:1 htb rate %dmbit ceil %dmbit || true\n", wan, up, up))
				sb.WriteString(fmt.Sprintf("tc qdisc replace dev %s parent 1:1 handle 10: fq_codel || true\n", wan))
			}
			// Ingress (LAN bridge) policing via ingress qdisc (simplified)
			if cfg.QoS.DownMbps > 0 {
				down := cfg.QoS.DownMbps
				sb.WriteString(fmt.Sprintf("tc qdisc replace dev %s handle ffff: ingress || true\n", lan))
				// Replace all u32 filters with a single police rule (simple cap)
				sb.WriteString(fmt.Sprintf("tc filter replace dev %s parent ffff: protocol all u32 match u32 0 0 police rate %dmbit burst %dkbit drop flowid :1 || true\n",
					lan, down, down*64))
			}
			path := filepath.Join(dir, "qos.sh")
			_ = os.WriteFile(path, []byte(sb.String()), 0o755)
		}
	}
	// Parental: when enabled, generate concrete nftables set+drop chain; else notes
	{
		if cfg.Parental.Enable {
			var b strings.Builder
			b.WriteString("# Generated by routerd (generate-only)\n")
			b.WriteString("table inet routerd_parental {\n")
			// Build blocked macs set from firewall.blockedMacs and parental rules with action=block
			elem := []string{}
			for _, m := range cfg.Firewall.BlockedMACs {
				if strings.TrimSpace(m) != "" {
					elem = append(elem, strings.ToLower(m))
				}
			}
			for _, r := range cfg.Parental.Rules {
				if strings.EqualFold(r.Action, "block") {
					for _, m := range r.TargetMACs {
						if strings.TrimSpace(m) != "" {
							elem = append(elem, strings.ToLower(m))
						}
					}
				}
			}
			b.WriteString("  set blocked_macs { type ether_addr; flags interval; ")
			if len(elem) > 0 {
				b.WriteString("elements = { ")
				for i, m := range elem {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(m)
				}
				b.WriteString(" }")
			}
			b.WriteString(" }\n")
			// Input chain drop rule referencing the set; priority 1 so it runs after main input rules
			b.WriteString("  chain input {\n")
			b.WriteString("    type filter hook input priority 1; policy accept;\n")
			b.WriteString("    ether saddr @blocked_macs drop\n")
			// Emit schedules as comments (not enforced)
			for _, r := range cfg.Parental.Rules {
				if strings.TrimSpace(r.Schedule) != "" {
					b.WriteString(fmt.Sprintf("    # schedule note: action=%s targetMacs=%v schedule=%q (not enforced)\n",
						r.Action, r.TargetMACs, r.Schedule))
				}
			}
			b.WriteString("  }\n}\n")
			_ = os.WriteFile(filepath.Join(dir, "parental.nft.fragment"), []byte(b.String()), 0o644)
		} else {
			par := "# Parental generate-only (notes)\n"
			par += fmt.Sprintf("# rules=%d\n", len(cfg.Parental.Rules))
			par += "\n# nftables skeleton:\n"
			par += "# table inet routerd_parental { set blocked_macs { type ether_addr; flags interval; }\n"
			par += "#   chain input { type filter hook input priority 1; policy accept; }\n"
			par += "# }\n"
			_ = os.WriteFile(filepath.Join(dir, "parental.conf.fragment"), []byte(par), 0o644)
		}
	}
	// Plugins notes (enabled/disabled)
	for _, pid := range []string{"adblock", "traffic", "mihomo", "vlan", "tailscale", "zerotier"} {
		fn := filepath.Join(dir, "plugin."+pid+".notes")
		if p, ok := cfg.Plugins[pid]; ok && p.Enable {
			_ = os.WriteFile(fn, []byte("# "+pid+" enabled\n"), 0o644)
		} else {
			_ = os.WriteFile(fn, []byte("# "+pid+" disabled\n"), 0o644)
		}
	}
	// Plugins config generation
	pdir := filepath.Join(dir, "plugins")
	_ = os.MkdirAll(pdir, 0o755)
	// adblock
	genAdblock := func() {
		id := "adblock"
		pc, ok := cfg.Plugins[id]
		if !ok || !pc.Enable {
			_ = os.WriteFile(filepath.Join(pdir, id+".DISABLED"), []byte("# disabled\n"), 0o644)
			return
		}
		var lines []string
		lines = append(lines, "# adblock fragment (generate-only)")
		lines = append(lines, "# units: dnsmasq.service (reload: try-reload-or-restart)")
		if lists, ok := pc.Config["lists"].([]interface{}); ok {
			for _, v := range lists {
				if s, ok := v.(string); ok {
					lines = append(lines, "# list: "+s)
				}
			}
		}
		if entries, ok := pc.Config["entries"].([]interface{}); ok {
			for _, v := range entries {
				if d, ok := v.(string); ok && d != "" {
					lines = append(lines, "address=/"+d+"/0.0.0.0")
				}
			}
		}
		_ = os.WriteFile(filepath.Join(pdir, "adblock.conf.fragment"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
	genAdblock()
	// traffic
	genTraffic := func() {
		id := "traffic"
		pc, ok := cfg.Plugins[id]
		if !ok || !pc.Enable {
			_ = os.WriteFile(filepath.Join(pdir, id+".DISABLED"), []byte("# disabled\n"), 0o644)
			return
		}
		var lines []string
		lines = append(lines, "# traffic retention config (generate-only)")
		lines = append(lines, "# units: (none)  // collector TBD")
		if v, ok := pc.Config["retentionDays"]; ok {
			lines = append(lines, fmt.Sprintf("retentionDays=%v", v))
		}
		_ = os.WriteFile(filepath.Join(pdir, "traffic.conf.fragment"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
	genTraffic()
	// mihomo
	genMihomo := func() {
		id := "mihomo"
		pc, ok := cfg.Plugins[id]
		if !ok || !pc.Enable {
			_ = os.WriteFile(filepath.Join(pdir, id+".DISABLED"), []byte("# disabled\n"), 0o644)
			return
		}
		// Minimal YAML skeleton from config
		var b strings.Builder
		b.WriteString("# mihomo.yaml (generate-only)\n")
		b.WriteString("# units: mihomo.service (reload: try-reload-or-restart)\n")
		if profile, ok := pc.Config["profile"].(string); ok && profile != "" {
			fmt.Fprintf(&b, "profile: %q\n", profile)
		}
		if mode, ok := pc.Config["mode"].(string); ok && mode != "" {
			fmt.Fprintf(&b, "mode: %q\n", mode)
		}
		if dns, ok := pc.Config["dns"].(map[string]interface{}); ok {
			if port, ok := dns["port"]; ok {
				fmt.Fprintf(&b, "dns:\n  port: %v\n", port)
			}
		}
		if tun, ok := pc.Config["tun"].(map[string]interface{}); ok {
			if en, ok := tun["enable"]; ok {
				fmt.Fprintf(&b, "tun:\n  enable: %v\n", en)
			}
		}
		_ = os.WriteFile(filepath.Join(pdir, "mihomo.yaml"), []byte(b.String()), 0o644)
	}
	genMihomo()
	// vlan
	genVLAN := func() {
		id := "vlan"
		pc, ok := cfg.Plugins[id]
		if !ok || !pc.Enable {
			_ = os.WriteFile(filepath.Join(pdir, id+".DISABLED"), []byte("# disabled\n"), 0o644)
			return
		}
		var lines []string
		lines = append(lines, "# vlan netdev/network fragment (generate-only)")
		lines = append(lines, "# units: (notes only, apply with systemd-networkd if present)")
		if vlans, ok := pc.Config["vlans"].([]interface{}); ok {
			for _, v := range vlans {
				if m, ok := v.(map[string]interface{}); ok {
					vid := m["vid"]
					name := m["name"]
					bridge := m["bridge"]
					lines = append(lines, fmt.Sprintf("# vid=%v name=%v bridge=%v", vid, name, bridge))
				}
			}
		}
		_ = os.WriteFile(filepath.Join(pdir, "vlan.network.fragment"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
	genVLAN()
	// tailscale
	genTailscale := func() {
		id := "tailscale"
		pc, ok := cfg.Plugins[id]
		if !ok || !pc.Enable {
			_ = os.WriteFile(filepath.Join(pdir, id+".DISABLED"), []byte("# disabled\n"), 0o644)
			return
		}
		var lines []string
		lines = append(lines, "# tailscale flags/env (generate-only)")
		cp, _ := pc.Config["controlPlane"].(string)
		loginServer, _ := pc.Config["loginServer"].(string)
		authKey, _ := pc.Config["authKey"].(string)
		lines = append(lines, "controlPlane="+cp)
		// Document units for orchestration
		lines = append(lines, "# units: tailscaled.service (reload: try-reload-or-restart)")
		if strings.EqualFold(cp, "headscale") {
			lines = append(lines, "# extra-units: headscale.service (when self-hosted controller is on this host)")
		}
		if loginServer != "" {
			lines = append(lines, "loginServer="+loginServer)
		}
		if authKey != "" {
			lines = append(lines, "authKey=****") // redacted
		}
		_ = os.WriteFile(filepath.Join(pdir, "tailscale.env.fragment"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
	genTailscale()
	// zerotier
	genZerotier := func() {
		id := "zerotier"
		pc, ok := cfg.Plugins[id]
		if !ok || !pc.Enable {
			_ = os.WriteFile(filepath.Join(pdir, id+".DISABLED"), []byte("# disabled\n"), 0o644)
			return
		}
		var lines []string
		lines = append(lines, "# zerotier config (generate-only)")
		lines = append(lines, "# units: zerotier-one.service (reload: try-reload-or-restart)")
		cp, _ := pc.Config["controlPlane"].(string)
		ctrl, _ := pc.Config["controllerUrl"].(string)
		api, _ := pc.Config["apiToken"].(string)
		lines = append(lines, "controlPlane="+cp)
		if ctrl != "" {
			lines = append(lines, "controllerUrl="+ctrl)
		}
		if api != "" {
			lines = append(lines, "apiToken=****")
		}
		if nets, ok := pc.Config["networks"].([]interface{}); ok {
			for _, v := range nets {
				if s, ok := v.(string); ok && s != "" {
					lines = append(lines, "join="+s)
				}
			}
		}
		_ = os.WriteFile(filepath.Join(pdir, "zerotier.conf.fragment"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
	genZerotier()
	return nil
}

type dnsmasqLease struct {
	Expires time.Time
	MAC     string
	IP      string
	Hostname string
	ClientID string
}

func (s *Server) readDnsmasqLeases() []dnsmasqLease {
	paths := []string{}
	if p := os.Getenv("NIXOS_ROUTER_DNSMASQ_LEASES"); p != "" {
		paths = append(paths, p)
	}
	paths = append(paths,
		"/var/lib/misc/dnsmasq.leases",
		filepath.Join(s.stateDir, "dnsmasq.leases"),
		filepath.Join(s.stateDir, "generated", "dnsmasq.leases"),
	)
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			if ls := parseDnsmasqLeasesFile(p); len(ls) > 0 {
				return ls
			}
		}
	}
	return nil
}

func parseDnsmasqLeasesFile(path string) []dnsmasqLease {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var out []dnsmasqLease
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Format: <expiry> <mac|duid> <ip> <hostname> <client-id or *>
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		var exp time.Time
		if secs, err := strconv.ParseInt(fields[0], 10, 64); err == nil && secs > 0 {
			exp = time.Unix(secs, 0)
		}
		mac := fields[1]
		ip := fields[2]
		hostname := fields[3]
		clientID := ""
		if len(fields) >= 5 {
			clientID = fields[4]
		}
		out = append(out, dnsmasqLease{
			Expires:  exp,
			MAC:      mac,
			IP:       ip,
			Hostname: hostname,
			ClientID: clientID,
		})
	}
	return out
}

func (s *Server) isMacBlocked(mac string) bool {
	if mac == "" {
		return false
	}
	m := strings.ToLower(mac)
	for _, b := range s.cfg.Firewall.BlockedMACs {
		if strings.ToLower(b) == m {
			return true
		}
	}
	return false
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

func execCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Start()
}

func execRun(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

func (s *Server) attemptReload() (bool, string) {
	// Best-effort reload using systemctl when available
	if err := s.runner.LookPath("systemctl"); err != nil {
		return false, "systemctl not found"
	}
	units := []string{"dnsmasq.service", "nftables.service", "hostapd.service"}
	var errs []string
	for _, u := range units {
		// try-reload-or-restart is safe; -q for quiet
		if err := s.runner.Run("systemctl", "-q", "try-reload-or-restart", u); err != nil {
			// accumulate but continue; some units may be absent
			errs = append(errs, fmt.Sprintf("%s: %v", u, err))
		}
	}
	if len(errs) > 0 && len(errs) == len(units) {
		return false, "no units reloaded: " + strings.Join(errs, "; ")
	}
	if len(errs) > 0 {
		// partial success considered failure for appliedRuntime
		return false, "partial reload: " + strings.Join(errs, "; ")
	}
	return true, ""
}

// reloadCoreUnits mirrors attemptReload but is explicit for core daemons
func (s *Server) reloadCoreUnits() (bool, string) {
	return s.attemptReload()
}

// orchestratePlugins performs best-effort unit orchestration per plugin.
// It NEVER fails the apply; instead it returns human-readable notes.
func (s *Server) orchestratePlugins(cfg config.Config) []string {
	notes := []string{}
	// If systemctl is missing, skip silently with a note.
	if err := s.runner.LookPath("systemctl"); err != nil {
		return append(notes, "systemctl not found, skipped plugin orchestration")
	}
	type plug struct {
		name  string
		units []string
		enabled bool
		extraHeadscale bool
	}
	var planned []plug
	// mihomo
	if pc, ok := cfg.Plugins["mihomo"]; ok {
		planned = append(planned, plug{name: "mihomo", enabled: pc.Enable, units: []string{"mihomo.service"}})
	}
	// tailscale
	if pc, ok := cfg.Plugins["tailscale"]; ok {
		cp, _ := pc.Config["controlPlane"].(string)
		p := plug{name: "tailscale", enabled: pc.Enable, units: []string{"tailscaled.service"}}
		if strings.EqualFold(cp, "headscale") {
			p.extraHeadscale = true
			p.units = append(p.units, "headscale.service")
		}
		planned = append(planned, p)
	}
	// zerotier
	if pc, ok := cfg.Plugins["zerotier"]; ok {
		planned = append(planned, plug{name: "zerotier", enabled: pc.Enable, units: []string{"zerotier-one.service"}})
	}
	// vlan: notes-only, no units
	if pc, ok := cfg.Plugins["vlan"]; ok {
		if pc.Enable {
			notes = append(notes, "vlan: notes-only; manage via systemd-networkd if configured")
		} else {
			notes = append(notes, "vlan: disabled; no units to stop")
		}
	}
	for _, p := range planned {
		if len(p.units) == 0 {
			continue
		}
		if p.enabled {
			for _, u := range p.units {
				if err := s.runner.Run("systemctl", "-q", "try-reload-or-restart", u); err != nil {
					notes = append(notes, fmt.Sprintf("%s: unit %s missing or not reloadable (%v)", p.name, u, err))
				} else {
					notes = append(notes, fmt.Sprintf("%s: reloaded %s", p.name, u))
				}
			}
		} else {
			for _, u := range p.units {
				if err := s.runner.Run("systemctl", "-q", "try-stop", u); err != nil {
					notes = append(notes, fmt.Sprintf("%s: unit %s not running or absent (%v)", p.name, u, err))
				} else {
					notes = append(notes, fmt.Sprintf("%s: stopped %s", p.name, u))
				}
			}
		}
	}
	return notes
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

