package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func setupApplyEnv(t *testing.T, valid bool) (*Server, string, []*http.Cookie, func()) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	var cfg config.Config
	if valid {
		cfg = config.DefaultConfig()
		cfg.System.Hostname = "apply-node"
		b := new(bytes.Buffer)
		_ = json.NewEncoder(b).Encode(cfg) // use json to write but bypass SaveToFile to avoid side-effects
		// write via SaveToFile to ensure format; it's ok here since valid
		if err := config.SaveToFile(cfgPath, cfg); err != nil {
			t.Fatalf("save valid cfg: %v", err)
		}
	} else {
		// invalid WAN CIDR to trigger validation error
		raw := `{"system":{"hostname":"bad"},"wan":{"mode":"static","static":{"addressCidr":"bad-cidr"}},"lan":{"bridgeName":"br-lan","ipv4Cidr":"192.168.1.1/24","dhcp":{"enable":true,"rangeStart":"192.168.1.100","rangeEnd":"192.168.1.200","leaseMins":60}},"dns":{"enableDnsmasq":true},"wifi":{"enable":false,"bridgeToLan":true},"firewall":{"enable":true,"natEnabled":true},"ssh":{"enable":true},"plugins":{}}`
		if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
			t.Fatalf("write invalid cfg: %v", err)
		}
	}
	database, err := db.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// seed admin
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	if err := database.CreateUser("admin", string(hash)); err != nil {
		t.Fatalf("create user: %v", err)
	}
	srv := New(Options{
		Config:     config.DefaultConfig(),
		ConfigPath: cfgPath,
		StateDir:   dir,
		DB:         database,
		Version:    "test",
		DevMode:    false,
	})
	// login to get cookie
	body := map[string]string{"username": "admin", "password": "secret"}
	bs, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewReader(bs))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	cleanup := func() {
		_ = database.Close()
		_ = os.RemoveAll(dir)
	}
	return srv, dir, cookies, cleanup
}

func TestApplyUnauthorized(t *testing.T) {
	srv, _, _, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apply", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestApplyInvalidConfigNoFiles(t *testing.T) {
	srv, dir, cookies, cleanup := setupApplyEnv(t, false)
	defer cleanup()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apply", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid config, got %d", w.Code)
	}
	// no generated files
	gen := filepath.Join(dir, "generated")
	if _, err := os.Stat(filepath.Join(gen, "dnsmasq.conf.fragment")); !os.IsNotExist(err) {
		t.Fatalf("dnsmasq fragment should not exist")
	}
}

func TestApplyValidGeneratesFiles(t *testing.T) {
	srv, dir, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apply", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	// get job id and fetch it
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	jid, _ := resp["jobId"].(string)
	if jid == "" {
		t.Fatalf("missing jobId")
	}
	reqJ := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jid, nil)
	for _, c := range cookies {
		reqJ.AddCookie(c)
	}
	wJ := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wJ, reqJ)
	if wJ.Code != http.StatusOK {
		t.Fatalf("expected 200 for job get, got %d", wJ.Code)
	}
	gen := filepath.Join(dir, "generated")
	for _, f := range []string{"dnsmasq.conf.fragment", "nftables.nft.fragment", "hostapd.conf.fragment"} {
		if _, err := os.Stat(filepath.Join(gen, f)); err != nil {
			t.Fatalf("expected %s to exist, err=%v", f, err)
		}
	}
	// extra notes may exist
	_ , _ = os.Stat(filepath.Join(gen, "ipv6.nft.fragment"))
	_ , _ = os.Stat(filepath.Join(gen, "ddns.env.fragment"))
}

