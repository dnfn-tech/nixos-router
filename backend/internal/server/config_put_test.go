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

func setupAuthedServerWithConfig(t *testing.T) (*Server, string, []*http.Cookie, func()) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	initial := config.DefaultConfig()
	initial.System.Hostname = "init"
	if err := config.SaveToFile(cfgPath, initial); err != nil {
		t.Fatalf("seed cfg: %v", err)
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
		Config:     initial,
		ConfigPath: cfgPath,
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
	return srv, cfgPath, cookies, cleanup
}

func TestPutConfigUnauthorized(t *testing.T) {
	srv, _, _, cleanup := setupAuthedServerWithConfig(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestPutConfigInvalidDoesNotWrite(t *testing.T) {
	srv, cfgPath, cookies, cleanup := setupAuthedServerWithConfig(t)
	defer cleanup()
	origBytes, _ := os.ReadFile(cfgPath)
	// invalid static WAN CIDR
	invalid := map[string]interface{}{
		"wan": map[string]interface{}{
			"mode": "static",
			"static": map[string]interface{}{
				"addressCidr": "not-a-cidr",
			},
		},
		"lan":    config.DefaultConfig().LAN,
		"dns":    config.DefaultConfig().DNS,
		"wifi":   config.DefaultConfig().WiFi,
		"ssh":    config.DefaultConfig().SSH,
		"system": config.DefaultConfig().System,
		"firewall": config.DefaultConfig().Firewall,
		"plugins": map[string]interface{}{},
	}
	bs, _ := json.Marshal(invalid)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(bs))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	newBytes, _ := os.ReadFile(cfgPath)
	if !bytes.Equal(origBytes, newBytes) {
		t.Fatalf("config file changed on invalid PUT")
	}
}

func TestPutConfigSuccessWritesAndReadsBack(t *testing.T) {
	srv, cfgPath, cookies, cleanup := setupAuthedServerWithConfig(t)
	defer cleanup()
	newCfg := config.DefaultConfig()
	newCfg.System.Hostname = "newname"
	wrap := map[string]interface{}{"config": newCfg}
	bs, _ := json.Marshal(wrap)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(bs))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// File content updated
	loaded, err := config.LoadFromFile(cfgPath)
	if err != nil {
		t.Fatalf("load file: %v", err)
	}
	if loaded.System.Hostname != "newname" {
		t.Fatalf("file not updated, got %s", loaded.System.Hostname)
	}
	// GET returns new value
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("get failed: %d", w2.Code)
	}
	var body struct {
		Config config.Config `json:"config"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if body.Config.System.Hostname != "newname" {
		t.Fatalf("GET mismatch, got %s", body.Config.System.Hostname)
	}
}

